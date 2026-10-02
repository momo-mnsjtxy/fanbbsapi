// Identity service owns passwords and device sessions.
// Login issues a 15-minute access token plus a rotating opaque refresh token.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"fanbbs.local/backend/internal/platform"
	"golang.org/x/crypto/argon2"
)

const (
	accessLifetime  = 15 * time.Minute
	refreshLifetime = 30 * 24 * time.Hour
)

type Service struct {
	db           *sql.DB
	now          func() time.Time
	loginLimiter *platform.RateLimiter
}

var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,24}$`)

type RegisterInput struct {
	Handle      string `json:"handle"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type ProfileInput struct {
	DisplayName *string `json:"display_name"`
	Bio         *string `json:"bio"`
	AvatarURL   *string `json:"avatar_url"`
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now, loginLimiter: platform.NewRateLimiter(4096, 10, time.Minute)}
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (Session, error) {
	input.Handle = strings.ToLower(strings.TrimSpace(input.Handle))
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	fields := map[string][]string{}
	if !handlePattern.MatchString(input.Handle) {
		fields["handle"] = []string{"用户名必须为 3 到 24 位小写字母、数字或下划线"}
	}
	address, err := mail.ParseAddress(input.Email)
	if err != nil || address.Address != input.Email {
		fields["email"] = []string{"邮箱格式不正确"}
	}
	if len(input.Password) < 8 || len(input.Password) > 128 {
		fields["password"] = []string{"密码长度必须为 8 到 128 个字符"}
	}
	if input.DisplayName == "" {
		input.DisplayName = input.Handle
	}
	if len([]rune(input.DisplayName)) > 50 {
		fields["display_name"] = []string{"显示名称不能超过 50 个字符"}
	}
	if len(fields) > 0 {
		return Session{}, platform.Validation(fields)
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE lower(handle) = ? OR lower(email) = ?`, input.Handle, input.Email).Scan(&existing); err != nil {
		return Session{}, fmt.Errorf("check registration identity: %w", err)
	}
	if existing > 0 {
		return Session{}, platform.Problem(http.StatusConflict, "identity_in_use", "用户名或邮箱已被使用")
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		return Session{}, err
	}
	userID, err := platform.NewID("usr")
	if err != nil {
		return Session{}, err
	}
	now := s.now().UTC()
	user := User{ID: userID, Handle: input.Handle, Email: input.Email, Name: input.DisplayName, DisplayName: input.DisplayName, Role: "member", Status: "active", CreatedAt: now.Format(time.RFC3339Nano)}
	session, err := makeSession(user, now)
	if err != nil {
		return Session{}, err
	}
	recoveryCodes, recoveryRecords, err := newRecoveryCodes(8)
	if err != nil {
		return Session{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin registration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users(id, handle, email, password_hash, display_name, role, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'member', 'active', ?, ?)`, user.ID, user.Handle, user.Email, passwordHash, user.DisplayName, user.CreatedAt, user.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Session{}, platform.Problem(http.StatusConflict, "identity_in_use", "用户名或邮箱已被使用")
		}
		return Session{}, fmt.Errorf("create account: %w", err)
	}
	if err := insertSession(ctx, tx, session, user.ID, now); err != nil {
		return Session{}, err
	}
	if err := insertRecoveryCodes(ctx, tx, user.ID, recoveryRecords, now); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit registration: %w", err)
	}
	session.RecoveryCodes = recoveryCodes
	return session, nil
}

