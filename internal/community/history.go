package community

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

type PostRevision struct {
	PostID     string   `json:"post_id"`
	Version    int      `json:"version"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Body       string   `json:"body"`
	CategoryID string   `json:"category_id,omitempty"`
	TagIDs     []string `json:"tag_ids"`
	Visibility string   `json:"visibility"`
	CreatedAt  string   `json:"created_at"`
}

type OwnerActivity struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func (s *Service) PostRevisions(ctx context.Context, postID, ownerID string, offset, limit int) ([]PostRevision, string, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM posts WHERE id = ? AND author_id = ?`, postID, ownerID).Scan(&exists); err == sql.ErrNoRows {
		return nil, "", platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	} else if err != nil {
		return nil, "", fmt.Errorf("check revision owner: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT post_id, version, title, summary, body, COALESCE(category_id, ''), tag_ids, visibility, created_at
		FROM post_revisions WHERE post_id = ? ORDER BY version DESC LIMIT ? OFFSET ?`, postID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list post revisions: %w", err)
	}
	defer rows.Close()
	items := []PostRevision{}
	for rows.Next() {
		var item PostRevision
		var tagJSON string
		if err := rows.Scan(&item.PostID, &item.Version, &item.Title, &item.Summary, &item.Body, &item.CategoryID, &tagJSON, &item.Visibility, &item.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("scan post revision: %w", err)
		}
		if err := json.Unmarshal([]byte(tagJSON), &item.TagIDs); err != nil {
			return nil, "", fmt.Errorf("decode post revision tags: %w", err)
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

func (s *Service) OwnerActivity(ctx context.Context, ownerID string, offset, limit int) ([]OwnerActivity, string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT activity_type, activity_id, action, activity_status, activity_at FROM (
			SELECT 'post' activity_type, p.id activity_id, 'created' action,
			       COALESCE((SELECT state FROM post_moderation WHERE post_id = p.id), p.status) activity_status, p.created_at activity_at
			FROM posts p WHERE p.author_id = ?
			UNION ALL
			SELECT 'comment', c.id, 'created', c.status, c.created_at FROM comments c WHERE c.author_id = ?
			UNION ALL
			SELECT 'report', r.id, 'submitted', r.status, r.created_at FROM reports r WHERE r.reporter_id = ?
		) own_activity ORDER BY activity_at DESC, activity_type, activity_id DESC LIMIT ? OFFSET ?`, ownerID, ownerID, ownerID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list owner activity: %w", err)
	}
	defer rows.Close()
	items := []OwnerActivity{}
	for rows.Next() {
		var item OwnerActivity
		if err := rows.Scan(&item.Type, &item.ID, &item.Action, &item.Status, &item.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("scan owner activity: %w", err)
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

func (s *Service) postRevisionsHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	items, next, err := s.PostRevisions(r.Context(), postID(r), current.ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) ownerActivityHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	items, next, err := s.OwnerActivity(r.Context(), current.ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}
