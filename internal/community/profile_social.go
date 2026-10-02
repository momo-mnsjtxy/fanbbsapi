package community

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

type PublicProfile struct {
	ID             string `json:"id"`
	Handle         string `json:"handle"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	AvatarURL      string `json:"avatar_url"`
	CoverURL       string `json:"cover_url"`
	Bio            string `json:"bio"`
	FollowerCount  int    `json:"follower_count"`
	FollowingCount int    `json:"following_count"`
	PostCount      int    `json:"post_count"`
	Following      bool   `json:"following"`
	FollowsYou     bool   `json:"follows_you"`
	BlockedByMe    bool   `json:"blocked_by_me"`
}

type CategoryFollow struct {
	Category   Category `json:"category"`
	FollowedAt string   `json:"followed_at"`
}

func (s *Service) PublicProfile(ctx context.Context, targetID, viewerID string) (PublicProfile, error) {
	var item PublicProfile
	var following, followsYou, blockedByMe int
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.handle, u.display_name, u.avatar_url,
		       COALESCE('/api/v1/media/' || cover.asset_id, ''), u.bio,
		       (SELECT COUNT(*) FROM follows f WHERE f.followed_id = u.id),
		       (SELECT COUNT(*) FROM follows f WHERE f.follower_id = u.id),
		       (SELECT COUNT(*) FROM posts p WHERE p.author_id = u.id AND p.status = 'published' AND (
		           p.visibility = 'public' OR p.author_id = ? OR (p.visibility = 'followers' AND EXISTS(
		               SELECT 1 FROM follows visible_follow WHERE visible_follow.follower_id = ? AND visible_follow.followed_id = p.author_id
		           ))
		       )),
		       EXISTS(SELECT 1 FROM follows f WHERE f.follower_id = ? AND f.followed_id = u.id),
		       EXISTS(SELECT 1 FROM follows f WHERE f.follower_id = u.id AND f.followed_id = ?),
		       EXISTS(SELECT 1 FROM blocks b WHERE b.blocker_id = ? AND b.blocked_id = u.id)
		FROM users u LEFT JOIN user_covers cover ON cover.user_id = u.id
		WHERE u.id = ? AND u.status = 'active'
		  AND (? = '' OR NOT EXISTS (
		      SELECT 1 FROM blocks privacy_block
		      WHERE (privacy_block.blocker_id = ? AND privacy_block.blocked_id = u.id)
		         OR (privacy_block.blocker_id = u.id AND privacy_block.blocked_id = ?)
		  ))`, viewerID, viewerID, viewerID, viewerID, viewerID, targetID, viewerID, viewerID, viewerID).
		Scan(&item.ID, &item.Handle, &item.DisplayName, &item.AvatarURL, &item.CoverURL, &item.Bio,
			&item.FollowerCount, &item.FollowingCount, &item.PostCount, &following, &followsYou, &blockedByMe)
	if err == sql.ErrNoRows {
		return PublicProfile{}, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在")
	}
	if err != nil {
		return PublicProfile{}, fmt.Errorf("load public profile: %w", err)
	}
	item.Name = item.DisplayName
	item.Following = following != 0
	item.FollowsYou = followsYou != 0
	item.BlockedByMe = blockedByMe != 0
	return item, nil
}

