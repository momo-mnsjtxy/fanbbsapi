package community

import (
	"database/sql"
	"errors"
	"net/http"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// My reports expose only the signed-in reporter's own submissions and outcomes.
// Target content is deliberately omitted: it may since have become private.
func (s *Service) myReportsHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "open" && status != "actioned" && status != "dismissed" {
		platform.WriteError(w, r, platform.Validation(map[string][]string{"status": {"举报状态无效"}}))
		return
	}
	user, _ := identity.UserFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,reporter_id,target_type,target_id,reason,status,COALESCE(decision,''),COALESCE(decision_reason,''),COALESCE(decided_at,''),created_at FROM reports WHERE reporter_id=? AND (?='' OR status=?) ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, user.ID, status, status, limit+1, offset)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	defer rows.Close()
	items := []Report{}
	for rows.Next() {
		var item Report
		if err := rows.Scan(&item.ID, &item.ReporterID, &item.TargetType, &item.TargetID, &item.Reason, &item.Status, &item.Decision, &item.DecisionReason, &item.DecidedAt, &item.CreatedAt); err != nil {
			platform.WriteError(w, r, err)
			return
		}
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

type ReportTargetContext struct {
	Parent      *ReportTargetContext `json:"parent,omitempty"`
	ID          string               `json:"id"`
	AuthorID    string               `json:"author_id,omitempty"`
	PostID      string               `json:"post_id,omitempty"`
	ParentID    string               `json:"parent_id,omitempty"`
	Body        string               `json:"body,omitempty"`
	Status      string               `json:"status"`
	Version     int                  `json:"version,omitempty"`
	DisplayName string               `json:"display_name,omitempty"`
	Handle      string               `json:"handle,omitempty"`
	Post        *Post                `json:"post,omitempty"`
}

// Role middleware is mandatory. This review-only view never changes public
// visibility and does not expose account email, credentials, or session data.
func (s *Service) reportContextHTTP(w http.ResponseWriter, r *http.Request) {
	item, err := s.report(r.Context(), chi.URLParam(r, "reportID"))
	if errors.Is(err, sql.ErrNoRows) {
		platform.WriteError(w, r, platform.Problem(http.StatusNotFound, "report_not_found", "举报不存在"))
		return
	} else if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	target := ReportTargetContext{ID: item.TargetID}
	switch item.TargetType {
	case "comment":
		err = s.db.QueryRowContext(r.Context(), `SELECT author_id,post_id,COALESCE(parent_id,''),body,status,version FROM comments WHERE id=?`, item.TargetID).Scan(&target.AuthorID, &target.PostID, &target.ParentID, &target.Body, &target.Status, &target.Version)
	case "post":
		target.PostID = item.TargetID
		err = s.db.QueryRowContext(r.Context(), `SELECT author_id,body,status,version FROM posts WHERE id=?`, item.TargetID).Scan(&target.AuthorID, &target.Body, &target.Status, &target.Version)
	case "user":
		err = s.db.QueryRowContext(r.Context(), `SELECT display_name,handle,bio,status FROM users WHERE id=?`, item.TargetID).Scan(&target.DisplayName, &target.Handle, &target.Body, &target.Status)
	}
	if err == sql.ErrNoRows {
		target.Status = "unavailable"
	} else if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if target.ParentID != "" {
		parent := ReportTargetContext{ID: target.ParentID}
		err = s.db.QueryRowContext(r.Context(), `SELECT author_id,body,status,version FROM comments WHERE id=? AND post_id=?`, target.ParentID, target.PostID).Scan(&parent.AuthorID, &parent.Body, &parent.Status, &parent.Version)
		if err == nil {
			target.Parent = &parent
		} else if err != sql.ErrNoRows {
			platform.WriteError(w, r, err)
			return
		}
	}
	if target.PostID != "" {
		var author, status string
		err = s.db.QueryRowContext(r.Context(), `SELECT author_id,status FROM posts WHERE id=?`, target.PostID).Scan(&author, &status)
		if err != nil && err != sql.ErrNoRows {
			platform.WriteError(w, r, err)
			return
		}
		if err == nil && status != "deleted" {
			detail, loadErr := s.OwnerPost(r.Context(), author, target.PostID)
			if loadErr != nil {
				platform.WriteError(w, r, loadErr)
				return
			}
			target.Post = &detail.Post
			if item.TargetType == "post" {
				target.Version = detail.Post.Version
				target.Status = detail.Post.Status
				target.Body = detail.Post.Body
				if target.Body == "" {
					target.Body = detail.Post.Content
				}
			}
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	platform.WriteData(w, r, http.StatusOK, map[string]any{"report": item, "target": target})
}
