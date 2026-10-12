package community

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func (s *Service) adminCommentHTTP(w http.ResponseWriter, r *http.Request) {
	item := ReportTargetContext{ID: commentID(r)}
	err := s.db.QueryRowContext(r.Context(), `SELECT author_id,post_id,COALESCE(parent_id,''),body,status,version FROM comments WHERE id=?`, item.ID).Scan(&item.AuthorID, &item.PostID, &item.ParentID, &item.Body, &item.Status, &item.Version)
	if err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "comment_not_found", "评论不存在"))
		return
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if item.ParentID != "" {
		parent := ReportTargetContext{ID: item.ParentID}
		err = s.db.QueryRowContext(r.Context(), `SELECT author_id,body,status,version FROM comments WHERE id=? AND post_id=?`, item.ParentID, item.PostID).Scan(&parent.AuthorID, &parent.Body, &parent.Status, &parent.Version)
		if err == nil {
			item.Parent = &parent
		} else if err != sql.ErrNoRows {
			platform.WriteError(w, r, err)
			return
		}
	}
	var owner, status string
	err = s.db.QueryRowContext(r.Context(), `SELECT author_id,status FROM posts WHERE id=?`, item.PostID).Scan(&owner, &status)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if status != "deleted" {
		post, err := s.OwnerPost(r.Context(), owner, item.PostID)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		item.Post = &post.Post
	}
	w.Header().Set("Cache-Control", "private, no-store")
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) removeReviewedCommentHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if len([]rune(input.Reason)) < 3 || len([]rune(input.Reason)) > 500 {
		platform.WriteError(w, r, platform.Validation(map[string][]string{"reason": {"请填写 3 到 500 字的处置理由"}}))
		return
	}
	expected, err := ifMatchVersion(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	defer tx.Rollback()
	var owner, post, status string
	var version int
	err = tx.QueryRowContext(r.Context(), platform.ForNoKeyUpdate(s.db, `SELECT author_id,post_id,status,version FROM comments WHERE id=?`), commentID(r)).Scan(&owner, &post, &status, &version)
	if err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "comment_not_found", "评论不存在"))
		return
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if version != expected {
		platform.WriteError(w, r, platform.Problem(http.StatusConflict, "version_conflict", "评论已更新，请重新查看后处理"))
		return
	}
	if status != "published" {
		platform.WriteError(w, r, platform.Problem(http.StatusConflict, "comment_already_removed", "评论已经被移除"))
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE comments SET status='deleted',version=version+1 WHERE id=?`, commentID(r)); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE posts SET comment_count=CASE WHEN comment_count>0 THEN comment_count-1 ELSE 0 END WHERE id=?`, post); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	now := s.now().UTC()
	if err := insertAudit(r.Context(), tx, actor.ID, "comment_moderation_remove", "comment", commentID(r), map[string]any{"status": status, "version": version}, map[string]any{"status": "deleted", "version": version + 1, "post_id": post}, input.Reason, platform.RequestID(r.Context()), now); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if err := addNotification(r.Context(), tx, owner, actor.ID, "moderation", "comment", commentID(r), map[string]string{"decision": "remove_content", "reason": input.Reason, "post_id": post}, now); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"comment_id": commentID(r), "status": "deleted", "version": version + 1})
}

func (s *Service) commentModerationHistoryHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM comments WHERE id=?`, commentID(r)).Scan(&exists); err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "comment_not_found", "评论不存在"))
		return
	} else if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,actor_id,action,reason,before_value,after_value,created_at FROM audit_events WHERE target_type='comment' AND target_id=? AND action IN ('comment_moderation_remove','report_decision') ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, commentID(r), limit+1, offset)
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
