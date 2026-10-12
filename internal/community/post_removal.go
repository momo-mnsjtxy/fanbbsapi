package community

import (
	"database/sql"
	"net/http"
	"strings"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

// Published-content removal mirrors the existing report outcome, but can be
// initiated directly from a reviewed content snapshot without a member report.
func (s *Service) removeReviewedPostHTTP(w http.ResponseWriter, r *http.Request) {
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
	var owner, status, kind, original string
	var version int
	err = tx.QueryRowContext(r.Context(), platform.ForNoKeyUpdate(s.db, `SELECT author_id,status,kind,COALESCE(repost_of,''),version FROM posts WHERE id=?`), postID(r)).Scan(&owner, &status, &kind, &original, &version)
	if err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "post_not_found", "帖子不存在"))
		return
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if version != expected {
		platform.WriteError(w, r, platform.Problem(http.StatusConflict, "version_conflict", "帖子已更新，请重新查看后处理"))
		return
	}
	if status != "published" {
		platform.WriteError(w, r, platform.Problem(http.StatusConflict, "post_not_published", "只能直接移除已发布帖子，待审稿请使用审核决定"))
		return
	}
	if kind == "repost" && original != "" {
		result, err := tx.ExecContext(r.Context(), `DELETE FROM reposts WHERE repost_post_id=?`, postID(r))
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		changed, err := result.RowsAffected()
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		if changed > 0 {
			if _, err := tx.ExecContext(r.Context(), `UPDATE posts SET repost_count=CASE WHEN repost_count>0 THEN repost_count-1 ELSE 0 END WHERE id=?`, original); err != nil {
				platform.WriteError(w, r, err)
				return
			}
		}
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE posts SET status='deleted',version=version+1 WHERE id=?`, postID(r)); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	now := s.now().UTC()
	if err := insertAudit(r.Context(), tx, actor.ID, "post_moderation_remove", "post", postID(r), map[string]any{"status": status, "version": version}, map[string]any{"status": "deleted", "version": version + 1}, input.Reason, platform.RequestID(r.Context()), now); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if err := addNotification(r.Context(), tx, owner, actor.ID, "moderation", "post", postID(r), map[string]string{"decision": "remove_content", "reason": input.Reason}, now); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"post_id": postID(r), "status": "deleted", "version": version + 1})
}
