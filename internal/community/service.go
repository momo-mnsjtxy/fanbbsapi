// Community service implements the post, feed, comment, like, and repost flows.
// Every multi-table write is committed as one SQLite transaction.
package community

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fanbbs.local/backend/internal/blob"
	"fanbbs.local/backend/internal/platform"
)

type Service struct {
	db             *sql.DB
	blobs          blob.Store
	now            func() time.Time
	postLimiter    *platform.RateLimiter
	commentLimiter *platform.RateLimiter
	messageLimiter *platform.RateLimiter
	uploadLimiter  *platform.RateLimiter
}

func NewService(db *sql.DB, blobs blob.Store) *Service {
	return &Service{
		db: db, blobs: blobs, now: time.Now,
		postLimiter: platform.NewRateLimiter(4096, 30, time.Minute), commentLimiter: platform.NewRateLimiter(4096, 60, time.Minute),
		messageLimiter: platform.NewRateLimiter(4096, 60, time.Minute), uploadLimiter: platform.NewRateLimiter(4096, 20, time.Minute),
	}
}

type CreatePostInput struct {
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Body       string   `json:"body"`
	Content    string   `json:"content"`
	CategoryID string   `json:"category_id"`
	TagIDs     []string `json:"tag_ids"`
	MediaIDs   []string `json:"media_ids"`
	Visibility string   `json:"visibility"`
	Status     string   `json:"status,omitempty"`
}