func (s *Service) User(ctx context.Context, userID string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, handle, email, display_name, avatar_url, bio, role, status, created_at
		FROM users WHERE id = ?`, userID).Scan(&user.ID, &user.Handle, &user.Email, &user.DisplayName,
		&user.AvatarURL, &user.Bio, &user.Role, &user.Status, &user.CreatedAt)
	if err == sql.ErrNoRows {
		return User{}, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在")
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	user.Name = user.DisplayName
	return user, nil
}

func (s *Service) UpdateProfile(ctx context.Context, userID string, input ProfileInput) (User, error) {
	fields := map[string][]string{}
	if input.DisplayName == nil && input.Bio == nil && input.AvatarURL == nil {
		fields["profile"] = []string{"至少提供一个需要更新的字段"}
	}
	if input.DisplayName != nil {
		value := strings.TrimSpace(*input.DisplayName)
		input.DisplayName = &value
		if value == "" || len([]rune(value)) > 50 {
			fields["display_name"] = []string{"显示名称长度必须为 1 到 50 个字符"}
		} else {
			var exists int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE lower(display_name) = lower(?) AND id <> ? AND status = 'active'`, value, userID).Scan(&exists); err != nil {
				return User{}, fmt.Errorf("check display name: %w", err)
			}
			if exists > 0 {
				return User{}, platform.Problem(http.StatusConflict, "display_name_in_use", "显示名称已被使用")
			}
		}
	}
	if input.Bio != nil {
		value := strings.TrimSpace(*input.Bio)
		input.Bio = &value
		if len([]rune(value)) > 500 {
			fields["bio"] = []string{"个人简介不能超过 500 个字符"}
		}
	}
	if input.AvatarURL != nil {
		value := strings.TrimSpace(*input.AvatarURL)
		input.AvatarURL = &value
		if len(value) > 500 {
			fields["avatar_url"] = []string{"头像地址不能超过 500 个字符"}
		} else if value != "" {
			parsed, err := url.ParseRequestURI(value)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				fields["avatar_url"] = []string{"头像地址必须是有效的 http 或 https URL"}
			}
		}
	}
	if len(fields) > 0 {
		return User{}, platform.Validation(fields)
	}
	current, err := s.User(ctx, userID)
	if err != nil {
		return User{}, err
	}
	if input.DisplayName != nil {
		current.DisplayName = *input.DisplayName
	}
	if input.Bio != nil {
		current.Bio = *input.Bio
	}
	if input.AvatarURL != nil {
		current.AvatarURL = *input.AvatarURL
	}
	_, err = s.db.ExecContext(ctx, `UPDATE users SET display_name = ?, bio = ?, avatar_url = ?, updated_at = ? WHERE id = ? AND status = 'active'`,
		current.DisplayName, current.Bio, current.AvatarURL, s.now().UTC().Format(time.RFC3339Nano), userID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, platform.Problem(http.StatusConflict, "display_name_in_use", "显示名称已被使用")
		}
		return User{}, fmt.Errorf("update profile: %w", err)
	}
	return s.User(ctx, userID)
}

func (s *Service) ChangePassword(ctx context.Context, userID, currentSessionID, currentPassword, newPassword string) error {
	fields := map[string][]string{}
	if currentPassword == "" || len(newPassword) < 8 || len(newPassword) > 128 {
		if currentPassword == "" {
			fields["current_password"] = []string{"当前密码不能为空"}
		}
		if len(newPassword) < 8 || len(newPassword) > 128 {
			fields["new_password"] = []string{"新密码长度必须为 8 到 128 个字符"}
		}
		return platform.Validation(fields)
	}
	var encoded string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ? AND status = 'active'`, userID).Scan(&encoded); err != nil {
		return fmt.Errorf("load password: %w", err)
	}
	if !checkPassword(currentPassword, encoded) {
		return platform.Problem(http.StatusUnauthorized, "invalid_current_password", "当前密码不正确")
	}
	replacement, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`, replacement, now, now, userID); err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND id <> ? AND revoked_at IS NULL`, now, userID, currentSessionID); err != nil {
		return fmt.Errorf("revoke other sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}

func (s *Service) Deactivate(ctx context.Context, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account deactivation: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE users SET status = 'suspended', updated_at = ? WHERE id = ? AND status = 'active'`, now, userID); err != nil {
		return fmt.Errorf("deactivate account: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, userID); err != nil {
		return fmt.Errorf("revoke deactivated sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account deactivation: %w", err)
	}
	return nil
}

