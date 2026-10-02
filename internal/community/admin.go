// Administrative operations are deliberately local and explicit. Role checks
// happen at route registration; every mutation and immutable audit row commits
// in the same transaction.
package community

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

var taxonomySlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type AdminUser struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	rowID       int64
}

type ContentReviewItem struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	AuthorID    string `json:"author_id"`
	Status      string `json:"status"`
	Title       string `json:"title,omitempty"`
	Body        string `json:"body"`
	Visibility  string `json:"visibility,omitempty"`
	Pinned      bool   `json:"pinned,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
	OpenReports int    `json:"open_reports"`
	CreatedAt   string `json:"created_at"`
}

func insertAudit(ctx context.Context, tx *sql.Tx, actorID, action, targetType, targetID string, before, after any, reason, requestID string, now time.Time) error {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("encode audit before: %w", err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("encode audit after: %w", err)
	}
	auditID, err := platform.NewID("aud")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events(id, actor_id, action, target_type, target_id, before_value, after_value, reason, request_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, auditID, actorID, action, targetType, targetID,
		string(beforeJSON), string(afterJSON), reason, requestID, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

func adminCursorScope(prefix, actorID string, values ...string) string {
	return prefix + ":" + actorID + ":" + strings.Join(values, "\x00")
}

func (s *Service) AdminUsers(ctx context.Context, actorID, query, status, role, rawCursor string, limit int) ([]AdminUser, string, error) {
	query = strings.TrimSpace(query)
	status = strings.TrimSpace(status)
	role = strings.TrimSpace(role)
	fields := map[string][]string{}
	if len([]rune(query)) > 100 {
		fields["q"] = []string{"搜索词不能超过 100 个字符"}
	}
	if status != "" && status != "active" && status != "suspended" {
		fields["status"] = []string{"status 必须是 active 或 suspended"}
	}
	if role != "" && role != "member" && role != "moderator" && role != "admin" {
		fields["role"] = []string{"role 必须是 member、moderator 或 admin"}
	}
	if len(fields) > 0 {
		return nil, "", platform.Validation(fields)
	}
	scope := adminCursorScope("admin-users", actorID, query, status, role)
	cursor, err := decodeSeekCursor(rawCursor, scope)
	if err != nil {
		return nil, "", err
	}
	if cursor.Timestamp != "" || cursor.ID != "" || (rawCursor != "" && cursor.Sequence == 0) {
		return nil, "", platform.Validation(map[string][]string{"cursor": {"游标无效"}})
	}
	where := []string{"1 = 1"}
	args := []any{}
	if query != "" {
		pattern := "%" + escapeLike(query) + "%"
		where = append(where, `(u.handle LIKE ? ESCAPE '\' OR u.display_name LIKE ? ESCAPE '\' OR u.email LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	if status != "" {
		where = append(where, "u.status = ?")
		args = append(args, status)
	}
	if role != "" {
		where = append(where, "u.role = ?")
		args = append(args, role)
	}
	if rawCursor != "" {
		where = append(where, "u.rowid < ?")
		args = append(args, cursor.Sequence)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.rowid, u.id, u.handle, u.email, u.display_name, u.role, u.status, u.created_at, COALESCE(u.updated_at, '')
		FROM users u WHERE `+strings.Join(where, " AND ")+` ORDER BY u.rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list admin users: %w", err)
	}
	defer rows.Close()
	items := []AdminUser{}
	for rows.Next() {
		var item AdminUser
		if err := rows.Scan(&item.rowID, &item.ID, &item.Handle, &item.Email, &item.DisplayName, &item.Role, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, "", fmt.Errorf("scan admin user: %w", err)
		}
		items = append(items, item)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	next := ""
	if hasMore && len(items) > 0 {
		next = encodeSeekCursor(seekCursor{Scope: scope, Sequence: items[len(items)-1].rowID})
	}
	return items, next, rows.Err()
}

