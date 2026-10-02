// Post mutation uses the existing version as an optimistic concurrency token.
// Clients must send If-Match so stale editors never silently overwrite content.
package community

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

type UpdatePostInput struct {
	Title      *string   `json:"title"`
	Summary    *string   `json:"summary"`
	Body       *string   `json:"body"`
	Content    *string   `json:"content"`
	CategoryID *string   `json:"category_id"`
	TagIDs     *[]string `json:"tag_ids"`
	Visibility *string   `json:"visibility"`
}

func (s *Service) UpdatePost(ctx context.Context, postID, authorID string, expectedVersion int, input UpdatePostInput) (Post, error) {
	if input.Title == nil && input.Summary == nil && input.Body == nil && input.Content == nil && input.CategoryID == nil && input.TagIDs == nil && input.Visibility == nil {
		return Post{}, platform.Validation(map[string][]string{"post": {"至少提供一个需要更新的字段"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, fmt.Errorf("begin post update: %w", err)
	}
	defer tx.Rollback()
	var ownerID, kind, title, summary, body, categoryID, visibility string
	var version int
	err = tx.QueryRowContext(ctx, `
		SELECT author_id, kind, title, summary, body, COALESCE(category_id, ''), visibility, version
		FROM posts WHERE id = ? AND status = 'published'`, postID).Scan(&ownerID, &kind, &title, &summary, &body, &categoryID, &visibility, &version)
	if err == sql.ErrNoRows {
		return Post{}, platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	}
	if err != nil {
		return Post{}, fmt.Errorf("load post for update: %w", err)
	}
	if ownerID != authorID {
		return Post{}, platform.Problem(http.StatusForbidden, "post_edit_forbidden", "只能编辑自己的文章")
	}
	if kind == "repost" {
		return Post{}, platform.Problem(http.StatusConflict, "repost_not_editable", "转发内容不能编辑")
	}
	if version != expectedVersion {
		return Post{}, platform.Problem(http.StatusConflict, "version_conflict", "文章已被其他操作更新，请刷新后重试")
	}
	tagIDs := []string{}
	tagRows, err := tx.QueryContext(ctx, `SELECT tag_id FROM post_tags WHERE post_id = ? ORDER BY tag_id`, postID)
	if err != nil {
		return Post{}, fmt.Errorf("load post revision tags: %w", err)
	}
	for tagRows.Next() {
		var tagID string
		if err := tagRows.Scan(&tagID); err != nil {
			tagRows.Close()
			return Post{}, fmt.Errorf("scan post revision tag: %w", err)
		}
		tagIDs = append(tagIDs, tagID)
	}
	if err := tagRows.Close(); err != nil {
		return Post{}, fmt.Errorf("close post revision tags: %w", err)
	}
	sort.Strings(tagIDs)
	encodedTagIDs, err := json.Marshal(tagIDs)
	if err != nil {
		return Post{}, fmt.Errorf("encode post revision tags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO post_revisions(post_id, version, title, summary, body, category_id, tag_ids, visibility, edited_by, created_at)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)`, postID, version, title, summary, body, categoryID,
		string(encodedTagIDs), visibility, authorID, s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return Post{}, fmt.Errorf("store post revision: %w", err)
	}
	if input.Title != nil {
		title = strings.TrimSpace(*input.Title)
	}
	if input.Summary != nil {
		summary = strings.TrimSpace(*input.Summary)
	}
	selectedBody := input.Body
	if selectedBody == nil {
		selectedBody = input.Content
	}
	if selectedBody != nil {
		body = strings.TrimSpace(*selectedBody)
	}
	if input.CategoryID != nil {
		categoryID = strings.TrimSpace(*input.CategoryID)
	}
	if input.Visibility != nil {
		visibility = strings.TrimSpace(*input.Visibility)
	}
	fields := map[string][]string{}
	if len([]rune(title)) < 3 || len([]rune(title)) > 120 {
		fields["title"] = []string{"标题长度必须为 3 到 120 个字符"}
	}
	if len([]rune(body)) < 1 || len([]rune(body)) > 50000 {
		fields["body"] = []string{"正文长度必须为 1 到 50000 个字符"}
	}
	if len([]rune(summary)) > 500 {
		fields["summary"] = []string{"摘要不能超过 500 个字符"}
	}
	if visibility != "public" && visibility != "followers" {
		fields["visibility"] = []string{"可见性必须是 public 或 followers"}
	}
	if len(fields) > 0 {
		return Post{}, platform.Validation(fields)
	}
	var category any
	if categoryID != "" {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM categories WHERE id = ?`, categoryID).Scan(&exists); err == sql.ErrNoRows {
			return Post{}, platform.Validation(map[string][]string{"category_id": {"分类不存在"}})
		} else if err != nil {
			return Post{}, fmt.Errorf("check updated category: %w", err)
		}
		category = categoryID
	}
	if input.TagIDs != nil {
		if len(*input.TagIDs) > 10 {
			return Post{}, platform.Validation(map[string][]string{"tag_ids": {"每篇文章最多选择 10 个标签"}})
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM post_tags WHERE post_id = ?`, postID); err != nil {
			return Post{}, fmt.Errorf("replace post tags: %w", err)
		}
		seen := map[string]bool{}
		for _, tagID := range *input.TagIDs {
			tagID = strings.TrimSpace(tagID)
			if tagID == "" || seen[tagID] {
				continue
			}
			seen[tagID] = true
			result, err := tx.ExecContext(ctx, `INSERT INTO post_tags(post_id, tag_id) SELECT ?, id FROM tags WHERE id = ?`, postID, tagID)
			if err != nil {
				return Post{}, fmt.Errorf("attach updated tag: %w", err)
			}
			changed, _ := result.RowsAffected()
			if changed != 1 {
				return Post{}, platform.Validation(map[string][]string{"tag_ids": {"标签不存在"}})
			}
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE posts SET title = ?, summary = ?, body = ?, category_id = ?, visibility = ?, version = version + 1
		WHERE id = ? AND author_id = ? AND version = ? AND status = 'published'`, title, summary, body, category, visibility, postID, authorID, expectedVersion)
	if err != nil {
		return Post{}, fmt.Errorf("update post: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return Post{}, platform.Problem(http.StatusConflict, "version_conflict", "文章已被其他操作更新，请刷新后重试")
	}
	if err := tx.Commit(); err != nil {
		return Post{}, fmt.Errorf("commit post update: %w", err)
	}
	return s.Post(ctx, postID, authorID)
}

func (s *Service) DeletePost(ctx context.Context, postID, authorID string, expectedVersion int) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin post delete: %w", err)
	}
	defer tx.Rollback()
	var ownerID, kind, repostOf string
	var version int
	err = tx.QueryRowContext(ctx, `SELECT author_id, kind, COALESCE(repost_of, ''), version FROM posts WHERE id = ? AND status = 'published'`, postID).Scan(&ownerID, &kind, &repostOf, &version)
	if err == sql.ErrNoRows {
		return 0, platform.Problem(http.StatusNotFound, "post_not_found", "文章不存在")
	}
	if err != nil {
		return 0, fmt.Errorf("load post for delete: %w", err)
	}
	if ownerID != authorID {
		return 0, platform.Problem(http.StatusForbidden, "post_delete_forbidden", "只能删除自己的文章")
	}
	if version != expectedVersion {
		return 0, platform.Problem(http.StatusConflict, "version_conflict", "文章已被其他操作更新，请刷新后重试")
	}
	if kind == "repost" && repostOf != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM reposts WHERE repost_post_id = ? AND user_id = ?`, postID, authorID); err != nil {
			return 0, fmt.Errorf("delete repost relation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE posts SET repost_count = CASE WHEN repost_count > 0 THEN repost_count - 1 ELSE 0 END WHERE id = ?`, repostOf); err != nil {
			return 0, fmt.Errorf("decrement original repost count: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE posts SET status = 'deleted', version = version + 1 WHERE id = ? AND author_id = ? AND version = ? AND status = 'published'`, postID, authorID, expectedVersion)
	if err != nil {
		return 0, fmt.Errorf("delete post: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return 0, platform.Problem(http.StatusConflict, "version_conflict", "文章已被其他操作更新，请刷新后重试")
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit post delete: %w", err)
	}
	return version + 1, nil
}

func (s *Service) updatePostHTTP(w http.ResponseWriter, r *http.Request) {
	expected, err := ifMatchVersion(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var input UpdatePostInput
	if decodeErr := platform.DecodeJSON(w, r, &input); decodeErr != nil {
		platform.WriteError(w, r, decodeErr)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	post, serviceErr := s.UpdatePost(r.Context(), postID(r), current.ID, expected, input)
	if serviceErr != nil {
		platform.WriteError(w, r, serviceErr)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, post.Version))
	platform.WriteData(w, r, http.StatusOK, post)
}

func (s *Service) deletePostHTTP(w http.ResponseWriter, r *http.Request) {
	expected, err := ifMatchVersion(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	version, err := s.DeletePost(r.Context(), postID(r), current.ID, expected)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"deleted": true, "version": version})
}
