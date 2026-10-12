package community

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

// Administrator-only account context deliberately excludes session identifiers,
// IP addresses, hashes, access/refresh tokens and recovery material.
func (s *Service) adminUserDetailHTTP(w http.ResponseWriter, r *http.Request) {
	var item AdminUser
	var sessions, posts, reports int
	err := s.db.QueryRowContext(r.Context(), `SELECT u.id,u.handle,u.email,u.display_name,u.role,u.status,u.created_at,COALESCE(u.updated_at,''),
 (SELECT COUNT(*) FROM sessions se WHERE se.user_id=u.id AND se.revoked_at IS NULL AND se.refresh_expires_at>?),
 (SELECT COUNT(*) FROM posts p WHERE p.author_id=u.id AND p.status<>'deleted'),
 (SELECT COUNT(*) FROM reports rp WHERE rp.target_type='user' AND rp.target_id=u.id AND rp.status='open')
 FROM users u WHERE u.id=?`, s.now().UTC().Format(time.RFC3339Nano), userID(r)).Scan(&item.ID, &item.Handle, &item.Email, &item.DisplayName, &item.Role, &item.Status, &item.CreatedAt, &item.UpdatedAt, &sessions, &posts, &reports)
	if err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在"))
		return
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	platform.WriteData(w, r, http.StatusOK, map[string]any{"user": item, "active_sessions": sessions, "post_count": posts, "open_user_reports": reports})
}

func (s *Service) userModerationHistoryHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM users WHERE id=?`, userID(r)).Scan(&exists); err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在"))
		return
	} else if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,actor_id,action,reason,before_value,after_value,created_at FROM audit_events WHERE target_type='user' AND target_id=? AND action IN ('user_status_update','user_sessions_revoke','report_decision') ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, userID(r), limit+1, offset)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	defer rows.Close()
	items := []ReviewHistoryEntry{}
	for rows.Next() {
		var item ReviewHistoryEntry
		var before, after string
		if err := rows.Scan(&item.ID, &item.ActorID, &item.Action, &item.Reason, &before, &after, &item.CreatedAt); err != nil {
			platform.WriteError(w, r, err)
			return
		}
		item.Before = json.RawMessage(before)
		item.After = json.RawMessage(after)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	platform.WriteList(w, r, items, next)
}

func (s *Service) adminRevokeUserSessionsHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if len([]rune(input.Reason)) < 3 || len([]rune(input.Reason)) > 500 {
		platform.WriteError(w, r, platform.Validation(map[string][]string{"reason": {"请填写 3 到 500 字的撤销理由"}}))
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	if actor.ID == userID(r) {
		platform.WriteError(w, r, platform.Problem(http.StatusConflict, "use_own_session_controls", "请在个人账号安全页面管理自己的设备会话"))
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(r.Context(), platform.ForNoKeyUpdate(s.db, `SELECT 1 FROM users WHERE id=?`), userID(r)).Scan(&exists); err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在"))
		return
	} else if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	now := s.now().UTC()
	result, err := tx.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`, now.Format(time.RFC3339Nano), userID(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if count > 0 {
		if err := insertAudit(r.Context(), tx, actor.ID, "user_sessions_revoke", "user", userID(r), map[string]any{"unrevoked_sessions": count}, map[string]any{"revoked_sessions": count}, input.Reason, platform.RequestID(r.Context()), now); err != nil {
			platform.WriteError(w, r, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"user_id": userID(r), "revoked": count})
}
