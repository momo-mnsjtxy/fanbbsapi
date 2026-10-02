// Moderation turns member reports into reasoned, role-checked decisions. Every
// decision and its immutable audit event commit in the same transaction.
package community

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

type Report struct {
	ID             string `json:"id"`
	ReporterID     string `json:"reporter_id"`
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
	Reason         string `json:"reason"`
	Status         string `json:"status"`
	Decision       string `json:"decision,omitempty"`
	DecisionReason string `json:"decision_reason,omitempty"`
	DecidedBy      string `json:"decided_by,omitempty"`
	DecidedAt      string `json:"decided_at,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type AuditEvent struct {
	ID          string          `json:"id"`
	ActorID     string          `json:"actor_id"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	BeforeValue json.RawMessage `json:"before"`
	AfterValue  json.RawMessage `json:"after"`
	Reason      string          `json:"reason"`
	RequestID   string          `json:"request_id"`
	CreatedAt   string          `json:"created_at"`
}

func (s *Service) Report(ctx context.Context, reporterID, idempotencyKey, targetType, targetID, reason string) (Report, bool, error) {
	targetType = strings.TrimSpace(targetType)
	targetID = strings.TrimSpace(targetID)
	reason = strings.TrimSpace(reason)
	fields := map[string][]string{}
	if targetType != "post" && targetType != "comment" && targetType != "user" {
		fields["target_type"] = []string{"举报对象必须是 post、comment 或 user"}
	}
	if targetID == "" {
		fields["target_id"] = []string{"举报对象不能为空"}
	}
	if len([]rune(reason)) < 5 || len([]rune(reason)) > 1000 {
		fields["reason"] = []string{"举报理由长度必须为 5 到 1000 个字符"}
	}
	if len(idempotencyKey) > 128 {
		fields["idempotency_key"] = []string{"Idempotency-Key 不能超过 128 个字符"}
	}
	if len(fields) > 0 {
		return Report{}, false, platform.Validation(fields)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Report{}, false, fmt.Errorf("begin report: %w", err)
	}
	defer tx.Rollback()
	if idempotencyKey != "" {
		var existingID string
		err := tx.QueryRowContext(ctx, `SELECT resource_id FROM idempotency_keys WHERE user_id = ? AND scope = 'create_report' AND key = ?`, reporterID, idempotencyKey).Scan(&existingID)
		if err == nil {
			tx.Rollback()
			report, err := s.report(ctx, existingID)
			return report, true, err
		}
		if err != sql.ErrNoRows {
			return Report{}, false, fmt.Errorf("check report idempotency: %w", err)
		}
	}
	if err := validateReportTarget(ctx, tx, targetType, targetID, reporterID); err != nil {
		return Report{}, false, err
	}
	reportID, err := platform.NewID("rpt")
	if err != nil {
		return Report{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO reports(id, reporter_id, target_type, target_id, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)`, reportID, reporterID, targetType, targetID, reason, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Report{}, false, platform.Problem(http.StatusConflict, "report_already_open", "你已提交过相同的未处理举报")
		}
		return Report{}, false, fmt.Errorf("create report: %w", err)
	}
	if idempotencyKey != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(user_id, scope, key, resource_id, created_at) VALUES (?, 'create_report', ?, ?, ?)`, reporterID, idempotencyKey, reportID, now); err != nil {
			return Report{}, false, fmt.Errorf("save report idempotency: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Report{}, false, fmt.Errorf("commit report: %w", err)
	}
	report, err := s.report(ctx, reportID)
	return report, false, err
}

func validateReportTarget(ctx context.Context, tx *sql.Tx, targetType, targetID, reporterID string) error {
	var exists int
	var err error
	switch targetType {
	case "post":
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM posts p WHERE p.id = ? AND `+visiblePostPredicate, append([]any{targetID}, visiblePostArgs(reporterID)...)...).Scan(&exists)
	case "comment":
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM comments c JOIN posts p ON p.id = c.post_id WHERE c.id = ? AND c.status = 'published' AND `+visiblePostPredicate, append([]any{targetID}, visiblePostArgs(reporterID)...)...).Scan(&exists)
	case "user":
		if targetID == reporterID {
			return platform.Validation(map[string][]string{"target_id": {"不能举报自己"}})
		}
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ? AND status = 'active'`, targetID).Scan(&exists)
	}
	if err == sql.ErrNoRows {
		return platform.Problem(http.StatusNotFound, "report_target_not_found", "举报对象不存在")
	}
	if err != nil {
		return fmt.Errorf("check report target: %w", err)
	}
	return nil
}

func (s *Service) report(ctx context.Context, reportID string) (Report, error) {
	var item Report
	err := s.db.QueryRowContext(ctx, `
		SELECT id, reporter_id, target_type, target_id, reason, status, COALESCE(decision, ''),
		       COALESCE(decision_reason, ''), COALESCE(decided_by, ''), COALESCE(decided_at, ''), created_at
		FROM reports WHERE id = ?`, reportID).Scan(&item.ID, &item.ReporterID, &item.TargetType, &item.TargetID,
		&item.Reason, &item.Status, &item.Decision, &item.DecisionReason, &item.DecidedBy, &item.DecidedAt, &item.CreatedAt)
	if err != nil {
		return Report{}, fmt.Errorf("load report: %w", err)
	}
	return item, nil
}

func (s *Service) Reports(ctx context.Context, status string, offset, limit int) ([]Report, string, error) {
	if status == "" {
		status = "open"
	}
	if status != "open" && status != "dismissed" && status != "actioned" {
		return nil, "", platform.Validation(map[string][]string{"status": {"status 必须是 open、dismissed 或 actioned"}})
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, reporter_id, target_type, target_id, reason, status, COALESCE(decision, ''),
		       COALESCE(decision_reason, ''), COALESCE(decided_by, ''), COALESCE(decided_at, ''), created_at
		FROM reports WHERE status = ? ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?`, status, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()
	items := make([]Report, 0, limit+1)
	for rows.Next() {
		var item Report
		if err := rows.Scan(&item.ID, &item.ReporterID, &item.TargetType, &item.TargetID, &item.Reason, &item.Status,
			&item.Decision, &item.DecisionReason, &item.DecidedBy, &item.DecidedAt, &item.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("scan report: %w", err)
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

func (s *Service) DecideReport(ctx context.Context, actor identity.User, requestID, reportID, decision, reason string) (Report, error) {
	decision = strings.TrimSpace(decision)
	reason = strings.TrimSpace(reason)
	if decision != "dismiss" && decision != "remove_content" && decision != "suspend_user" {
		return Report{}, platform.Validation(map[string][]string{"decision": {"decision 必须是 dismiss、remove_content 或 suspend_user"}})
	}
	if len([]rune(reason)) < 3 || len([]rune(reason)) > 500 {
		return Report{}, platform.Validation(map[string][]string{"reason": {"处理理由长度必须为 3 到 500 个字符"}})
	}
	if decision == "suspend_user" && actor.Role != "admin" {
		return Report{}, platform.Problem(http.StatusForbidden, "admin_required", "停用用户需要管理员权限")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Report{}, fmt.Errorf("begin report decision: %w", err)
	}
	defer tx.Rollback()
	var item Report
	err = tx.QueryRowContext(ctx, `SELECT id, reporter_id, target_type, target_id, reason, status, created_at FROM reports WHERE id = ?`, reportID).Scan(
		&item.ID, &item.ReporterID, &item.TargetType, &item.TargetID, &item.Reason, &item.Status, &item.CreatedAt)
	if err == sql.ErrNoRows {
		return Report{}, platform.Problem(http.StatusNotFound, "report_not_found", "举报不存在")
	}
	if err != nil {
		return Report{}, fmt.Errorf("load report decision: %w", err)
	}
	if item.Status != "open" {
		return Report{}, platform.Problem(http.StatusConflict, "report_already_decided", "举报已经处理")
	}
	if decision == "remove_content" && item.TargetType != "post" && item.TargetType != "comment" {
		return Report{}, platform.Validation(map[string][]string{"decision": {"该举报对象不是可移除的内容"}})
	}
	if decision == "suspend_user" && item.TargetType != "user" {
		return Report{}, platform.Validation(map[string][]string{"decision": {"该举报对象不是用户"}})
	}
	before, _ := json.Marshal(map[string]string{"report_status": item.Status, "target_status": "active"})
	targetAuthor := ""
	if decision == "remove_content" {
		if item.TargetType == "comment" {
			var postID, commentStatus string
			if err := tx.QueryRowContext(ctx, `SELECT author_id, post_id, status FROM comments WHERE id = ?`, item.TargetID).Scan(&targetAuthor, &postID, &commentStatus); err != nil {
				return Report{}, fmt.Errorf("load moderated comment: %w", err)
			}
			if commentStatus != "published" {
				return Report{}, platform.Problem(http.StatusConflict, "target_already_changed", "举报对象已经发生变化")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE comments SET status = 'deleted', version = version + 1 WHERE id = ? AND status = 'published'`, item.TargetID); err != nil {
				return Report{}, fmt.Errorf("remove reported comment: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE posts SET comment_count = CASE WHEN comment_count > 0 THEN comment_count - 1 ELSE 0 END WHERE id = ?`, postID); err != nil {
				return Report{}, fmt.Errorf("decrement moderated comment count: %w", err)
			}
		} else {
			var kind, repostOf, postStatus string
			if err := tx.QueryRowContext(ctx, `SELECT author_id, kind, COALESCE(repost_of, ''), status FROM posts WHERE id = ?`, item.TargetID).Scan(&targetAuthor, &kind, &repostOf, &postStatus); err != nil {
				return Report{}, fmt.Errorf("load moderated post: %w", err)
			}
			if postStatus != "published" {
				return Report{}, platform.Problem(http.StatusConflict, "target_already_changed", "举报对象已经发生变化")
			}
			if kind == "repost" && repostOf != "" {
				if _, err := tx.ExecContext(ctx, `DELETE FROM reposts WHERE repost_post_id = ?`, item.TargetID); err != nil {
					return Report{}, fmt.Errorf("remove moderated repost relation: %w", err)
				}
				if _, err := tx.ExecContext(ctx, `UPDATE posts SET repost_count = CASE WHEN repost_count > 0 THEN repost_count - 1 ELSE 0 END WHERE id = ?`, repostOf); err != nil {
					return Report{}, fmt.Errorf("decrement moderated repost count: %w", err)
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE posts SET status = 'deleted', version = version + 1 WHERE id = ? AND status = 'published'`, item.TargetID); err != nil {
				return Report{}, fmt.Errorf("remove reported post: %w", err)
			}
		}
	}
	if decision == "suspend_user" {
		targetAuthor = item.TargetID
		if _, err := tx.ExecContext(ctx, `UPDATE users SET status = 'suspended', updated_at = ? WHERE id = ?`, s.now().UTC().Format(time.RFC3339Nano), item.TargetID); err != nil {
			return Report{}, fmt.Errorf("suspend reported user: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, s.now().UTC().Format(time.RFC3339Nano), item.TargetID); err != nil {
			return Report{}, fmt.Errorf("revoke suspended user sessions: %w", err)
		}
	}
	status := "actioned"
	if decision == "dismiss" {
		status = "dismissed"
	}
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE reports SET status = ?, decision = ?, decision_reason = ?, decided_by = ?, decided_at = ? WHERE id = ?`, status, decision, reason, actor.ID, now.Format(time.RFC3339Nano), reportID); err != nil {
		return Report{}, fmt.Errorf("save report decision: %w", err)
	}
	auditID, err := platform.NewID("aud")
	if err != nil {
		return Report{}, err
	}
	after, _ := json.Marshal(map[string]string{"report_status": status, "decision": decision})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events(id, actor_id, action, target_type, target_id, before_value, after_value, reason, request_id, created_at)
		VALUES (?, ?, 'report_decision', ?, ?, ?, ?, ?, ?, ?)`, auditID, actor.ID, item.TargetType, item.TargetID, string(before), string(after), reason, requestID, now.Format(time.RFC3339Nano)); err != nil {
		return Report{}, fmt.Errorf("write report audit event: %w", err)
	}
	if err := addNotification(ctx, tx, item.ReporterID, actor.ID, "moderation", "report", reportID, map[string]string{"decision": decision}, now); err != nil {
		return Report{}, err
	}
	if targetAuthor != "" {
		if err := addNotification(ctx, tx, targetAuthor, actor.ID, "moderation", item.TargetType, item.TargetID, map[string]string{"decision": decision}, now); err != nil {
			return Report{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Report{}, fmt.Errorf("commit report decision: %w", err)
	}
	return s.report(ctx, reportID)
}

func (s *Service) AuditEvents(ctx context.Context, offset, limit int) ([]AuditEvent, string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, actor_id, action, target_type, target_id, before_value, after_value, reason, request_id, created_at FROM audit_events ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	items := make([]AuditEvent, 0, limit+1)
	for rows.Next() {
		var item AuditEvent
		var before, after string
		if err := rows.Scan(&item.ID, &item.ActorID, &item.Action, &item.TargetType, &item.TargetID, &before, &after, &item.Reason, &item.RequestID, &item.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("scan audit event: %w", err)
		}
		item.BeforeValue = json.RawMessage(before)
		item.AfterValue = json.RawMessage(after)
		items = append(items, item)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return items, next, rows.Err()
}

func (s *Service) submitReportHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Reason     string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	report, replayed, err := s.Report(r.Context(), current.ID, strings.TrimSpace(r.Header.Get("Idempotency-Key")), input.TargetType, input.TargetID, input.Reason)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	platform.WriteData(w, r, status, report)
}

func (s *Service) reportsHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	items, next, err := s.Reports(r.Context(), r.URL.Query().Get("status"), offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) decideReportHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	item, err := s.DecideReport(r.Context(), actor, platform.RequestID(r.Context()), chi.URLParam(r, "reportID"), input.Decision, input.Reason)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) auditEventsHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	items, next, err := s.AuditEvents(r.Context(), offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}