func (s *Service) CreatePost(ctx context.Context, authorID, idempotencyKey string, input CreatePostInput) (Post, bool, error) {
	input.Kind = strings.TrimSpace(input.Kind)
	input.Title = strings.TrimSpace(input.Title)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Body = strings.TrimSpace(input.Body)
	input.Content = strings.TrimSpace(input.Content)
	input.CategoryID = strings.TrimSpace(input.CategoryID)
	input.Visibility = strings.TrimSpace(input.Visibility)
	input.Status = strings.TrimSpace(input.Status)
	if input.Body == "" {
		input.Body = input.Content
	}
	if input.Title == "" && input.Body != "" {
		characters := []rune(input.Body)
		if len(characters) > 60 {
			characters = characters[:60]
		}
		input.Title = string(characters)
		if len(characters) < 3 {
			input.Title = "社区动态"
		}
	}
	if input.Kind == "" {
		input.Kind = "article"
	}
	if input.Visibility == "" {
		input.Visibility = "public"
	}
	fields := map[string][]string{}
	if len([]rune(input.Title)) < 3 || len([]rune(input.Title)) > 120 {
		fields["title"] = []string{"标题长度必须为 3 到 120 个字符"}
	}
	if len([]rune(input.Body)) < 1 || len([]rune(input.Body)) > 50000 {
		fields["body"] = []string{"正文长度必须为 1 到 50000 个字符"}
	}
	if input.Kind != "article" && input.Kind != "image" && input.Kind != "video" {
		fields["kind"] = []string{"类型必须是 article、image 或 video"}
	}
	if input.Visibility != "public" && input.Visibility != "followers" {
		fields["visibility"] = []string{"可见性必须是 public 或 followers"}
	}
	if input.Status != "" && input.Status != "pending" {
		fields["status"] = []string{"status 只能省略或设为 pending"}
	}
	if len(input.Summary) > 500 {
		fields["summary"] = []string{"摘要不能超过 500 个字符"}
	}
	if idempotencyKey != "" && len(idempotencyKey) > 128 {
		fields["idempotency_key"] = []string{"Idempotency-Key 不能超过 128 个字符"}
	}
	if len(fields) > 0 {
		return Post{}, false, platform.Validation(fields)
	}
	if input.CategoryID != "" {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM categories WHERE id = ?`, input.CategoryID).Scan(&exists); err == sql.ErrNoRows {
			return Post{}, false, platform.Validation(map[string][]string{"category_id": {"分类不存在"}})
		} else if err != nil {
			return Post{}, false, fmt.Errorf("check category: %w", err)
		}
	}
	if len(input.TagIDs) > 10 {
		return Post{}, false, platform.Validation(map[string][]string{"tag_ids": {"每篇文章最多选择 10 个标签"}})
	}
	if len(input.MediaIDs) > 8 {
		return Post{}, false, platform.Validation(map[string][]string{"media_ids": {"每篇文章最多关联 8 个媒体文件"}})
	}
	seenTags := map[string]bool{}
	for _, tagID := range input.TagIDs {
		tagID = strings.TrimSpace(tagID)
		if tagID == "" || seenTags[tagID] {
			continue
		}
		seenTags[tagID] = true
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM tags WHERE id = ?`, tagID).Scan(&exists); err == sql.ErrNoRows {
			return Post{}, false, platform.Validation(map[string][]string{"tag_ids": {"标签不存在"}})
		} else if err != nil {
			return Post{}, false, fmt.Errorf("check tag: %w", err)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, false, fmt.Errorf("begin create post: %w", err)
	}
	defer tx.Rollback()
	if idempotencyKey != "" {
		var existingID string
		err := tx.QueryRowContext(ctx, `SELECT resource_id FROM idempotency_keys WHERE user_id = ? AND scope = 'create_post' AND key = ?`, authorID, idempotencyKey).Scan(&existingID)
		if err == nil {
			tx.Rollback()
			post, err := s.postForOwner(ctx, existingID, authorID)
			return post, true, err
		}
		if err != sql.ErrNoRows {
			return Post{}, false, fmt.Errorf("check post idempotency: %w", err)
		}
	}
	postID, err := platform.NewID("post")
	if err != nil {
		return Post{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	var category any
	if input.CategoryID != "" {
		category = input.CategoryID
	}
	databaseStatus := "published"
	if input.Status == "pending" {
		databaseStatus = "draft"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO posts(id, author_id, kind, title, summary, body, category_id, status, visibility, created_at, published_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, postID, authorID, input.Kind, input.Title, input.Summary, input.Body, category, databaseStatus, input.Visibility, now, now)
	if err != nil {
		return Post{}, false, fmt.Errorf("insert post: %w", err)
	}
	if input.Status == "pending" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_moderation(post_id, state, updated_at) VALUES (?, 'pending', ?)`, postID, now); err != nil {
			return Post{}, false, fmt.Errorf("submit post for moderation: %w", err)
		}
	}
	for tagID := range seenTags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_tags(post_id, tag_id) VALUES (?, ?)`, postID, tagID); err != nil {
			return Post{}, false, fmt.Errorf("attach post tag: %w", err)
		}
	}
	seenMedia := map[string]bool{}
	for position, mediaID := range input.MediaIDs {
		mediaID = strings.TrimSpace(mediaID)
		if mediaID == "" || seenMedia[mediaID] {
			continue
		}
		seenMedia[mediaID] = true
		var exists int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM media_assets m
			WHERE m.id = ? AND m.owner_id = ? AND m.purpose = 'post' AND m.status = 'ready'
			  AND NOT EXISTS (SELECT 1 FROM post_media used WHERE used.asset_id = m.id)`, mediaID, authorID).Scan(&exists)
		if err == sql.ErrNoRows {
			return Post{}, false, platform.Validation(map[string][]string{"media_ids": {"媒体不存在、无权使用或已被关联"}})
		}
		if err != nil {
			return Post{}, false, fmt.Errorf("check post media: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_media(post_id, asset_id, position) VALUES (?, ?, ?)`, postID, mediaID, position); err != nil {
			return Post{}, false, fmt.Errorf("attach post media: %w", err)
		}
	}
	if idempotencyKey != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(user_id, scope, key, resource_id, created_at) VALUES (?, 'create_post', ?, ?, ?)`, authorID, idempotencyKey, postID, now); err != nil {
			return Post{}, false, fmt.Errorf("save post idempotency: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Post{}, false, fmt.Errorf("commit create post: %w", err)
	}
	post, err := s.postForOwner(ctx, postID, authorID)
	return post, false, err
}

func (s *Service) ListPosts(ctx context.Context, feed, viewerID string, offset, limit int) ([]Post, string, error) {
	return s.ListPostsFiltered(ctx, feed, viewerID, "", "", offset, limit)
}

func (s *Service) ListPostsFiltered(ctx context.Context, feed, viewerID, categoryID, tagID string, offset, limit int) ([]Post, string, error) {
	if feed == "" {
		feed = "recommend"
	}
	if feed == "recommended" {
		feed = "recommend"
	}
	if feed != "recommend" && feed != "latest" && feed != "global" && feed != "following" {
		return nil, "", platform.Validation(map[string][]string{"feed": {"feed 必须是 recommend、latest、global 或 following"}})
	}
	if feed == "following" && viewerID == "" {
		return nil, "", platform.Problem(http.StatusUnauthorized, "authentication_required", "关注流需要登录")
	}
	categoryID = strings.TrimSpace(categoryID)
	tagID = strings.TrimSpace(tagID)
	if categoryID != "" {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM categories WHERE id = ?`, categoryID).Scan(&exists); err == sql.ErrNoRows {
			return nil, "", platform.Validation(map[string][]string{"category_id": {"分类不存在"}})
		} else if err != nil {
			return nil, "", fmt.Errorf("check feed category: %w", err)
		}
	}
	if tagID != "" {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM tags WHERE id = ?`, tagID).Scan(&exists); err == sql.ErrNoRows {
			return nil, "", platform.Validation(map[string][]string{"tag_id": {"标签不存在"}})
		} else if err != nil {
			return nil, "", fmt.Errorf("check feed tag: %w", err)
		}
	}
	where := visiblePostPredicate
	order := `COALESCE((SELECT is_pinned FROM post_moderation WHERE post_id = p.id), 0) DESC, p.published_at DESC, p.id DESC`
	args := []any{viewerID, viewerID, viewerID}
	args = append(args, visiblePostArgs(viewerID)...)
	if feed == "following" {
		where += ` AND EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = ? AND f.followed_id = p.author_id)`
		args = append(args, viewerID)
	}
	if feed == "recommend" {
		order = `COALESCE((SELECT is_pinned FROM post_moderation WHERE post_id = p.id), 0) DESC, COALESCE((SELECT is_recommended FROM post_moderation WHERE post_id = p.id), 0) DESC, (p.like_count * 3 + p.comment_count * 5 + p.repost_count * 4) DESC, p.published_at DESC, p.id DESC`
	}
	if categoryID != "" {
		where += ` AND p.category_id = ?`
		args = append(args, categoryID)
	}
	if tagID != "" {
		where += ` AND EXISTS(SELECT 1 FROM post_tags feed_tag WHERE feed_tag.post_id = p.id AND feed_tag.tag_id = ?)`
		args = append(args, tagID)
	}
	query := postSelect + ` WHERE ` + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list %s feed: %w", feed, err)
	}
	defer rows.Close()
	posts := make([]Post, 0, limit+1)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan feed post: %w", err)
		}
		post.Body = ""
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("read feed posts: %w", err)
	}
	next := ""
	if len(posts) > limit {
		posts = posts[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return posts, next, nil
}

func (s *Service) Post(ctx context.Context, postID, viewerID string) (Post, error) {
	post, err := scanPost(s.db.QueryRowContext(ctx, postSelect+`
		WHERE p.id = ? AND `+visiblePostPredicate, append([]any{viewerID, viewerID, viewerID, postID}, visiblePostArgs(viewerID)...)...))
	if err == sql.ErrNoRows {
		return Post{}, platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	}
	if err != nil {
		return Post{}, fmt.Errorf("load post: %w", err)
	}
	return post, nil
}

func (s *Service) postForOwner(ctx context.Context, postID, ownerID string) (Post, error) {
	post, err := scanPost(s.db.QueryRowContext(ctx, postSelect+`
		WHERE p.id = ? AND p.author_id = ? AND p.status <> 'deleted'`, ownerID, ownerID, ownerID, postID, ownerID))
	if err == sql.ErrNoRows {
		return Post{}, platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	}
	if err != nil {
		return Post{}, fmt.Errorf("load owner post: %w", err)
	}
	return post, nil
}

const visiblePostPredicate = `p.status = 'published'
AND COALESCE((SELECT state FROM post_moderation WHERE post_id = p.id), 'published') = 'published' AND (
	p.visibility = 'public'
	OR p.author_id = ?
	OR (p.visibility = 'followers' AND EXISTS (
		SELECT 1 FROM follows visibility_follow
		WHERE visibility_follow.follower_id = ? AND visibility_follow.followed_id = p.author_id
	))
) AND (? = '' OR NOT EXISTS (
	SELECT 1 FROM blocks visibility_block
	WHERE (visibility_block.blocker_id = ? AND visibility_block.blocked_id = p.author_id)
	   OR (visibility_block.blocker_id = p.author_id AND visibility_block.blocked_id = ?)
))`

func visiblePostArgs(viewerID string) []any {
	return []any{viewerID, viewerID, viewerID, viewerID, viewerID}
}

const postSelect = `
	SELECT p.id, p.kind, COALESCE(p.repost_of, ''), p.title, p.summary, p.body,
	       COALESCE((SELECT state FROM post_moderation WHERE post_id = p.id), p.status), p.visibility, p.version,
	       COALESCE((SELECT is_pinned FROM post_moderation WHERE post_id = p.id), 0),
	       COALESCE((SELECT is_recommended FROM post_moderation WHERE post_id = p.id), 0), p.like_count, p.comment_count,
	       p.repost_count, p.created_at, p.published_at,
	       u.id, u.handle, u.display_name, u.avatar_url,
	       COALESCE(c.id, ''), COALESCE(c.slug, ''), COALESCE(c.name, ''),
	       COALESCE((SELECT group_concat(tag.id || char(31) || tag.slug || char(31) || tag.name, char(30))
	                 FROM post_tags selected_tag JOIN tags tag ON tag.id = selected_tag.tag_id
	                 WHERE selected_tag.post_id = p.id), ''),
	       COALESCE((SELECT group_concat(encoded, char(30)) FROM (
	                 SELECT media.id || char(31) || media.mime_type || char(31) || media.size_bytes || char(31) || media.alt_text AS encoded
	                 FROM post_media selected_media JOIN media_assets media ON media.id = selected_media.asset_id
	                 WHERE selected_media.post_id = p.id AND media.status = 'ready'
	                 ORDER BY selected_media.position ASC)), ''),
	       EXISTS(SELECT 1 FROM reactions r WHERE r.post_id = p.id AND r.user_id = ?),
	       EXISTS(SELECT 1 FROM reposts rp WHERE rp.post_id = p.id AND rp.user_id = ?),
	       EXISTS(SELECT 1 FROM bookmarks b WHERE b.post_id = p.id AND b.user_id = ?)
	FROM posts p JOIN users u ON u.id = p.author_id
	LEFT JOIN categories c ON c.id = p.category_id`

type scanner interface {
	Scan(...any) error
}

func scanPost(row scanner) (Post, error) {
	var post Post
	var category Category
	var liked, reposted, bookmarked, pinned, recommended int
	var tagData, mediaData string
	err := row.Scan(&post.ID, &post.Kind, &post.RepostOf, &post.Title, &post.Summary, &post.Body,
		&post.Status, &post.Visibility, &post.Version, &pinned, &recommended, &post.LikeCount, &post.CommentCount,
		&post.RepostCount, &post.CreatedAt, &post.PublishedAt,
		&post.Author.ID, &post.Author.Handle, &post.Author.DisplayName, &post.Author.AvatarURL,
		&category.ID, &category.Slug, &category.Name, &tagData, &mediaData, &liked, &reposted, &bookmarked)
	if err != nil {
		return Post{}, err
	}
	if category.ID != "" {
		post.Category = &category
	}
	post.Tags = []Tag{}
	if tagData != "" {
		for _, encodedTag := range strings.Split(tagData, string(rune(30))) {
			parts := strings.SplitN(encodedTag, string(rune(31)), 3)
			if len(parts) == 3 {
				post.Tags = append(post.Tags, Tag{ID: parts[0], Slug: parts[1], Name: parts[2]})
			}
		}
	}
	post.Media = []MediaAsset{}
	if mediaData != "" {
		for _, encodedMedia := range strings.Split(mediaData, string(rune(30))) {
			parts := strings.SplitN(encodedMedia, string(rune(31)), 4)
			if len(parts) == 4 {
				size, _ := strconv.ParseInt(parts[2], 10, 64)
				post.Media = append(post.Media, MediaAsset{ID: parts[0], MIMEType: parts[1], SizeBytes: size, AltText: parts[3], URL: "/api/v1/media/" + parts[0]})
			}
		}
	}
	post.Liked = liked != 0
	post.Pinned = pinned != 0
	post.Recommended = recommended != 0
	post.Reposted = reposted != 0
	post.Bookmarked = bookmarked != 0
	post.Author.Name = post.Author.DisplayName
	post.Content = post.Body
	post.Likes = post.LikeCount
	post.Comments = post.CommentCount
	post.Reposts = post.RepostCount
	post.Avatar = firstCharacter(post.Author.DisplayName)
	post.Age = relativeAge(post.PublishedAt)
	return post, nil
}

func (s *Service) ListComments(ctx context.Context, postID, viewerID string, offset, limit int) ([]Comment, string, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM posts p WHERE p.id = ? AND `+visiblePostPredicate, append([]any{postID}, visiblePostArgs(viewerID)...)...).Scan(&exists); err == sql.ErrNoRows {
		return nil, "", platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	} else if err != nil {
		return nil, "", fmt.Errorf("check comment post: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.post_id, COALESCE(c.parent_id, ''), COALESCE(c.root_id, ''), c.depth,
		       c.body, c.like_count, c.version, c.created_at, u.id, u.handle, u.display_name, u.avatar_url,
		       EXISTS(SELECT 1 FROM comment_reactions reaction WHERE reaction.comment_id = c.id AND reaction.user_id = ?)
		FROM comments c JOIN users u ON u.id = c.author_id
		WHERE c.post_id = ? AND c.status = 'published'
		ORDER BY c.created_at DESC, c.id DESC LIMIT ? OFFSET ?`, viewerID, postID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()
	comments := make([]Comment, 0, limit+1)
	for rows.Next() {
		var comment Comment
		var liked int
		if err := rows.Scan(&comment.ID, &comment.PostID, &comment.ParentID, &comment.RootID, &comment.Depth,
			&comment.Body, &comment.LikeCount, &comment.Version, &comment.CreatedAt, &comment.Author.ID, &comment.Author.Handle,
			&comment.Author.DisplayName, &comment.Author.AvatarURL, &liked); err != nil {
			return nil, "", fmt.Errorf("scan comment: %w", err)
		}
		comment.Liked = liked != 0
		comments = append(comments, comment)
		comments[len(comments)-1] = shapeComment(comment)
	}
	next := ""
	if len(comments) > limit {
		comments = comments[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return comments, next, rows.Err()
}

func (s *Service) AddComment(ctx context.Context, postID, authorID, parentID, idempotencyKey, body string) (Comment, bool, error) {
	body = strings.TrimSpace(body)
	if len([]rune(body)) < 1 || len([]rune(body)) > 2000 {
		return Comment{}, false, platform.Validation(map[string][]string{"body": {"评论长度必须为 1 到 2000 个字符"}})
	}
	if len(idempotencyKey) > 128 {
		return Comment{}, false, platform.Validation(map[string][]string{"idempotency_key": {"Idempotency-Key 不能超过 128 个字符"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Comment{}, false, fmt.Errorf("begin comment: %w", err)
	}
	defer tx.Rollback()
	if idempotencyKey != "" {
		var existingID string
		err := tx.QueryRowContext(ctx, `SELECT resource_id FROM idempotency_keys WHERE user_id = ? AND scope = 'create_comment' AND key = ?`, authorID, idempotencyKey).Scan(&existingID)
		if err == nil {
			tx.Rollback()
			comment, err := s.comment(ctx, existingID)
			return comment, true, err
		}
		if err != sql.ErrNoRows {
			return Comment{}, false, fmt.Errorf("check comment idempotency: %w", err)
		}
	}
	var recipientID string
	if err := tx.QueryRowContext(ctx, `SELECT p.author_id FROM posts p WHERE p.id = ? AND `+visiblePostPredicate, append([]any{postID}, visiblePostArgs(authorID)...)...).Scan(&recipientID); err == sql.ErrNoRows {
		return Comment{}, false, platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	} else if err != nil {
		return Comment{}, false, fmt.Errorf("check comment target: %w", err)
	}
	depth := 0
	rootID := ""
	if parentID != "" {
		var parentRoot string
		err := tx.QueryRowContext(ctx, `SELECT depth, COALESCE(root_id, ''), author_id FROM comments WHERE id = ? AND post_id = ? AND status = 'published'`, parentID, postID).Scan(&depth, &parentRoot, &recipientID)
		if err == sql.ErrNoRows {
			return Comment{}, false, platform.Validation(map[string][]string{"parent_id": {"被回复的评论不存在"}})
		}
		if err != nil {
			return Comment{}, false, fmt.Errorf("load parent comment: %w", err)
		}
		depth++
		if depth > 2 {
			return Comment{}, false, platform.Validation(map[string][]string{"parent_id": {"回复最多支持三层"}})
		}
		rootID = parentRoot
		if rootID == "" {
			rootID = parentID
		}
	}
	commentID, err := platform.NewID("cmt")
	if err != nil {
		return Comment{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	var parent, root any
	if parentID != "" {
		parent = parentID
	}
	if rootID != "" {
		root = rootID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, post_id, author_id, parent_id, root_id, depth, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		commentID, postID, authorID, parent, root, depth, body, now); err != nil {
		return Comment{}, false, fmt.Errorf("insert comment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET comment_count = comment_count + 1 WHERE id = ?`, postID); err != nil {
		return Comment{}, false, fmt.Errorf("increment comment count: %w", err)
	}
	if err := addNotification(ctx, tx, recipientID, authorID, "comment", "post", postID, map[string]string{"comment_id": commentID}, s.now().UTC()); err != nil {
		return Comment{}, false, err
	}
	if idempotencyKey != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(user_id, scope, key, resource_id, created_at) VALUES (?, 'create_comment', ?, ?, ?)`, authorID, idempotencyKey, commentID, now); err != nil {
			return Comment{}, false, fmt.Errorf("save comment idempotency: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Comment{}, false, fmt.Errorf("commit comment: %w", err)
	}
	comment, err := s.comment(ctx, commentID)
	return comment, false, err
}

func (s *Service) comment(ctx context.Context, commentID string) (Comment, error) {
	var comment Comment
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.post_id, COALESCE(c.parent_id, ''), COALESCE(c.root_id, ''), c.depth,
		       c.body, c.like_count, c.version, c.created_at, u.id, u.handle, u.display_name, u.avatar_url
		FROM comments c JOIN users u ON u.id = c.author_id WHERE c.id = ?`, commentID).Scan(
		&comment.ID, &comment.PostID, &comment.ParentID, &comment.RootID, &comment.Depth,
		&comment.Body, &comment.LikeCount, &comment.Version, &comment.CreatedAt, &comment.Author.ID, &comment.Author.Handle,
		&comment.Author.DisplayName, &comment.Author.AvatarURL)
	if err != nil {
		return Comment{}, fmt.Errorf("load comment: %w", err)
	}
	return shapeComment(comment), nil
}

