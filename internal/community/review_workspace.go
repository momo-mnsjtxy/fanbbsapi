package community

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"fanbbs.local/backend/internal/platform"
)

type ReviewHistoryEntry struct {
	ID        string          `json:"id"`
	ActorID   string          `json:"actor_id"`
	Action    string          `json:"action"`
	Reason    string          `json:"reason"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	CreatedAt string          `json:"created_at"`
}

// These handlers are registered exclusively under moderator/admin middleware.
// Author-only and public endpoints keep their original privacy predicates.
func (s *Service) adminSubmissionHTTP(w http.ResponseWriter, r *http.Request) {
	var author string
	err := s.db.QueryRowContext(r.Context(), `SELECT author_id FROM posts WHERE id=? AND status<>'deleted'`, postID(r)).Scan(&author)
	if err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "post_not_found", "稿件不存在"))
		return
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.OwnerPost(r.Context(), author, postID(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(item.Post.Version)))
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) adminSubmissionMediaHTTP(w http.ResponseWriter, r *http.Request) {
	var owner string
	err := s.db.QueryRowContext(r.Context(), `SELECT m.owner_id FROM media_assets m JOIN post_media link ON link.asset_id=m.id JOIN posts p ON p.id=link.post_id
  WHERE m.id=? AND p.id=? AND p.status<>'deleted' AND m.status='ready' AND m.purpose='post'`, mediaID(r), postID(r)).Scan(&owner)
	if err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "media_not_found", "稿件附件不存在"))
		return
	}
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	asset, _, content, _, err := s.media(r.Context(), mediaID(r), owner)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	defer content.Close()
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", asset.MIMEType)
	w.Header().Set("Content-Length", strconv.FormatInt(asset.SizeBytes, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, content)
}

func (s *Service) submissionHistoryHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM posts WHERE id=?`, postID(r)).Scan(&exists); err == sql.ErrNoRows {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "post_not_found", "稿件不存在"))
		return
	} else if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,actor_id,action,reason,before_value,after_value,created_at FROM audit_events WHERE target_type='post' AND target_id=? AND action IN ('submission_submit','submission_withdraw','post_moderation_decision') ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, postID(r), limit+1, offset)
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
