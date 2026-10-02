// Social relations use explicit idempotent PUT/DELETE operations. Composite
// primary keys prevent double bookmarks and double follows under retries.
package community

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func (s *Service) Bookmark(ctx context.Context, postID, userID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin bookmark: %w", err)
	}
	defer tx.Rollback()
	if err := ensureVisiblePost(ctx, tx, postID, userID); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO bookmarks(post_id, user_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, postID, userID, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("create bookmark: %w", err)
	}
	changed, _ := result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit bookmark: %w", err)
	}
	return changed == 1, nil
}

func (s *Service) Unbookmark(ctx context.Context, postID, userID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM bookmarks WHERE post_id = ? AND user_id = ?`, postID, userID)
	if err != nil {
		return false, fmt.Errorf("delete bookmark: %w", err)
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

func (s *Service) Bookmarks(ctx context.Context, userID string, offset, limit int) ([]Post, string, error) {
	rows, err := s.db.QueryContext(ctx, postSelect+`
		JOIN bookmarks saved ON saved.post_id = p.id
		WHERE saved.user_id = ? AND `+visiblePostPredicate+`
		ORDER BY saved.created_at DESC, p.id DESC LIMIT ? OFFSET ?`,
		userID, userID, userID, userID, userID, userID, userID, userID, userID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list bookmarks: %w", err)
	}
	defer rows.Close()
	posts := make([]Post, 0, limit+1)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan bookmark: %w", err)
		}
		post.Body = ""
		posts = append(posts, post)
	}
	next := ""
	if len(posts) > limit {
		posts = posts[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return posts, next, rows.Err()
}

func (s *Service) Follow(ctx context.Context, followerID, followedID string) (bool, int, error) {
	if followerID == followedID {
		return false, 0, platform.Validation(map[string][]string{"user_id": {"不能关注自己"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("begin follow: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ? AND status = 'active'`, followedID).Scan(&exists); err == sql.ErrNoRows {
		return false, 0, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在")
	} else if err != nil {
		return false, 0, fmt.Errorf("check followed user: %w", err)
	}
	var blocked bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM blocks
		WHERE (blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?))`,
		followerID, followedID, followedID, followerID).Scan(&blocked); err != nil {
		return false, 0, fmt.Errorf("check follow block: %w", err)
	}
	if blocked {
		return false, 0, platform.Problem(http.StatusConflict, "relationship_blocked", "屏蔽关系下不能关注")
	}
	now := s.now().UTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO follows(follower_id, followed_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, followerID, followedID, now.Format(time.RFC3339Nano))
	if err != nil {
		return false, 0, fmt.Errorf("create follow: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 1 {
		if err := addNotification(ctx, tx, followedID, followerID, "follow", "user", followedID, map[string]any{}, now); err != nil {
			return false, 0, err
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM follows WHERE followed_id = ?`, followedID).Scan(&count); err != nil {
		return false, 0, fmt.Errorf("count followers: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("commit follow: %w", err)
	}
	return changed == 1, count, nil
}

func (s *Service) Unfollow(ctx context.Context, followerID, followedID string) (bool, int, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM follows WHERE follower_id = ? AND followed_id = ?`, followerID, followedID)
	if err != nil {
		return false, 0, fmt.Errorf("delete follow: %w", err)
	}
	changed, _ := result.RowsAffected()
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM follows WHERE followed_id = ?`, followedID).Scan(&count); err != nil {
		return false, 0, fmt.Errorf("count followers: %w", err)
	}
	return changed == 1, count, nil
}

func (s *Service) bookmarkHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, err := s.Bookmark(r.Context(), postID(r), current.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"bookmarked": true, "changed": changed})
}

func (s *Service) unbookmarkHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, err := s.Unbookmark(r.Context(), postID(r), current.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"bookmarked": false, "changed": changed})
}

func (s *Service) bookmarksHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	posts, next, err := s.Bookmarks(r.Context(), current.ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, posts, next)
}

func (s *Service) followHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, count, err := s.Follow(r.Context(), current.ID, userID(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"following": true, "changed": changed, "follower_count": count})
}

func (s *Service) unfollowHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, count, err := s.Unfollow(r.Context(), current.ID, userID(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"following": false, "changed": changed, "follower_count": count})
}
