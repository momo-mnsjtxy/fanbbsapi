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

// TransitionSubmission never publishes a reviewed draft. Authors explicitly
// submit for moderation or withdraw a pending submission before editing it.
func (s *Service) TransitionSubmission(ctx context.Context, owner identity.User, requestID, id, action, reason string, expected int) (Post, error) {
	action = strings.TrimSpace(action)
	reason = strings.TrimSpace(reason)
	if action != "submit" && action != "withdraw" {
		return Post{}, platform.Validation(map[string][]string{"action": {"必须是 submit 或 withdraw"}})
	}
	if len([]rune(reason)) < 3 || len([]rune(reason)) > 500 {
		return Post{}, platform.Validation(map[string][]string{"reason": {"说明长度必须为 3 到 500 个字符"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	var author, status, body, title string
	var version int
	err = tx.QueryRowContext(ctx, platform.ForNoKeyUpdate(s.db, `SELECT author_id,status,version,title,body FROM posts WHERE id=? AND status<>'deleted'`), id).Scan(&author, &status, &version, &title, &body)
	if err == sql.ErrNoRows || err == nil && author != owner.ID {
		return Post{}, platform.Problem(http.StatusNotFound, "post_not_found", "稿件不存在")
	}
	if err != nil {
		return Post{}, err
	}
	if version != expected {
		return Post{}, platform.Problem(http.StatusConflict, "version_conflict", "稿件已变更，请刷新后重试")
	}
	if status != "draft" {
		return Post{}, platform.Problem(http.StatusConflict, "submission_not_draft", "仅未发布稿件可以提交或撤回审核")
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT state FROM post_moderation WHERE post_id=?),'draft')`, id).Scan(&state); err != nil {
		return Post{}, err
	}
	before := state
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	if action == "submit" {
		if state != "draft" && state != "rejected" {
			return Post{}, platform.Problem(http.StatusConflict, "submission_already_pending", "稿件已在审核中")
		}
		if strings.TrimSpace(body) == "" || len([]rune(title)) < 3 {
			return Post{}, platform.Validation(map[string][]string{"content": {"请完善标题与正文后再提交"}})
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO post_moderation(post_id,state,reason,updated_at) VALUES(?,'pending',?,?)
   ON CONFLICT(post_id) DO UPDATE SET state='pending',reason=excluded.reason,decided_by=NULL,decided_at=NULL,is_pinned=0,is_recommended=0,updated_at=excluded.updated_at`, id, reason, stamp)
		state = "pending"
	} else {
		if state != "pending" {
			return Post{}, platform.Problem(http.StatusConflict, "submission_not_pending", "只有待审核稿件可以撤回")
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM post_moderation WHERE post_id=? AND state='pending'`, id)
		state = "draft"
	}
	if err != nil {
		return Post{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET version=version+1 WHERE id=? AND version=?`, id, expected); err != nil {
		return Post{}, err
	}
	if err := insertAudit(ctx, tx, owner.ID, "submission_"+action, "post", id, map[string]any{"state": before, "version": version}, map[string]any{"state": state, "version": version + 1}, reason, requestID, now); err != nil {
		return Post{}, err
	}
	if err := tx.Commit(); err != nil {
		return Post{}, err
	}
	return s.postForOwner(ctx, id, owner.ID)
}

func (s *Service) transitionSubmissionHTTP(w http.ResponseWriter, r *http.Request) {
	expected, err := ifMatchVersion(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var input struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	owner, _ := identity.UserFromContext(r.Context())
	post, err := s.TransitionSubmission(r.Context(), owner, platform.RequestID(r.Context()), postID(r), input.Action, input.Reason, expected)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, post.Version))
	platform.WriteData(w, r, http.StatusOK, post)
}
