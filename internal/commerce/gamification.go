package commerce

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func awardPoints(ctx context.Context, tx *sql.Tx, userID, eventType, eventKey, description string, amount int, now time.Time) (string, bool, error) {
	if amount < 1 || amount > 100000 {
		return "", false, platform.Validation(map[string][]string{"points": {"奖励积分必须在 1 到 100000 之间"}})
	}
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT id FROM local_point_events WHERE user_id=? AND event_type=? AND event_key=?`, userID, eventType, eventKey).Scan(&existing)
	if err == nil {
		return existing, true, nil
	}
	if err != sql.ErrNoRows {
		return "", false, err
	}
	id, err := platform.NewID("pte")
	if err != nil {
		return "", false, err
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO local_point_events(id,user_id,amount,event_type,event_key,description,created_at) VALUES(?,?,?,?,?,?,?)`, id, userID, amount, eventType, eventKey, description, stamp); err != nil {
		return "", false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_points(user_id,balance,lifetime_points,updated_at) VALUES(?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET balance=balance+excluded.balance,lifetime_points=lifetime_points+excluded.lifetime_points,updated_at=excluded.updated_at`, userID, amount, amount, stamp); err != nil {
		return "", false, fmt.Errorf("update local points: %w", err)
	}
	return id, false, nil
}

func (s *Service) CheckIn(ctx context.Context, userID string) (PointEvent, bool, error) {
	now := s.now().UTC()
	day := now.Format("2006-01-02")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PointEvent{}, false, err
	}
	defer tx.Rollback()
	var event PointEvent
	err = tx.QueryRowContext(ctx, `SELECT e.id,e.amount,e.event_type,e.event_key,e.description,e.created_at FROM daily_checkins c JOIN local_point_events e ON e.id=c.point_event_id WHERE c.user_id=? AND c.checkin_date=?`, userID, day).Scan(&event.ID, &event.Amount, &event.EventType, &event.EventKey, &event.Description, &event.CreatedAt)
	if err == nil {
		return event, true, nil
	}
	if err != sql.ErrNoRows {
		return PointEvent{}, false, err
	}
	eventID, _, err := awardPoints(ctx, tx, userID, "daily_checkin", day, "每日签到", 5, now)
	if err != nil {
		return PointEvent{}, false, err
	}
	stamp := now.Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO daily_checkins(user_id,checkin_date,point_event_id,created_at) VALUES(?,?,?,?)`, userID, day, eventID, stamp); err != nil {
		return PointEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return PointEvent{}, false, err
	}
	return PointEvent{ID: eventID, Amount: 5, EventType: "daily_checkin", EventKey: day, Description: "每日签到", CreatedAt: stamp}, false, nil
}

