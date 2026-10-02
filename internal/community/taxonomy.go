// Taxonomy and search keep categories, tags, posts, and public user discovery in
// one readable flow. User search deliberately never matches or exposes email.
package community

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func (s *Service) Categories(ctx context.Context) ([]Category, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.slug, c.name,
		       COUNT(CASE WHEN p.status = 'published' AND p.visibility = 'public' THEN 1 END)
		FROM categories c LEFT JOIN posts p ON p.category_id = c.id
		GROUP BY c.id, c.slug, c.name ORDER BY c.name ASC`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	items := []Category{}
	for rows.Next() {
		var item Category
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.PostCount); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) Tags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.slug, t.name,
		       COUNT(CASE WHEN p.status = 'published' AND p.visibility = 'public' THEN 1 END)
		FROM tags t
		LEFT JOIN post_tags pt ON pt.tag_id = t.id
		LEFT JOIN posts p ON p.id = pt.post_id
		GROUP BY t.id, t.slug, t.name ORDER BY t.name ASC`)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()
	items := []Tag{}
	for rows.Next() {
		var item Tag
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.PostCount); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) Search(ctx context.Context, query, kind, viewerID string, offset, limit int) (SearchResults, string, error) {
	query = strings.TrimSpace(query)
	if len([]rune(query)) < 2 || len([]rune(query)) > 100 {
		return SearchResults{}, "", platform.Validation(map[string][]string{"q": {"搜索词长度必须为 2 到 100 个字符"}})
	}
	if kind == "" {
		kind = "all"
	}
	if kind != "all" && kind != "posts" && kind != "users" && kind != "tags" {
		return SearchResults{}, "", platform.Validation(map[string][]string{"type": {"type 必须是 all、posts、users 或 tags"}})
	}
	result := SearchResults{Posts: []Post{}, Users: []PublicUser{}, Tags: []Tag{}}
	pattern := "%" + escapeLike(query) + "%"
	hasMore := false
	if kind == "all" || kind == "posts" {
		posts, more, err := s.searchPosts(ctx, pattern, viewerID, offset, limit)
		if err != nil {
			return SearchResults{}, "", err
		}
		result.Posts = posts
		hasMore = hasMore || more
	}
	if kind == "all" || kind == "users" {
		users, more, err := s.searchUsers(ctx, pattern, viewerID, offset, limit)
		if err != nil {
			return SearchResults{}, "", err
		}
		result.Users = users
		hasMore = hasMore || more
	}
	if kind == "all" || kind == "tags" {
		tags, more, err := s.searchTags(ctx, pattern, offset, limit)
		if err != nil {
			return SearchResults{}, "", err
		}
		result.Tags = tags
		hasMore = hasMore || more
	}
	next := ""
	if hasMore {
		next = platform.EncodeCursor(offset + limit)
	}
	return result, next, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func (s *Service) searchPosts(ctx context.Context, pattern, viewerID string, offset, limit int) ([]Post, bool, error) {
	rows, err := s.db.QueryContext(ctx, postSelect+` WHERE `+visiblePostPredicate+` AND (
		p.title LIKE ? ESCAPE '\' OR p.summary LIKE ? ESCAPE '\' OR p.body LIKE ? ESCAPE '\'
		OR EXISTS (SELECT 1 FROM post_tags search_pt JOIN tags search_tag ON search_tag.id = search_pt.tag_id WHERE search_pt.post_id = p.id AND search_tag.name LIKE ? ESCAPE '\')
	) ORDER BY p.published_at DESC, p.id DESC LIMIT ? OFFSET ?`,
		viewerID, viewerID, viewerID, viewerID, viewerID, viewerID, viewerID, viewerID, pattern, pattern, pattern, pattern, limit+1, offset)
	if err != nil {
		return nil, false, fmt.Errorf("search posts: %w", err)
	}
	defer rows.Close()
	posts := make([]Post, 0, limit+1)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, false, fmt.Errorf("scan searched post: %w", err)
		}
		post.Body = ""
		posts = append(posts, post)
	}
	more := len(posts) > limit
	if more {
		posts = posts[:limit]
	}
	return posts, more, rows.Err()
}

func (s *Service) searchUsers(ctx context.Context, pattern, viewerID string, offset, limit int) ([]PublicUser, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.handle, u.display_name, u.avatar_url, u.bio,
		       (SELECT COUNT(*) FROM follows count_follow WHERE count_follow.followed_id = u.id),
		       EXISTS(SELECT 1 FROM follows viewer_follow WHERE viewer_follow.follower_id = ? AND viewer_follow.followed_id = u.id)
		FROM users u
		WHERE u.status = 'active' AND (u.handle LIKE ? ESCAPE '\' OR u.display_name LIKE ? ESCAPE '\')
		  AND (? = '' OR NOT EXISTS (SELECT 1 FROM blocks b WHERE (b.blocker_id = ? AND b.blocked_id = u.id) OR (b.blocker_id = u.id AND b.blocked_id = ?)))
		ORDER BY u.display_name ASC, u.id ASC LIMIT ? OFFSET ?`, viewerID, pattern, pattern, viewerID, viewerID, viewerID, limit+1, offset)
	if err != nil {
		return nil, false, fmt.Errorf("search users: %w", err)
	}
	defer rows.Close()
	users := make([]PublicUser, 0, limit+1)
	for rows.Next() {
		var user PublicUser
		var following bool
		if err := rows.Scan(&user.ID, &user.Handle, &user.DisplayName, &user.AvatarURL, &user.Bio, &user.FollowerCount, &following); err != nil {
			return nil, false, fmt.Errorf("scan searched user: %w", err)
		}
		user.Name = user.DisplayName
		user.Following = following
		users = append(users, user)
	}
	more := len(users) > limit
	if more {
		users = users[:limit]
	}
	return users, more, rows.Err()
}

func (s *Service) searchTags(ctx context.Context, pattern string, offset, limit int) ([]Tag, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.slug, t.name, COUNT(CASE WHEN p.status = 'published' AND p.visibility = 'public' THEN 1 END)
		FROM tags t LEFT JOIN post_tags pt ON pt.tag_id = t.id LEFT JOIN posts p ON p.id = pt.post_id
		WHERE t.name LIKE ? ESCAPE '\' OR t.slug LIKE ? ESCAPE '\'
		GROUP BY t.id, t.slug, t.name ORDER BY t.name ASC LIMIT ? OFFSET ?`, pattern, pattern, limit+1, offset)
	if err != nil {
		return nil, false, fmt.Errorf("search tags: %w", err)
	}
	defer rows.Close()
	tags := make([]Tag, 0, limit+1)
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.ID, &tag.Slug, &tag.Name, &tag.PostCount); err != nil {
			return nil, false, fmt.Errorf("scan searched tag: %w", err)
		}
		tags = append(tags, tag)
	}
	more := len(tags) > limit
	if more {
		tags = tags[:limit]
	}
	return tags, more, rows.Err()
}

func (s *Service) categoriesHTTP(w http.ResponseWriter, r *http.Request) {
	items, err := s.Categories(r.Context())
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, items)
}

func (s *Service) tagsHTTP(w http.ResponseWriter, r *http.Request) {
	items, err := s.Tags(r.Context())
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, items)
}

func (s *Service) searchHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	user, _ := identity.UserFromContext(r.Context())
	results, next, err := s.Search(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("type"), user.ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, results, next)
}