func (s *Service) SocialUsers(ctx context.Context, targetID, viewerID, relation string, offset, limit int) ([]PublicUser, string, error) {
	if _, err := s.PublicProfile(ctx, targetID, viewerID); err != nil {
		return nil, "", err
	}
	join := `JOIN follows relation ON relation.follower_id = u.id AND relation.followed_id = ?`
	if relation == "following" {
		join = `JOIN follows relation ON relation.followed_id = u.id AND relation.follower_id = ?`
	} else if relation != "followers" {
		return nil, "", platform.Validation(map[string][]string{"relation": {"关系类型无效"}})
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.handle, u.display_name, u.avatar_url, u.bio,
		       (SELECT COUNT(*) FROM follows count_follow WHERE count_follow.followed_id = u.id),
		       EXISTS(SELECT 1 FROM follows viewer_follow WHERE viewer_follow.follower_id = ? AND viewer_follow.followed_id = u.id)
		FROM users u `+join+`
		WHERE u.status = 'active' AND (? = '' OR NOT EXISTS (
			SELECT 1 FROM blocks b
			WHERE (b.blocker_id = ? AND b.blocked_id = u.id) OR (b.blocker_id = u.id AND b.blocked_id = ?)
		))
		ORDER BY relation.created_at DESC, u.id DESC LIMIT ? OFFSET ?`, viewerID, targetID, viewerID, viewerID, viewerID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list %s: %w", relation, err)
	}
	defer rows.Close()
	items := []PublicUser{}
	for rows.Next() {
		var item PublicUser
		var following int
		if err := rows.Scan(&item.ID, &item.Handle, &item.DisplayName, &item.AvatarURL, &item.Bio, &item.FollowerCount, &following); err != nil {
			return nil, "", fmt.Errorf("scan %s: %w", relation, err)
		}
		item.Name = item.DisplayName
		item.Following = following != 0
		items = append(items, item)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return items, next, rows.Err()
}

func (s *Service) Block(ctx context.Context, blockerID, blockedID string) (bool, error) {
	if blockerID == blockedID {
		return false, platform.Validation(map[string][]string{"user_id": {"不能屏蔽自己"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin block: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ? AND status = 'active'`, blockedID).Scan(&exists); err == sql.ErrNoRows {
		return false, platform.Problem(http.StatusNotFound, "user_not_found", "用户不存在")
	} else if err != nil {
		return false, fmt.Errorf("check blocked user: %w", err)
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `INSERT INTO blocks(blocker_id, blocked_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, blockerID, blockedID, now)
	if err != nil {
		return false, fmt.Errorf("create block: %w", err)
	}
	changed, _ := result.RowsAffected()
	if _, err := tx.ExecContext(ctx, `DELETE FROM follows WHERE (follower_id = ? AND followed_id = ?) OR (follower_id = ? AND followed_id = ?)`, blockerID, blockedID, blockedID, blockerID); err != nil {
		return false, fmt.Errorf("remove blocked follows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit block: %w", err)
	}
	return changed == 1, nil
}

func (s *Service) Unblock(ctx context.Context, blockerID, blockedID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM blocks WHERE blocker_id = ? AND blocked_id = ?`, blockerID, blockedID)
	if err != nil {
		return false, fmt.Errorf("delete block: %w", err)
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

func (s *Service) BlockedUsers(ctx context.Context, userID string, offset, limit int) ([]PublicUser, string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.handle, u.display_name, u.avatar_url, u.bio,
		       (SELECT COUNT(*) FROM follows f WHERE f.followed_id = u.id)
		FROM blocks b JOIN users u ON u.id = b.blocked_id
		WHERE b.blocker_id = ? AND u.status = 'active'
		ORDER BY b.created_at DESC, u.id DESC LIMIT ? OFFSET ?`, userID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list blocked users: %w", err)
	}
	defer rows.Close()
	items := []PublicUser{}
	for rows.Next() {
		var item PublicUser
		if err := rows.Scan(&item.ID, &item.Handle, &item.DisplayName, &item.AvatarURL, &item.Bio, &item.FollowerCount); err != nil {
			return nil, "", fmt.Errorf("scan blocked user: %w", err)
		}
		item.Name = item.DisplayName
		items = append(items, item)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return items, next, rows.Err()
}

func (s *Service) FollowCategory(ctx context.Context, userID, categoryID string) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM categories WHERE id = ?`, categoryID).Scan(&exists); err == sql.ErrNoRows {
		return false, platform.Problem(http.StatusNotFound, "category_not_found", "分类不存在")
	} else if err != nil {
		return false, fmt.Errorf("check category: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO category_follows(category_id, user_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, categoryID, userID, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("follow category: %w", err)
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

func (s *Service) UnfollowCategory(ctx context.Context, userID, categoryID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM category_follows WHERE category_id = ? AND user_id = ?`, categoryID, userID)
	if err != nil {
		return false, fmt.Errorf("unfollow category: %w", err)
	}
	changed, _ := result.RowsAffected()
	return changed == 1, nil
}

func (s *Service) FollowedCategories(ctx context.Context, userID string, offset, limit int) ([]CategoryFollow, string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.slug, c.name,
		       COUNT(CASE WHEN p.status = 'published' AND p.visibility = 'public' THEN 1 END), f.created_at
		FROM category_follows f JOIN categories c ON c.id = f.category_id
		LEFT JOIN posts p ON p.category_id = c.id
		WHERE f.user_id = ?
		GROUP BY c.id, c.slug, c.name, f.created_at
		ORDER BY f.created_at DESC, c.id DESC LIMIT ? OFFSET ?`, userID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list category follows: %w", err)
	}
	defer rows.Close()
	items := []CategoryFollow{}
	for rows.Next() {
		var item CategoryFollow
		if err := rows.Scan(&item.Category.ID, &item.Category.Slug, &item.Category.Name, &item.Category.PostCount, &item.FollowedAt); err != nil {
			return nil, "", fmt.Errorf("scan category follow: %w", err)
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

func (s *Service) publicProfileHTTP(w http.ResponseWriter, r *http.Request) {
	viewer, _ := identity.UserFromContext(r.Context())
	item, err := s.PublicProfile(r.Context(), userID(r), viewer.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) socialUsersHTTP(relation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		offset, limit, err := pagination(r)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		viewer, _ := identity.UserFromContext(r.Context())
		items, next, err := s.SocialUsers(r.Context(), userID(r), viewer.ID, relation, offset, limit)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteList(w, r, items, next)
	}
}

func (s *Service) blockHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, err := s.Block(r.Context(), current.ID, userID(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"blocked": true, "changed": changed})
}

func (s *Service) unblockHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, err := s.Unblock(r.Context(), current.ID, userID(r))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"blocked": false, "changed": changed})
}

func (s *Service) blocksHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	items, next, err := s.BlockedUsers(r.Context(), current.ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) followCategoryHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, err := s.FollowCategory(r.Context(), current.ID, chi.URLParam(r, "categoryID"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"following": true, "changed": changed})
}

func (s *Service) unfollowCategoryHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, err := s.UnfollowCategory(r.Context(), current.ID, chi.URLParam(r, "categoryID"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"following": false, "changed": changed})
}

func (s *Service) followedCategoriesHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	items, next, err := s.FollowedCategories(r.Context(), current.ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}