func shapeComment(comment Comment) Comment {
	comment.Author.Name = comment.Author.DisplayName
	comment.Content = comment.Body
	comment.Age = relativeAge(comment.CreatedAt)
	return comment
}

func firstCharacter(value string) string {
	characters := []rune(strings.TrimSpace(value))
	if len(characters) == 0 {
		return "?"
	}
	return string(characters[0])
}

func relativeAge(raw string) string {
	created, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return "刚刚"
	}
	difference := time.Since(created)
	if difference < time.Minute {
		return "刚刚"
	}
	if difference < time.Hour {
		return fmt.Sprintf("%d 分钟", int(difference.Minutes()))
	}
	if difference < 24*time.Hour {
		return fmt.Sprintf("%d 小时", int(difference.Hours()))
	}
	return fmt.Sprintf("%d 天", int(difference.Hours()/24))
}

func (s *Service) Like(ctx context.Context, postID, userID string) (bool, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("begin like: %w", err)
	}
	defer tx.Rollback()
	if err := ensureVisiblePost(ctx, tx, postID, userID); err != nil {
		return false, 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO reactions(post_id, user_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, postID, userID, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, 0, fmt.Errorf("insert like: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE posts SET like_count = like_count + 1 WHERE id = ?`, postID); err != nil {
			return false, 0, fmt.Errorf("increment like count: %w", err)
		}
		var recipientID string
		if err := tx.QueryRowContext(ctx, `SELECT author_id FROM posts WHERE id = ?`, postID).Scan(&recipientID); err != nil {
			return false, 0, fmt.Errorf("load liked post author: %w", err)
		}
		if err := addNotification(ctx, tx, recipientID, userID, "like", "post", postID, map[string]any{}, s.now().UTC()); err != nil {
			return false, 0, err
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT like_count FROM posts WHERE id = ?`, postID).Scan(&count); err != nil {
		return false, 0, fmt.Errorf("read like count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("commit like: %w", err)
	}
	return changed == 1, count, nil
}

func (s *Service) Unlike(ctx context.Context, postID, userID string) (bool, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("begin unlike: %w", err)
	}
	defer tx.Rollback()
	if err := ensureVisiblePost(ctx, tx, postID, userID); err != nil {
		return false, 0, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM reactions WHERE post_id = ? AND user_id = ?`, postID, userID)
	if err != nil {
		return false, 0, fmt.Errorf("delete like: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE posts SET like_count = CASE WHEN like_count > 0 THEN like_count - 1 ELSE 0 END WHERE id = ?`, postID); err != nil {
			return false, 0, fmt.Errorf("decrement like count: %w", err)
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT like_count FROM posts WHERE id = ?`, postID).Scan(&count); err != nil {
		return false, 0, fmt.Errorf("read like count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("commit unlike: %w", err)
	}
	return changed == 1, count, nil
}

func (s *Service) Repost(ctx context.Context, postID, userID string) (Post, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, false, fmt.Errorf("begin repost: %w", err)
	}
	defer tx.Rollback()
	var title, summary, categoryID, visibility, recipientID string
	err = tx.QueryRowContext(ctx, `SELECT p.title, p.summary, COALESCE(p.category_id, ''), p.visibility, p.author_id FROM posts p WHERE p.id = ? AND `+visiblePostPredicate, append([]any{postID}, visiblePostArgs(userID)...)...).Scan(&title, &summary, &categoryID, &visibility, &recipientID)
	if err == sql.ErrNoRows {
		return Post{}, false, platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	}
	if err != nil {
		return Post{}, false, fmt.Errorf("load repost target: %w", err)
	}
	if visibility == "followers" {
		title = "转发了一条仅关注者可见的内容"
		summary = ""
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT repost_post_id FROM reposts WHERE post_id = ? AND user_id = ?`, postID, userID).Scan(&existingID)
	if err == nil {
		tx.Rollback()
		post, err := s.Post(ctx, existingID, userID)
		return post, true, err
	}
	if err != sql.ErrNoRows {
		return Post{}, false, fmt.Errorf("check existing repost: %w", err)
	}
	repostID, err := platform.NewID("post")
	if err != nil {
		return Post{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	var category any
	if categoryID != "" {
		category = categoryID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO posts(id, author_id, kind, repost_of, title, summary, category_id, visibility, created_at, published_at)
		VALUES (?, ?, 'repost', ?, ?, ?, ?, ?, ?, ?)`, repostID, userID, postID, "转发："+title, summary, category, visibility, now, now); err != nil {
		return Post{}, false, fmt.Errorf("insert repost post: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO reposts(post_id, user_id, repost_post_id, created_at) VALUES (?, ?, ?, ?)`, postID, userID, repostID, now); err != nil {
		return Post{}, false, fmt.Errorf("insert repost relation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET repost_count = repost_count + 1 WHERE id = ?`, postID); err != nil {
		return Post{}, false, fmt.Errorf("increment repost count: %w", err)
	}
	if err := addNotification(ctx, tx, recipientID, userID, "repost", "post", postID, map[string]string{"repost_id": repostID}, s.now().UTC()); err != nil {
		return Post{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Post{}, false, fmt.Errorf("commit repost: %w", err)
	}
	post, err := s.Post(ctx, repostID, userID)
	return post, false, err
}

func (s *Service) UndoRepost(ctx context.Context, postID, userID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin undo repost: %w", err)
	}
	defer tx.Rollback()
	var repostID string
	err = tx.QueryRowContext(ctx, `SELECT repost_post_id FROM reposts WHERE post_id = ? AND user_id = ?`, postID, userID).Scan(&repostID)
	if err == sql.ErrNoRows {
		return false, tx.Commit()
	}
	if err != nil {
		return false, fmt.Errorf("load repost relation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM reposts WHERE post_id = ? AND user_id = ?`, postID, userID); err != nil {
		return false, fmt.Errorf("delete repost relation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET status = 'deleted' WHERE id = ? AND author_id = ?`, repostID, userID); err != nil {
		return false, fmt.Errorf("delete repost post: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET repost_count = CASE WHEN repost_count > 0 THEN repost_count - 1 ELSE 0 END WHERE id = ?`, postID); err != nil {
		return false, fmt.Errorf("decrement repost count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit undo repost: %w", err)
	}
	return true, nil
}

func ensureVisiblePost(ctx context.Context, tx *sql.Tx, postID, viewerID string) error {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM posts p WHERE p.id = ? AND `+visiblePostPredicate, append([]any{postID}, visiblePostArgs(viewerID)...)...).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	}
	if err != nil {
		return fmt.Errorf("check post: %w", err)
	}
	return nil
}