func (s *Service) UpdateUserStatus(ctx context.Context, actor identity.User, requestID, targetID, status, reason string) (AdminUser, bool, error) {
	status = strings.TrimSpace(status)
	reason = strings.TrimSpace(reason)
	fields := map[string][]string{}
	if status != "active" && status != "suspended" {
		fields["status"] = []string{"status 必须是 active 或 suspended"}
	}
	if len([]rune(reason)) < 3 || len([]rune(reason)) > 500 {
		fields["reason"] = []string{"理由长度必须为 3 到 500 个字符"}
	}
	if len(fields) > 0 {
		return AdminUser{}, false, platform.Validation(fields)
	}
	if actor.ID == targetID && status == "suspended" {
		return AdminUser{}, false, platform.Problem(http.StatusConflict, "self_suspension_forbidden", "不能停用自己的管理员账号")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, false, fmt.Errorf("begin user status update: %w", err)
	}
	defer tx.Rollback()
	var item AdminUser
	err = tx.QueryRowContext(ctx, `SELECT rowid, id, handle, email, display_name, role, status, created_at, COALESCE(updated_at, '') FROM users WHERE id = ?`, targetID).
		Scan(&item.rowID, &item.ID, &item.Handle, &item.Email, &item.DisplayName, &item.Role, &item.Status, &item.CreatedAt, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		return AdminUser{}, false, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在")
	}
	if err != nil {
		return AdminUser{}, false, fmt.Errorf("load status target: %w", err)
	}
	if item.Status == status {
		return item, false, nil
	}
	if status == "suspended" && item.Role == "admin" {
		var activeAdmins int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND status = 'active'`).Scan(&activeAdmins); err != nil {
			return AdminUser{}, false, fmt.Errorf("count active admins: %w", err)
		}
		if activeAdmins <= 1 {
			return AdminUser{}, false, platform.Problem(http.StatusConflict, "last_admin_required", "至少需要保留一个启用的管理员")
		}
	}
	now := s.now().UTC()
	before := map[string]string{"status": item.Status, "role": item.Role}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET status = ?, updated_at = ? WHERE id = ? AND status = ?`, status, now.Format(time.RFC3339Nano), targetID, item.Status); err != nil {
		return AdminUser{}, false, fmt.Errorf("update user status: %w", err)
	}
	if status == "suspended" {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now.Format(time.RFC3339Nano), targetID); err != nil {
			return AdminUser{}, false, fmt.Errorf("revoke suspended user sessions: %w", err)
		}
	}
	if err := insertAudit(ctx, tx, actor.ID, "user_status_update", "user", targetID, before,
		map[string]string{"status": status, "role": item.Role}, reason, requestID, now); err != nil {
		return AdminUser{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, false, fmt.Errorf("commit user status update: %w", err)
	}
	item.Status = status
	item.UpdatedAt = now.Format(time.RFC3339Nano)
	return item, true, nil
}

