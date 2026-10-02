package community

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

type PostModerationResult struct {
	PostID      string `json:"post_id"`
	State       string `json:"state"`
	Pinned      bool   `json:"pinned"`
	Recommended bool   `json:"recommended"`
	Reason      string `json:"reason,omitempty"`
	DecidedBy   string `json:"decided_by,omitempty"`
	DecidedAt   string `json:"decided_at,omitempty"`
}

func (s *Service) ModeratePost(ctx context.Context, actor identity.User, requestID, postID, decision, reason string) (PostModerationResult, error) {
	decision = strings.TrimSpace(decision)
	reason = strings.TrimSpace(reason)
	fields := map[string][]string{}
	if decision != "published" && decision != "rejected" {
		fields["decision"] = []string{"decision 必须是 published 或 rejected"}
	}
	if len([]rune(reason)) < 3 || len([]rune(reason)) > 500 {
		fields["reason"] = []string{"理由长度必须为 3 到 500 个字符"}
	}
	if len(fields) > 0 {
		return PostModerationResult{}, platform.Validation(fields)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PostModerationResult{}, fmt.Errorf("begin post moderation: %w", err)
	}
	defer tx.Rollback()
	var authorID, state string
	var pinned, recommended int
	err = tx.QueryRowContext(ctx, `
		SELECT p.author_id, m.state, m.is_pinned, m.is_recommended
		FROM posts p JOIN post_moderation m ON m.post_id = p.id WHERE p.id = ?`, postID).Scan(&authorID, &state, &pinned, &recommended)
	if err == sql.ErrNoRows {
		return PostModerationResult{}, platform.Problem(http.StatusNotFound, "pending_post_not_found", "待审核文章不存在")
	}
	if err != nil {
		return PostModerationResult{}, fmt.Errorf("load pending post: %w", err)
	}
	if state != "pending" {
		return PostModerationResult{}, platform.Problem(http.StatusConflict, "moderation_already_decided", "文章审核状态已经变更")
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	databaseStatus := "draft"
	if decision == "published" {
		databaseStatus = "published"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET status = ?, published_at = CASE WHEN ? = 'published' THEN ? ELSE published_at END WHERE id = ?`, databaseStatus, decision, stamp, postID); err != nil {
		return PostModerationResult{}, fmt.Errorf("apply post moderation: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE post_moderation SET state = ?, reason = ?, decided_by = ?, decided_at = ?, updated_at = ?
		WHERE post_id = ? AND state = 'pending'`, decision, reason, actor.ID, stamp, stamp, postID)
	if err != nil {
		return PostModerationResult{}, fmt.Errorf("record post moderation: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return PostModerationResult{}, platform.Problem(http.StatusConflict, "moderation_already_decided", "文章审核状态已经变更")
	}
	if err := insertAudit(ctx, tx, actor.ID, "post_moderation_decision", "post", postID,
		map[string]any{"state": state}, map[string]any{"state": decision}, reason, requestID, now); err != nil {
		return PostModerationResult{}, err
	}
	if err := addNotification(ctx, tx, authorID, actor.ID, "moderation", "post", postID, map[string]string{"decision": decision}, now); err != nil {
		return PostModerationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PostModerationResult{}, fmt.Errorf("commit post moderation: %w", err)
	}
	return PostModerationResult{PostID: postID, State: decision, Pinned: pinned != 0, Recommended: recommended != 0, Reason: reason, DecidedBy: actor.ID, DecidedAt: stamp}, nil
}

func (s *Service) UpdatePostControls(ctx context.Context, actor identity.User, requestID, postID string, pin, recommend *bool, reason string) (PostModerationResult, error) {
	reason = strings.TrimSpace(reason)
	if pin == nil && recommend == nil {
		return PostModerationResult{}, platform.Validation(map[string][]string{"controls": {"至少提供 pinned 或 recommended"}})
	}
	if len([]rune(reason)) < 3 || len([]rune(reason)) > 500 {
		return PostModerationResult{}, platform.Validation(map[string][]string{"reason": {"理由长度必须为 3 到 500 个字符"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PostModerationResult{}, fmt.Errorf("begin post controls: %w", err)
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM posts WHERE id = ?`, postID).Scan(&status); err == sql.ErrNoRows {
		return PostModerationResult{}, platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	} else if err != nil {
		return PostModerationResult{}, fmt.Errorf("load controlled post: %w", err)
	}
	var state string
	var oldPinned, oldRecommended int
	err = tx.QueryRowContext(ctx, `SELECT state, is_pinned, is_recommended FROM post_moderation WHERE post_id = ?`, postID).Scan(&state, &oldPinned, &oldRecommended)
	if err == sql.ErrNoRows {
		state = status
	} else if err != nil {
		return PostModerationResult{}, fmt.Errorf("load post controls: %w", err)
	}
	if state != "published" || status != "published" {
		return PostModerationResult{}, platform.Problem(http.StatusConflict, "post_not_published", "只有已发布文章可以设置推荐或置顶")
	}
	newPinned, newRecommended := oldPinned, oldRecommended
	if pin != nil {
		if *pin {
			newPinned = 1
		} else {
			newPinned = 0
		}
	}
	if recommend != nil {
		if *recommend {
			newRecommended = 1
		} else {
			newRecommended = 0
		}
	}
	now := s.now().UTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO post_moderation(post_id, state, is_pinned, is_recommended, updated_at)
		VALUES (?, 'published', ?, ?, ?)
		ON CONFLICT(post_id) DO UPDATE SET is_pinned = excluded.is_pinned, is_recommended = excluded.is_recommended, updated_at = excluded.updated_at`,
		postID, newPinned, newRecommended, now.Format(time.RFC3339Nano))
	if err != nil {
		return PostModerationResult{}, fmt.Errorf("update post controls: %w", err)
	}
	if err := insertAudit(ctx, tx, actor.ID, "post_feed_controls_update", "post", postID,
		map[string]any{"pinned": oldPinned != 0, "recommended": oldRecommended != 0},
		map[string]any{"pinned": newPinned != 0, "recommended": newRecommended != 0}, reason, requestID, now); err != nil {
		return PostModerationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PostModerationResult{}, fmt.Errorf("commit post controls: %w", err)
	}
	return PostModerationResult{PostID: postID, State: "published", Pinned: newPinned != 0, Recommended: newRecommended != 0}, nil
}

func (s *Service) moderatePostHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	item, err := s.ModeratePost(r.Context(), actor, platform.RequestID(r.Context()), postID(r), input.Decision, input.Reason)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) updatePostControlsHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Pinned      *bool  `json:"pinned"`
		Recommended *bool  `json:"recommended"`
		Reason      string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	item, err := s.UpdatePostControls(r.Context(), actor, platform.RequestID(r.Context()), postID(r), input.Pinned, input.Recommended, input.Reason)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