func (s *Service) Login(ctx context.Context, account, password string) (Session, error) {
	account = strings.TrimSpace(strings.ToLower(account))
	if account == "" || password == "" {
		return Session{}, platform.Validation(map[string][]string{
			"account":  {"账号不能为空"},
			"password": {"密码不能为空"},
		})
	}

	var user User
	var encodedPassword string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, handle, email, password_hash, display_name, avatar_url, bio, role, status, created_at
		FROM users WHERE lower(email) = ? OR lower(handle) = ?`, account, account).Scan(
		&user.ID, &user.Handle, &user.Email, &encodedPassword, &user.DisplayName,
		&user.AvatarURL, &user.Bio, &user.Role, &user.Status, &user.CreatedAt,
	)
	if err == sql.ErrNoRows || (err == nil && !checkPassword(password, encodedPassword)) {
		return Session{}, platform.Problem(http.StatusUnauthorized, "invalid_credentials", "账号或密码不正确")
	}
	if err != nil {
		return Session{}, fmt.Errorf("load login account: %w", err)
	}
	if user.Status != "active" {
		return Session{}, platform.Problem(http.StatusForbidden, "account_suspended", "账号已被停用")
	}
	user.Name = user.DisplayName
	return s.issueSession(ctx, user)
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return Session{}, platform.Validation(map[string][]string{"refresh_token": {"刷新令牌不能为空"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin refresh: %w", err)
	}
	defer tx.Rollback()

	var oldSessionID, expiresAt string
	var user User
	err = tx.QueryRowContext(ctx, `
		SELECT s.id, s.refresh_expires_at, u.id, u.handle, u.email, u.display_name,
		       u.avatar_url, u.bio, u.role, u.status, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.refresh_hash = ? AND s.revoked_at IS NULL`, tokenHash(refreshToken)).Scan(
		&oldSessionID, &expiresAt, &user.ID, &user.Handle, &user.Email, &user.DisplayName,
		&user.AvatarURL, &user.Bio, &user.Role, &user.Status, &user.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return Session{}, platform.Problem(http.StatusUnauthorized, "invalid_refresh_token", "刷新令牌无效或已使用")
	}
	if err != nil {
		return Session{}, fmt.Errorf("load refresh session: %w", err)
	}
	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || !expiry.After(s.now().UTC()) || user.Status != "active" {
		return Session{}, platform.Problem(http.StatusUnauthorized, "expired_refresh_token", "刷新令牌已过期")
	}
	user.Name = user.DisplayName

	newSession, err := makeSession(user, s.now().UTC())
	if err != nil {
		return Session{}, err
	}
	if err := insertSession(ctx, tx, newSession, user.ID, s.now().UTC()); err != nil {
		return Session{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ?, replaced_by = ? WHERE id = ? AND revoked_at IS NULL`,
		s.now().UTC().Format(time.RFC3339Nano), newSession.SessionID, oldSessionID)
	if err != nil {
		return Session{}, fmt.Errorf("revoke rotated session: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return Session{}, platform.Problem(http.StatusUnauthorized, "invalid_refresh_token", "刷新令牌无效或已使用")
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit refresh: %w", err)
	}
	return newSession, nil
}