func (s *Service) PointEvents(ctx context.Context, userID string, offset, limit int) ([]PointEvent, string, error) {
	offset, limit = page(offset, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id,amount,event_type,event_key,description,created_at FROM local_point_events WHERE user_id=? ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, userID, limit+1, offset)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []PointEvent{}
	for rows.Next() {
		var item PointEvent
		if err := rows.Scan(&item.ID, &item.Amount, &item.EventType, &item.EventKey, &item.Description, &item.CreatedAt); err != nil {
			return nil, "", err
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

func (s *Service) GamificationStatus(ctx context.Context, userID string) (GamificationStatus, error) {
	var item GamificationStatus
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(p.balance,0),COALESCE(p.lifetime_points,0),l.level,l.name,l.title,1+(SELECT COUNT(*) FROM user_points r WHERE r.lifetime_points>COALESCE(p.lifetime_points,0)) FROM users u LEFT JOIN user_points p ON p.user_id=u.id JOIN gamification_levels l ON l.min_lifetime_points=(SELECT MAX(min_lifetime_points) FROM gamification_levels WHERE min_lifetime_points<=COALESCE(p.lifetime_points,0)) WHERE u.id=?`, userID).Scan(&item.Balance, &item.LifetimePoints, &item.Level, &item.LevelName, &item.Title, &item.Rank); err != nil {
		return item, err
	}
	var f AvatarFrame
	err := s.db.QueryRowContext(ctx, `SELECT f.id,f.slug,f.name,f.image_url,f.status,f.created_at,f.updated_at FROM user_avatar_frame_selection s JOIN avatar_frames f ON f.id=s.frame_id WHERE s.user_id=?`, userID).Scan(&f.ID, &f.Slug, &f.Name, &f.ImageURL, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err == nil {
		f.Entitled = true
		f.Selected = true
		item.SelectedFrame = &f
	} else if err != sql.ErrNoRows {
		return item, err
	}
	return item, nil
}

func (s *Service) Ranks(ctx context.Context, limit int) ([]RankEntry, error) {
	_, limit = page(0, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT p.user_id,u.display_name,p.lifetime_points,l.level,l.title FROM user_points p JOIN users u ON u.id=p.user_id JOIN gamification_levels l ON l.min_lifetime_points=(SELECT MAX(min_lifetime_points) FROM gamification_levels WHERE min_lifetime_points<=p.lifetime_points) WHERE u.status='active' ORDER BY p.lifetime_points DESC,p.user_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RankEntry{}
	rank := 0
	for rows.Next() {
		rank++
		var item RankEntry
		item.Rank = rank
		if err := rows.Scan(&item.UserID, &item.DisplayName, &item.LifetimePoints, &item.Level, &item.Title); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type TaskInput struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	RepeatPolicy  string `json:"repeat_policy"`
	RewardPoints  int    `json:"reward_points"`
	RewardFrameID string `json:"reward_frame_id"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
}

func validateTask(input TaskInput) (TaskInput, error) {
	var err error
	input.Code, err = validateCode(input.Code, "code")
	if err != nil {
		return input, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.RepeatPolicy = strings.TrimSpace(input.RepeatPolicy)
	input.RewardFrameID = strings.TrimSpace(input.RewardFrameID)
	input.Status = strings.TrimSpace(input.Status)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Status == "" {
		input.Status = "active"
	}
	fields := map[string][]string{}
	if len([]rune(input.Name)) < 1 || len([]rune(input.Name)) > 120 {
		fields["name"] = []string{"长度必须为 1 到 120 个字符"}
	}
	if len([]rune(input.Description)) > 1000 {
		fields["description"] = []string{"不能超过 1000 个字符"}
	}
	if input.RepeatPolicy != "once" && input.RepeatPolicy != "daily" {
		fields["repeat_policy"] = []string{"必须是 once 或 daily"}
	}
	if input.RewardPoints < 0 || input.RewardPoints > 100000 {
		fields["reward_points"] = []string{"必须在 0 到 100000 之间"}
	}
	if input.RewardPoints == 0 && input.RewardFrameID == "" {
		fields["reward"] = []string{"必须配置积分或头像框奖励"}
	}
	if input.Status != "active" && input.Status != "archived" {
		fields["status"] = []string{"必须是 active 或 archived"}
	}
	if len(fields) > 0 {
		return input, platform.Validation(fields)
	}
	return input, nil
}

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var item Task
	err := row.Scan(&item.ID, &item.Code, &item.Name, &item.Description, &item.RepeatPolicy, &item.RewardPoints, &item.RewardFrameID, &item.Status, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

const taskSelect = `SELECT id,code,name,description,repeat_policy,reward_points,COALESCE(reward_frame_id,''),status,created_at,updated_at FROM gamification_tasks`

func (s *Service) Tasks(ctx context.Context, userID string, includeArchived bool) ([]Task, error) {
	where := ` WHERE status='active'`
	if includeArchived {
		where = ""
	}
	rows, err := s.db.QueryContext(ctx, taskSelect+where+` ORDER BY created_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Task{}
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if userID != "" {
		day := s.now().UTC().Format("2006-01-02")
		for i := range items {
			key := "once"
			if items[i].RepeatPolicy == "daily" {
				key = day
			}
			var n int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_claims WHERE user_id=? AND task_id=? AND claim_key=?`, userID, items[i].ID, key).Scan(&n); err != nil {
				return nil, err
			}
			items[i].Claimed = n > 0
		}
	}
	return items, nil
}

func (s *Service) CreateTask(ctx context.Context, actor identity.User, requestID string, input TaskInput) (Task, error) {
	input, err := validateTask(input)
	if err != nil {
		return Task{}, err
	}
	id, err := platform.NewID("tsk")
	if err != nil {
		return Task{}, err
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	if input.RewardFrameID != "" {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM avatar_frames WHERE id=?`, input.RewardFrameID).Scan(&n); err != nil {
			return Task{}, err
		}
		if n == 0 {
			return Task{}, platform.Validation(map[string][]string{"reward_frame_id": {"头像框不存在"}})
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gamification_tasks(id,code,name,description,repeat_policy,reward_points,reward_frame_id,status,created_by,updated_by,created_at,updated_at) VALUES(?,?,?,?,?,?,NULLIF(?,''),?,?,?,?,?)`, id, input.Code, input.Name, input.Description, input.RepeatPolicy, input.RewardPoints, input.RewardFrameID, input.Status, actor.ID, actor.ID, stamp, stamp)
	if err != nil {
		return Task{}, conflictFromUnique(err, "task_code_in_use", "任务 code 已存在")
	}
	after := Task{ID: id, Code: input.Code, Name: input.Name, Description: input.Description, RepeatPolicy: input.RepeatPolicy, RewardPoints: input.RewardPoints, RewardFrameID: input.RewardFrameID, Status: input.Status, CreatedAt: stamp, UpdatedAt: stamp}
	if err := insertAudit(ctx, tx, actor.ID, "task_create", "gamification_task", id, map[string]any{}, after, input.Reason, requestID, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return after, nil
}

func (s *Service) UpdateTask(ctx context.Context, actor identity.User, requestID, id string, input TaskInput) (Task, error) {
	input, err := validateTask(input)
	if err != nil {
		return Task{}, err
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	before, err := scanTask(tx.QueryRowContext(ctx, taskSelect+` WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return Task{}, platform.Problem(http.StatusNotFound, "task_not_found", "任务不存在")
	}
	if err != nil {
		return Task{}, err
	}
	if input.RewardFrameID != "" {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM avatar_frames WHERE id=?`, input.RewardFrameID).Scan(&n); err != nil {
			return Task{}, err
		}
		if n == 0 {
			return Task{}, platform.Validation(map[string][]string{"reward_frame_id": {"头像框不存在"}})
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE gamification_tasks SET code=?,name=?,description=?,repeat_policy=?,reward_points=?,reward_frame_id=NULLIF(?,''),status=?,updated_by=?,updated_at=? WHERE id=?`, input.Code, input.Name, input.Description, input.RepeatPolicy, input.RewardPoints, input.RewardFrameID, input.Status, actor.ID, stamp, id)
	if err != nil {
		return Task{}, conflictFromUnique(err, "task_code_in_use", "任务 code 已存在")
	}
	after := Task{ID: id, Code: input.Code, Name: input.Name, Description: input.Description, RepeatPolicy: input.RepeatPolicy, RewardPoints: input.RewardPoints, RewardFrameID: input.RewardFrameID, Status: input.Status, CreatedAt: before.CreatedAt, UpdatedAt: stamp}
	if err := insertAudit(ctx, tx, actor.ID, "task_update", "gamification_task", id, before, after, input.Reason, requestID, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return after, nil
}

// AwardTask is the only HTTP task-award path. The caller is admin-authorized,
// and the explicit claim key identifies the verified local event/period. This
// avoids trusting a client to self-attest that an arbitrary task was completed.
func (s *Service) AwardTask(ctx context.Context, actor identity.User, requestID, userID, taskID, claimKey, reason string) (Task, bool, error) {
	claimKey = strings.TrimSpace(claimKey)
	reason = strings.TrimSpace(reason)
	if claimKey == "" || len(claimKey) > 128 {
		return Task{}, false, platform.Validation(map[string][]string{"claim_key": {"必填且不能超过 128 个字符"}})
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, false, err
	}
	defer tx.Rollback()
	var userExists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND status='active'`, userID).Scan(&userExists); err != nil {
		return Task{}, false, err
	}
	if userExists == 0 {
		return Task{}, false, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在")
	}
	task, err := scanTask(tx.QueryRowContext(ctx, taskSelect+` WHERE id=? AND status='active'`, taskID))
	if err == sql.ErrNoRows {
		return Task{}, false, platform.Problem(http.StatusNotFound, "task_not_found", "任务不存在")
	}
	if err != nil {
		return Task{}, false, err
	}
	evidenceKey := claimKey
	if task.RepeatPolicy == "once" {
		claimKey = "once"
	} else {
		// Daily tasks have a server-derived UTC period. The supplied key is
		// retained only in the audit evidence and cannot create extra awards.
		claimKey = now.Format("2006-01-02")
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM task_claims WHERE user_id=? AND task_id=? AND claim_key=?`, userID, taskID, claimKey).Scan(&existing)
	if err == nil {
		task.Claimed = true
		return task, true, nil
	}
	if err != sql.ErrNoRows {
		return Task{}, false, err
	}
	claimID, err := platform.NewID("tcl")
	if err != nil {
		return Task{}, false, err
	}
	var pointEvent any
	if task.RewardPoints > 0 {
		id, _, err := awardPoints(ctx, tx, userID, "task_reward", taskID+":"+claimKey, "任务奖励："+task.Name, task.RewardPoints, now)
		if err != nil {
			return Task{}, false, err
		}
		pointEvent = id
	}
	stamp := now.Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_claims(id,user_id,task_id,claim_key,point_event_id,created_at) VALUES(?,?,?,?,?,?)`, claimID, userID, taskID, claimKey, pointEvent, stamp); err != nil {
		return Task{}, false, err
	}
	if task.RewardFrameID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_avatar_frames(user_id,frame_id,source_type,source_id,granted_at) VALUES(?,?,'task_reward',?,?)`, userID, task.RewardFrameID, claimID, stamp); err != nil {
			return Task{}, false, err
		}
	}
	if err := insertAudit(ctx, tx, actor.ID, "task_reward_award", "user", userID, map[string]any{}, map[string]string{"task_id": taskID, "claim_key": claimKey, "evidence_key": evidenceKey}, reason, requestID, now); err != nil {
		return Task{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, false, err
	}
	task.Claimed = true
	return task, false, nil
}

type FrameInput struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	ImageURL string `json:"image_url"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
}

func validateFrame(input FrameInput) (FrameInput, error) {
	var err error
	input.Slug, err = validateCode(input.Slug, "slug")
	if err != nil {
		return input, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.ImageURL = strings.TrimSpace(input.ImageURL)
	input.Status = strings.TrimSpace(input.Status)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Status == "" {
		input.Status = "active"
	}
	fields := map[string][]string{}
	if len([]rune(input.Name)) < 1 || len([]rune(input.Name)) > 100 {
		fields["name"] = []string{"长度必须为 1 到 100 个字符"}
	}
	if input.ImageURL == "" || len(input.ImageURL) > 500 {
		fields["image_url"] = []string{"不能为空且不能超过 500 个字符"}
	}
	if input.Status != "active" && input.Status != "archived" {
		fields["status"] = []string{"必须是 active 或 archived"}
	}
	if len(fields) > 0 {
		return input, platform.Validation(fields)
	}
	return input, nil
}
func scanFrame(row interface{ Scan(...any) error }) (AvatarFrame, error) {
	var f AvatarFrame
	err := row.Scan(&f.ID, &f.Slug, &f.Name, &f.ImageURL, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

const frameSelect = `SELECT id,slug,name,image_url,status,created_at,updated_at FROM avatar_frames`

func (s *Service) Frames(ctx context.Context, userID string, includeArchived bool) ([]AvatarFrame, error) {
	where := ` WHERE f.status='active'`
	if includeArchived {
		where = ""
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,f.slug,f.name,f.image_url,f.status,f.created_at,f.updated_at,CASE WHEN uf.user_id IS NULL THEN 0 ELSE 1 END,CASE WHEN sel.user_id IS NULL THEN 0 ELSE 1 END,COALESCE(uf.granted_at,'') FROM avatar_frames f LEFT JOIN user_avatar_frames uf ON uf.frame_id=f.id AND uf.user_id=? LEFT JOIN user_avatar_frame_selection sel ON sel.frame_id=f.id AND sel.user_id=?`+where+` ORDER BY f.created_at DESC,f.id`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AvatarFrame{}
	for rows.Next() {
		var f AvatarFrame
		if err := rows.Scan(&f.ID, &f.Slug, &f.Name, &f.ImageURL, &f.Status, &f.CreatedAt, &f.UpdatedAt, &f.Entitled, &f.Selected, &f.GrantedAt); err != nil {
			return nil, err
		}
		items = append(items, f)
	}
	return items, rows.Err()
}
func (s *Service) CreateFrame(ctx context.Context, actor identity.User, requestID string, input FrameInput) (AvatarFrame, error) {
	input, err := validateFrame(input)
	if err != nil {
		return AvatarFrame{}, err
	}
	id, err := platform.NewID("frm")
	if err != nil {
		return AvatarFrame{}, err
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AvatarFrame{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO avatar_frames(id,slug,name,image_url,status,created_by,updated_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, input.Slug, input.Name, input.ImageURL, input.Status, actor.ID, actor.ID, stamp, stamp)
	if err != nil {
		return AvatarFrame{}, conflictFromUnique(err, "avatar_frame_in_use", "头像框名称或 slug 已存在")
	}
	after := AvatarFrame{ID: id, Slug: input.Slug, Name: input.Name, ImageURL: input.ImageURL, Status: input.Status, CreatedAt: stamp, UpdatedAt: stamp}
	if err := insertAudit(ctx, tx, actor.ID, "avatar_frame_create", "avatar_frame", id, map[string]any{}, after, input.Reason, requestID, now); err != nil {
		return AvatarFrame{}, err
	}
	if err := tx.Commit(); err != nil {
		return AvatarFrame{}, err
	}
	return after, nil
}
func (s *Service) UpdateFrame(ctx context.Context, actor identity.User, requestID, id string, input FrameInput) (AvatarFrame, error) {
	input, err := validateFrame(input)
	if err != nil {
		return AvatarFrame{}, err
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AvatarFrame{}, err
	}
	defer tx.Rollback()
	before, err := scanFrame(tx.QueryRowContext(ctx, frameSelect+` WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return AvatarFrame{}, platform.Problem(http.StatusNotFound, "avatar_frame_not_found", "头像框不存在")
	}
	if err != nil {
		return AvatarFrame{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE avatar_frames SET slug=?,name=?,image_url=?,status=?,updated_by=?,updated_at=? WHERE id=?`, input.Slug, input.Name, input.ImageURL, input.Status, actor.ID, stamp, id)
	if err != nil {
		return AvatarFrame{}, conflictFromUnique(err, "avatar_frame_in_use", "头像框名称或 slug 已存在")
	}
	if input.Status == "archived" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_avatar_frame_selection WHERE frame_id=?`, id); err != nil {
			return AvatarFrame{}, err
		}
	}
	after := AvatarFrame{ID: id, Slug: input.Slug, Name: input.Name, ImageURL: input.ImageURL, Status: input.Status, CreatedAt: before.CreatedAt, UpdatedAt: stamp}
	if err := insertAudit(ctx, tx, actor.ID, "avatar_frame_update", "avatar_frame", id, before, after, input.Reason, requestID, now); err != nil {
		return AvatarFrame{}, err
	}
	if err := tx.Commit(); err != nil {
		return AvatarFrame{}, err
	}
	return after, nil
}
func (s *Service) GrantFrame(ctx context.Context, actor identity.User, requestID, userID, frameID, reason string) (AvatarFrame, bool, error) {
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AvatarFrame{}, false, err
	}
	defer tx.Rollback()
	var userExists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND status='active'`, userID).Scan(&userExists); err != nil {
		return AvatarFrame{}, false, err
	}
	if userExists == 0 {
		return AvatarFrame{}, false, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在")
	}
	frame, err := scanFrame(tx.QueryRowContext(ctx, frameSelect+` WHERE id=?`, frameID))
	if err == sql.ErrNoRows {
		return AvatarFrame{}, false, platform.Problem(http.StatusNotFound, "avatar_frame_not_found", "头像框不存在")
	}
	if err != nil {
		return AvatarFrame{}, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_avatar_frames(user_id,frame_id,source_type,source_id,granted_at) VALUES(?,?,'admin_grant',?,?)`, userID, frameID, requestID, stamp)
	if err != nil {
		return AvatarFrame{}, false, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		frame.Entitled = true
		return frame, false, nil
	}
	if err := insertAudit(ctx, tx, actor.ID, "avatar_frame_grant", "user", userID, map[string]any{}, map[string]string{"frame_id": frameID}, strings.TrimSpace(reason), requestID, now); err != nil {
		return AvatarFrame{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return AvatarFrame{}, false, err
	}
	frame.Entitled = true
	frame.GrantedAt = stamp
	return frame, true, nil
}
func (s *Service) SelectFrame(ctx context.Context, userID, frameID string) (*AvatarFrame, error) {
	frameID = strings.TrimSpace(frameID)
	if frameID == "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM user_avatar_frame_selection WHERE user_id=?`, userID); err != nil {
			return nil, err
		}
		return nil, nil
	}
	var f AvatarFrame
	err := s.db.QueryRowContext(ctx, `SELECT f.id,f.slug,f.name,f.image_url,f.status,f.created_at,f.updated_at FROM user_avatar_frames uf JOIN avatar_frames f ON f.id=uf.frame_id WHERE uf.user_id=? AND uf.frame_id=? AND f.status='active'`, userID, frameID).Scan(&f.ID, &f.Slug, &f.Name, &f.ImageURL, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, platform.Problem(http.StatusForbidden, "avatar_frame_not_entitled", "尚未获得该头像框")
	}
	if err != nil {
		return nil, err
	}
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO user_avatar_frame_selection(user_id,frame_id,selected_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET frame_id=excluded.frame_id,selected_at=excluded.selected_at`, userID, frameID, stamp); err != nil {
		return nil, err
	}
	f.Entitled = true
	f.Selected = true
	f.GrantedAt = stamp
	return &f, nil
}

func (s *Service) GrantPoints(ctx context.Context, actor identity.User, requestID, userID, idempotencyKey, description string, amount int) (PointEvent, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	description = strings.TrimSpace(description)
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		return PointEvent{}, false, platform.Validation(map[string][]string{"idempotency_key": {"Idempotency-Key 必填且不能超过 128 个字符"}})
	}
	if description == "" || len([]rune(description)) > 200 {
		return PointEvent{}, false, platform.Validation(map[string][]string{"description": {"长度必须为 1 到 200 个字符"}})
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PointEvent{}, false, err
	}
	defer tx.Rollback()
	var userExists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND status='active'`, userID).Scan(&userExists); err != nil {
		return PointEvent{}, false, err
	}
	if userExists == 0 {
		return PointEvent{}, false, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在")
	}
	id, replayed, err := awardPoints(ctx, tx, userID, "admin_grant", idempotencyKey, description, amount, now)
	if err != nil {
		return PointEvent{}, false, err
	}
	if replayed {
		var event PointEvent
		if err := tx.QueryRowContext(ctx, `SELECT id,amount,event_type,event_key,description,created_at FROM local_point_events WHERE id=?`, id).Scan(&event.ID, &event.Amount, &event.EventType, &event.EventKey, &event.Description, &event.CreatedAt); err != nil {
			return PointEvent{}, false, err
		}
		return event, true, nil
	}
	event := PointEvent{ID: id, Amount: amount, EventType: "admin_grant", EventKey: idempotencyKey, Description: description, CreatedAt: now.Format(time.RFC3339Nano)}
	if err := insertAudit(ctx, tx, actor.ID, "local_points_grant", "user", userID, map[string]any{}, event, description, requestID, now); err != nil {
		return PointEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return PointEvent{}, false, err
	}
	return event, false, nil
}