func (s *Service) AdminContent(ctx context.Context, kind, status, authorID, reportStatus, query string, offset, limit int) ([]ContentReviewItem, string, error) {
	kind = strings.TrimSpace(kind)
	status = strings.TrimSpace(status)
	authorID = strings.TrimSpace(authorID)
	reportStatus = strings.TrimSpace(reportStatus)
	query = strings.TrimSpace(query)
	if kind == "" {
		kind = "all"
	}
	fields := map[string][]string{}
	if kind != "all" && kind != "post" && kind != "comment" {
		fields["type"] = []string{"type 必须是 all、post 或 comment"}
	}
	if status != "" && status != "draft" && status != "pending" && status != "published" && status != "rejected" && status != "deleted" {
		fields["status"] = []string{"status 必须是 draft、pending、published、rejected 或 deleted"}
	}
	if reportStatus != "" && reportStatus != "open" && reportStatus != "dismissed" && reportStatus != "actioned" {
		fields["report_status"] = []string{"report_status 必须是 open、dismissed 或 actioned"}
	}
	if len([]rune(query)) > 100 {
		fields["q"] = []string{"搜索词不能超过 100 个字符"}
	}
	if len(fields) > 0 {
		return nil, "", platform.Validation(fields)
	}
	where := []string{"1 = 1"}
	args := []any{}
	if kind != "all" {
		where = append(where, "content_type = ?")
		args = append(args, kind)
	}
	if status != "" {
		where = append(where, "content_status = ?")
		args = append(args, status)
	}
	if authorID != "" {
		where = append(where, "author_id = ?")
		args = append(args, authorID)
	}
	if reportStatus != "" {
		where = append(where, `EXISTS (SELECT 1 FROM reports r WHERE r.target_type = content_type AND r.target_id = content_id AND r.status = ?)`)
		args = append(args, reportStatus)
	}
	if query != "" {
		pattern := "%" + escapeLike(query) + "%"
		where = append(where, `(content_title LIKE ? ESCAPE '\' OR content_body LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern)
	}
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT content_type, content_id, author_id, content_status, content_title, content_body, visibility, is_pinned, is_recommended,
		       (SELECT COUNT(*) FROM reports r WHERE r.target_type = content_type AND r.target_id = content_id AND r.status = 'open'), created_at
		FROM (
			SELECT 'post' AS content_type, p.id AS content_id, p.author_id,
			       COALESCE((SELECT state FROM post_moderation WHERE post_id = p.id), p.status) AS content_status,
			       p.title AS content_title, p.body AS content_body, p.visibility,
			       COALESCE((SELECT is_pinned FROM post_moderation WHERE post_id = p.id), 0) AS is_pinned,
			       COALESCE((SELECT is_recommended FROM post_moderation WHERE post_id = p.id), 0) AS is_recommended, p.created_at
			FROM posts p
			UNION ALL
			SELECT 'comment', c.id, c.author_id, c.status, '', c.body, '', 0, 0, c.created_at
			FROM comments c
		) review_content
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY created_at DESC, content_type ASC, content_id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list admin content: %w", err)
	}
	defer rows.Close()
	items := []ContentReviewItem{}
	for rows.Next() {
		var item ContentReviewItem
		if err := rows.Scan(&item.Type, &item.ID, &item.AuthorID, &item.Status, &item.Title, &item.Body, &item.Visibility, &item.Pinned, &item.Recommended, &item.OpenReports, &item.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("scan admin content: %w", err)
		}
		items = append(items, item)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return items, next, rows.Err()
}

func validateTaxonomyInput(slug, name string) (string, string, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	name = strings.TrimSpace(name)
	fields := map[string][]string{}
	if len(slug) < 2 || len(slug) > 50 || !taxonomySlugPattern.MatchString(slug) {
		fields["slug"] = []string{"slug 必须是 2 到 50 位小写字母、数字或单连字符"}
	}
	if len([]rune(name)) < 1 || len([]rune(name)) > 50 {
		fields["name"] = []string{"名称长度必须为 1 到 50 个字符"}
	}
	if len(fields) > 0 {
		return "", "", platform.Validation(fields)
	}
	return slug, name, nil
}

func (s *Service) CreateTaxonomy(ctx context.Context, actor identity.User, requestID, kind, slug, name string) (any, error) {
	slug, name, err := validateTaxonomyInput(slug, name)
	if err != nil {
		return nil, err
	}
	table, prefix := "categories", "cat"
	if kind == "tag" {
		table, prefix = "tags", "tag"
	} else if kind != "category" {
		return nil, platform.Validation(map[string][]string{"type": {"分类类型无效"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin taxonomy create: %w", err)
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM `+table+` WHERE lower(slug) = lower(?) OR lower(name) = lower(?)`, slug, name).Scan(&existing)
	if err == nil {
		return nil, platform.Problem(http.StatusConflict, "taxonomy_conflict", "slug 或名称已经存在")
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("check taxonomy conflict: %w", err)
	}
	id, err := platform.NewID(prefix)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if kind == "tag" {
		_, err = tx.ExecContext(ctx, `INSERT INTO tags(id, slug, name, created_at) VALUES (?, ?, ?, ?)`, id, slug, name, now.Format(time.RFC3339Nano))
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO categories(id, slug, name) VALUES (?, ?, ?)`, id, slug, name)
	}
	if err != nil {
		return nil, fmt.Errorf("create taxonomy: %w", err)
	}
	if err := insertAudit(ctx, tx, actor.ID, "taxonomy_create", kind, id, map[string]any{}, map[string]string{"slug": slug, "name": name}, "创建分类项", requestID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit taxonomy create: %w", err)
	}
	if kind == "tag" {
		return Tag{ID: id, Slug: slug, Name: name}, nil
	}
	return Category{ID: id, Slug: slug, Name: name}, nil
}

func (s *Service) UpdateTaxonomy(ctx context.Context, actor identity.User, requestID, kind, id string, slugInput, nameInput *string) (any, error) {
	table := "categories"
	if kind == "tag" {
		table = "tags"
	} else if kind != "category" {
		return nil, platform.Validation(map[string][]string{"type": {"分类类型无效"}})
	}
	if slugInput == nil && nameInput == nil {
		return nil, platform.Validation(map[string][]string{"body": {"至少提供 slug 或 name"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin taxonomy update: %w", err)
	}
	defer tx.Rollback()
	var oldSlug, oldName string
	if err := tx.QueryRowContext(ctx, `SELECT slug, name FROM `+table+` WHERE id = ?`, id).Scan(&oldSlug, &oldName); err == sql.ErrNoRows {
		return nil, platform.Problem(http.StatusNotFound, "taxonomy_not_found", "分类项不存在")
	} else if err != nil {
		return nil, fmt.Errorf("load taxonomy: %w", err)
	}
	slug, name := oldSlug, oldName
	if slugInput != nil {
		slug = *slugInput
	}
	if nameInput != nil {
		name = *nameInput
	}
	slug, name, err = validateTaxonomyInput(slug, name)
	if err != nil {
		return nil, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM `+table+` WHERE id <> ? AND (lower(slug) = lower(?) OR lower(name) = lower(?))`, id, slug, name).Scan(&existing)
	if err == nil {
		return nil, platform.Problem(http.StatusConflict, "taxonomy_conflict", "slug 或名称已经存在")
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("check taxonomy update conflict: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET slug = ?, name = ? WHERE id = ?`, slug, name, id); err != nil {
		return nil, fmt.Errorf("update taxonomy: %w", err)
	}
	now := s.now().UTC()
	if err := insertAudit(ctx, tx, actor.ID, "taxonomy_update", kind, id,
		map[string]string{"slug": oldSlug, "name": oldName}, map[string]string{"slug": slug, "name": name}, "更新分类项", requestID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit taxonomy update: %w", err)
	}
	if kind == "tag" {
		return Tag{ID: id, Slug: slug, Name: name}, nil
	}
	return Category{ID: id, Slug: slug, Name: name}, nil
}

func (s *Service) DeleteTaxonomy(ctx context.Context, actor identity.User, requestID, kind, id, reason string) error {
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 3 || len([]rune(reason)) > 500 {
		return platform.Validation(map[string][]string{"reason": {"删除理由长度必须为 3 到 500 个字符"}})
	}
	table, relation, relationColumn := "categories", "posts", "category_id"
	if kind == "tag" {
		table, relation, relationColumn = "tags", "post_tags", "tag_id"
	} else if kind != "category" {
		return platform.Validation(map[string][]string{"type": {"分类类型无效"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin taxonomy delete: %w", err)
	}
	defer tx.Rollback()
	var slug, name string
	if err := tx.QueryRowContext(ctx, `SELECT slug, name FROM `+table+` WHERE id = ?`, id).Scan(&slug, &name); err == sql.ErrNoRows {
		return platform.Problem(http.StatusNotFound, "taxonomy_not_found", "分类项不存在")
	} else if err != nil {
		return fmt.Errorf("load taxonomy delete: %w", err)
	}
	var uses int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+relation+` WHERE `+relationColumn+` = ?`, id).Scan(&uses); err != nil {
		return fmt.Errorf("count taxonomy references: %w", err)
	}
	if uses > 0 {
		return platform.Problem(http.StatusConflict, "taxonomy_in_use", "分类项仍被内容使用，不能删除")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete taxonomy: %w", err)
	}
	now := s.now().UTC()
	if err := insertAudit(ctx, tx, actor.ID, "taxonomy_delete", kind, id, map[string]string{"slug": slug, "name": name}, map[string]any{}, reason, requestID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit taxonomy delete: %w", err)
	}
	return nil
}

func (s *Service) adminUsersHTTP(w http.ResponseWriter, r *http.Request) {
	limit, err := validateLimit(r.URL.Query().Get("limit"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	items, next, err := s.AdminUsers(r.Context(), actor.ID, r.URL.Query().Get("q"), r.URL.Query().Get("status"), r.URL.Query().Get("role"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) updateUserStatusHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	item, changed, err := s.UpdateUserStatus(r.Context(), actor, platform.RequestID(r.Context()), chi.URLParam(r, "userID"), input.Status, input.Reason)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"user": item, "changed": changed})
}

func (s *Service) adminContentHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	items, next, err := s.AdminContent(r.Context(), r.URL.Query().Get("type"), r.URL.Query().Get("status"), r.URL.Query().Get("author_id"), r.URL.Query().Get("report_status"), r.URL.Query().Get("q"), offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func taxonomyKind(r *http.Request) string {
	if strings.Contains(r.URL.Path, "/tags") {
		return "tag"
	}
	return "category"
}

func taxonomyID(r *http.Request) string {
	if id := chi.URLParam(r, "tagID"); id != "" {
		return id
	}
	return chi.URLParam(r, "categoryID")
}

func (s *Service) createTaxonomyHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	item, err := s.CreateTaxonomy(r.Context(), actor, platform.RequestID(r.Context()), taxonomyKind(r), input.Slug, input.Name)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusCreated, item)
}

func (s *Service) updateTaxonomyHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Slug *string `json:"slug"`
		Name *string `json:"name"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	item, err := s.UpdateTaxonomy(r.Context(), actor, platform.RequestID(r.Context()), taxonomyKind(r), taxonomyID(r), input.Slug, input.Name)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) deleteTaxonomyHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	if err := s.DeleteTaxonomy(r.Context(), actor, platform.RequestID(r.Context()), taxonomyKind(r), taxonomyID(r), input.Reason); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"deleted": true})
}
