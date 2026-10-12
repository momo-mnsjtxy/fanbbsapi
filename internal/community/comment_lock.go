package community

import (
	"database/sql"
	"net/http"
	"strings"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func (s *Service) commentLockHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Locked *bool  `json:"locked"`
		Reason string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Locked == nil || len([]rune(input.Reason)) < 3 || len([]rune(input.Reason)) > 500 {
		platform.WriteError(w, r, platform.Validation(map[string][]string{"lock": {"请指定 locked 并填写 3 到 500 字的理由"}}))
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
	var status, owner string
	var current, locked int
	err = tx.QueryRowContext(r.Context(), platform.ForNoKeyUpdate(s.db, `SELECT status,author_id,version,comments_locked FROM posts WHERE id=?`), postID(r)).Scan(&status, &owner, &current, &locked)
	if err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "post_not_found", "帖子不存在"))
		return
	} else if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if status != "published" {
		platform.WriteError(w, r, platform.Problem(http.StatusConflict, "post_not_published", "只有已发布帖子可以调整评论开关"))
		return
	}
	if expected != current {
		platform.WriteError(w, r, platform.Problem(http.StatusConflict, "version_conflict", "帖子已更新，请重新查看后操作"))
		return
	}
	next := 0
	if *input.Locked {
		next = 1
	}
	changed := next != locked
	if changed {
		if _, err := tx.ExecContext(r.Context(), `UPDATE posts SET comments_locked=?,version=version+1 WHERE id=?`, next, postID(r)); err != nil {
			platform.WriteError(w, r, err)
			return
		}
		now := s.now().UTC()
		if err := insertAudit(r.Context(), tx, actor.ID, "post_comments_lock", "post", postID(r), map[string]any{"comments_locked": locked != 0, "version": current}, map[string]any{"comments_locked": next != 0, "version": current + 1}, input.Reason, platform.RequestID(r.Context()), now); err != nil {
			platform.WriteError(w, r, err)
			return
		}
		decision := "comments_reopened"
		if next != 0 {
			decision = "comments_locked"
		}
		if err := addNotification(r.Context(), tx, owner, actor.ID, "moderation", "post", postID(r), map[string]string{"decision": decision, "reason": input.Reason}, now); err != nil {
			platform.WriteError(w, r, err)
			return
		}
		current++
	}
	if err := tx.Commit(); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"post_id": postID(r), "comments_locked": next != 0, "version": current, "changed": changed})
}
