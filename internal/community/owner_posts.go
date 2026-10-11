package community

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

// OwnerPostDetail keeps review decisions in the private author workspace;
// public post reads never acquire a bypass for drafts or rejected content.
type OwnerPostDetail struct {
	Post       Post                  `json:"post"`
	Moderation *PostModerationResult `json:"moderation,omitempty"`
}

func (s *Service) OwnerPost(ctx context.Context, ownerID, postID string) (OwnerPostDetail, error) {
	// Read body and decision from one snapshot, so a concurrent moderator
	// cannot produce a pending body paired with a later decision in one reply.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return OwnerPostDetail{}, fmt.Errorf("begin owner post snapshot: %w", err)
	}
	defer tx.Rollback()
	post, err := scanPost(tx.QueryRowContext(ctx, postSelect+` WHERE p.id = ? AND p.author_id = ? AND p.status <> 'deleted'`, ownerID, ownerID, ownerID, postID, ownerID))
	if err == sql.ErrNoRows {
		return OwnerPostDetail{}, platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	}
	if err != nil {
		return OwnerPostDetail{}, fmt.Errorf("load owner post snapshot: %w", err)
	}
	result := OwnerPostDetail{Post: post}
	var moderation PostModerationResult
	var pinned, recommended int
	err = tx.QueryRowContext(ctx, `SELECT post_id,state,is_pinned,is_recommended,reason,COALESCE(decided_by,''),COALESCE(decided_at,'') FROM post_moderation WHERE post_id=?`, postID).Scan(&moderation.PostID, &moderation.State, &pinned, &recommended, &moderation.Reason, &moderation.DecidedBy, &moderation.DecidedAt)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return OwnerPostDetail{}, fmt.Errorf("load owner review status: %w", err)
	}
	moderation.Pinned = pinned != 0
	moderation.Recommended = recommended != 0
	result.Moderation = &moderation
	return result, nil
}

func (s *Service) OwnerPosts(ctx context.Context, ownerID, status, rawCursor string, limit int) ([]Post, string, error) {
	if limit < 1 || limit > 50 {
		return nil, "", platform.Validation(map[string][]string{"limit": {"limit 必须是 1 到 50 的整数"}})
	}
	if status != "" && status != "published" && status != "pending" && status != "rejected" && status != "draft" {
		return nil, "", platform.Validation(map[string][]string{"status": {"文章状态无效"}})
	}
	scope := "owner-posts:" + ownerID + ":" + status
	cursor, err := decodeSeekCursor(rawCursor, scope)
	if err != nil {
		return nil, "", err
	}
	if cursor.Sequence != 0 || (rawCursor != "" && (cursor.ID == "" || cursor.Timestamp == "")) {
		return nil, "", platform.Validation(map[string][]string{"cursor": {"游标无效"}})
	}
	if rawCursor != "" {
		if _, err := time.Parse(time.RFC3339Nano, cursor.Timestamp); err != nil {
			return nil, "", platform.Validation(map[string][]string{"cursor": {"游标时间无效"}})
		}
	}
	where := `p.author_id=? AND p.status <> 'deleted'`
	args := []any{ownerID, ownerID, ownerID, ownerID}
	if status != "" {
		where += ` AND COALESCE((SELECT state FROM post_moderation WHERE post_id=p.id),p.status)=?`
		args = append(args, status)
	}
	if rawCursor != "" {
		where += ` AND (p.created_at < ? OR (p.created_at = ? AND p.id < ?))`
		args = append(args, cursor.Timestamp, cursor.Timestamp, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, postSelect+` WHERE `+where+` ORDER BY p.created_at DESC,p.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list owner posts: %w", err)
	}
	defer rows.Close()
	items := make([]Post, 0, limit+1)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, post)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeSeekCursor(seekCursor{Scope: scope, Timestamp: last.CreatedAt, ID: last.ID})
	}
	return items, next, nil
}

func (s *Service) ownerPostsHTTP(w http.ResponseWriter, r *http.Request) {
	limit, err := validateLimit(r.URL.Query().Get("limit"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	items, next, err := s.OwnerPosts(r.Context(), current.ID, r.URL.Query().Get("status"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) ownerPostHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	item, err := s.OwnerPost(r.Context(), current.ID, postID(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(item.Post.Version)))
	platform.WriteData(w, r, http.StatusOK, item)
}