func (s *Service) Authenticate(ctx context.Context, accessToken string) (User, string, error) {
	if accessToken == "" {
		return User{}, "", platform.Problem(http.StatusUnauthorized, "authentication_required", "请先登录")
	}
	var user User
	var sessionID, expiresAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.access_expires_at, u.id, u.handle, u.email, u.display_name,
		       u.avatar_url, u.bio, u.role, u.status, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.access_hash = ? AND s.revoked_at IS NULL`, tokenHash(accessToken)).Scan(
		&sessionID, &expiresAt, &user.ID, &user.Handle, &user.Email, &user.DisplayName,
		&user.AvatarURL, &user.Bio, &user.Role, &user.Status, &user.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return User{}, "", platform.Problem(http.StatusUnauthorized, "invalid_access_token", "登录凭证无效")
	}
	if err != nil {
		return User{}, "", fmt.Errorf("authenticate session: %w", err)
	}
	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || !expiry.After(s.now().UTC()) {
		return User{}, "", platform.Problem(http.StatusUnauthorized, "expired_access_token", "登录凭证已过期")
	}
	if user.Status != "active" {
		return User{}, "", platform.Problem(http.StatusForbidden, "account_suspended", "账号已被停用")
	}
	user.Name = user.DisplayName
	return user, sessionID, nil
}

func (s *Service) Logout(ctx context.Context, userID, sessionID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		s.now().UTC().Format(time.RFC3339Nano), sessionID, userID)
	if err != nil {
		return fmt.Errorf("logout session: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return platform.Problem(http.StatusNotFound, "session_not_found", "会话不存在")
	}
	return nil
}

func (s *Service) issueSession(ctx context.Context, user User) (Session, error) {
	session, err := makeSession(user, s.now().UTC())
	if err != nil {
		return Session{}, err
	}
	if err := insertSession(ctx, s.db, session, user.ID, s.now().UTC()); err != nil {
		return Session{}, err
	}
	return session, nil
}

type sqlRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertSession(ctx context.Context, runner sqlRunner, session Session, userID string, now time.Time) error {
	_, err := runner.ExecContext(ctx, `
		INSERT INTO sessions(id, user_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, session.SessionID, userID,
		tokenHash(session.AccessToken), tokenHash(session.RefreshToken),
		now.Add(accessLifetime).Format(time.RFC3339Nano), now.Add(refreshLifetime).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func makeSession(user User, now time.Time) (Session, error) {
	sessionID, err := platform.NewID("ses")
	if err != nil {
		return Session{}, err
	}
	accessToken, err := platform.NewToken()
	if err != nil {
		return Session{}, err
	}
	refreshToken, err := platform.NewToken()
	if err != nil {
		return Session{}, err
	}
	return Session{SessionID: sessionID, AccessToken: accessToken, RefreshToken: refreshToken, TokenType: "Bearer", ExpiresIn: int(accessLifetime.Seconds()), User: user}, nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, 2, 32*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=32768,t=2,p=1$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func checkPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (s *Service) SeedDemo(ctx context.Context) error {
	now := s.now().UTC()
	for _, tag := range [][]string{{"tag_city", "city", "城市"}, {"tag_go", "go", "Go"}, {"tag_weekend", "weekend", "周末"}} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO tags(id, slug, name, created_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, tag[0], tag[1], tag[2], now.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("seed tag: %w", err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("check demo seed: %w", err)
	}
	if count > 0 {
		return nil
	}
	passwordHash, err := hashPassword("demo1234")
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin demo seed: %w", err)
	}
	defer tx.Rollback()
	users := [][]string{
		{"usr_demo", "demo", "demo@fanbbs.local", "演示用户", "记录社区里的新鲜事。"},
		{"usr_rain", "rain", "rain@fanbbs.local", "雨落南山", "记录城市与人的温柔连接。"},
		{"usr_forest", "forest", "forest@fanbbs.local", "林间风", "设计与旅行。"},
	}
	for _, user := range users {
		_, err = tx.ExecContext(ctx, `INSERT INTO users(id, handle, email, password_hash, display_name, bio, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			user[0], user[1], user[2], passwordHash, user[3], user[4], now.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("seed user: %w", err)
		}
	}
	for _, category := range [][]string{{"cat_life", "life", "生活"}, {"cat_tech", "tech", "技术"}, {"cat_photo", "photo", "摄影"}} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO categories(id, slug, name) VALUES (?, ?, ?)`, category[0], category[1], category[2]); err != nil {
			return fmt.Errorf("seed category: %w", err)
		}
	}
	posts := [][]string{
		{"post_morning", "usr_rain", "article", "在城市醒来之前，找到属于清晨的安静", "清晨五点半，路灯还没有熄灭。", "日常生活里，我们很少认真看见一座城市如何醒来。", "cat_photo", "18", "4", "1"},
		{"post_go", "usr_forest", "article", "把复杂系统写成一条能读懂的线", "从明确的数据来源开始。", "结构的价值是让下一位维护者更快找到答案。", "cat_tech", "9", "2", "0"},
		{"post_weekend", "usr_demo", "image", "周末沿河散步", "把天气和脚步都放慢一点。", "分享三张沿河散步时拍下的照片。", "cat_life", "3", "1", "0"},
	}
	for index, post := range posts {
		created := now.Add(-time.Duration(3-index) * time.Hour).Format(time.RFC3339Nano)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO posts(id, author_id, kind, title, summary, body, category_id, like_count, comment_count, repost_count, created_at, published_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, post[0], post[1], post[2], post[3], post[4], post[5], post[6], post[7], post[8], post[9], created, created)
		if err != nil {
			return fmt.Errorf("seed post: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO follows(follower_id, followed_id, created_at) VALUES ('usr_demo', 'usr_rain', ?)`, now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("seed follow: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO comments(id, post_id, author_id, body, created_at) VALUES ('cmt_seed', 'post_morning', 'usr_forest', '照片的光线太舒服了。', ?)`, now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("seed comment: %w", err)
	}
	for _, relation := range [][]string{{"post_morning", "tag_city"}, {"post_go", "tag_go"}, {"post_weekend", "tag_weekend"}} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO post_tags(post_id, tag_id) VALUES (?, ?)`, relation[0], relation[1]); err != nil {
			return fmt.Errorf("seed post tag: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit demo seed: %w", err)
	}
	return nil
}

// ParseBearer accepts the standard form and the old client's raw-token form.
func ParseBearer(header string) string {
	header = strings.TrimSpace(header)
	if len(header) >= 7 && strings.EqualFold(header[:7], "Bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return header
}
