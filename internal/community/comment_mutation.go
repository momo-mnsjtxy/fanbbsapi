// Comment owners can edit or soft-delete with If-Match. Comment likes are
// explicit idempotent relations and update the cached count transactionally.
package community

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func (s *Service) UpdateComment(ctx context.Context, postID, commentID, userID, body string, expectedVersion int) (Comment, error) {
	body = strings.TrimSpace(body)
	if len([]rune(body)) < 1 || len([]rune(body)) > 2000 {
		return Comment{}, platform.Validation(map[string][]string{"body": {"评论长度必须为 1 到 2000 个字符"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Comment{}, fmt.Errorf("begin comment update: %w", err)
	}
	defer tx.Rollback()
	var ownerID string
	var version int
	err = tx.QueryRowContext(ctx, `
		SELECT c.author_id, c.version FROM comments c JOIN posts p ON p.id = c.post_id
		WHERE c.id = ? AND c.post_id = ? AND c.status = 'published' AND `+visiblePostPredicate,
		append([]any{commentID, postID}, visiblePostArgs(userID)...)...).Scan(&ownerID, &version)
	if err == sql.ErrNoRows {
		return Comment{}, platform.Problem(http.StatusNotFound, "comment_not_found", "评论不存在")
	}
	if err != nil {
		return Comment{}, fmt.Errorf("load comment for update: %w", err)
	}
	if ownerID != userID {
		return Comment{}, platform.Problem(http.StatusForbidden, "comment_edit_forbidden", "只能编辑自己的评论")
	}
	if version != expectedVersion {
		return Comment{}, platform.Problem(http.StatusConflict, "version_conflict", "评论已被其他操作更新，请刷新后重试")
	}
	result, err := tx.ExecContext(ctx, `UPDATE comments SET body = ?, version = version + 1 WHERE id = ? AND author_id = ? AND version = ? AND status = 'published'`, body, commentID, userID, expectedVersion)
	if err != nil {
		return Comment{}, fmt.Errorf("update comment: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return Comment{}, platform.Problem(http.StatusConflict, "version_conflict", "评论已被其他操作更新，请刷新后重试")
	}
	if err := tx.Commit(); err != nil {
		return Comment{}, fmt.Errorf("commit comment update: %w", err)
	}
	return s.comment(ctx, commentID)
}

func (s *Service) DeleteComment(ctx context.Context, postID, commentID, userID string, expectedVersion int) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin comment delete: %w", err)
	}
	defer tx.Rollback()
	var ownerID string
	var version int
	err = tx.QueryRowContext(ctx, `
		SELECT c.author_id, c.version FROM comments c JOIN posts p ON p.id = c.post_id
		WHERE c.id = ? AND c.post_id = ? AND c.status = 'published' AND `+visiblePostPredicate,
		append([]any{commentID, postID}, visiblePostArgs(userID)...)...).Scan(&ownerID, &version)
	if err == sql.ErrNoRows {
		return 0, platform.Problem(http.StatusNotFound, "comment_not_found", "评论不存在")
	}
	if err != nil {
		return 0, fmt.Errorf("load comment for delete: %w", err)
	}
	if ownerID != userID {
		return 0, platform.Problem(http.StatusForbidden, "comment_delete_forbidden", "只能删除自己的评论")
	}
	if version != expectedVersion {
		return 0, platform.Problem(http.StatusConflict, "version_conflict", "评论已被其他操作更新，请刷新后重试")
	}
	result, err := tx.ExecContext(ctx, `UPDATE comments SET status = 'deleted', version = version + 1 WHERE id = ? AND author_id = ? AND version = ? AND status = 'published'`, commentID, userID, expectedVersion)
	if err != nil {
		return 0, fmt.Errorf("delete comment: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return 0, platform.Problem(http.StatusConflict, "version_conflict", "评论已被其他操作更新，请刷新后重试")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE posts SET comment_count = CASE WHEN comment_count > 0 THEN comment_count - 1 ELSE 0 END WHERE id = ?`, postID); err != nil {
		return 0, fmt.Errorf("decrement comment count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit comment delete: %w", err)
	}
	return version + 1, nil
}

func (s *Service) LikeComment(ctx context.Context, postID, commentID, userID string) (bool, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("begin comment like: %w", err)
	}
	defer tx.Rollback()
	var recipientID string
	err = tx.QueryRowContext(ctx, `
		SELECT c.author_id FROM comments c JOIN posts p ON p.id = c.post_id
		WHERE c.id = ? AND c.post_id = ? AND c.status = 'published' AND `+visiblePostPredicate,
		append([]any{commentID, postID}, visiblePostArgs(userID)...)...).Scan(&recipientID)
	if err == sql.ErrNoRows {
		return false, 0, platform.Problem(http.StatusNotFound, "comment_not_found", "评论不存在")
	}
	if err != nil {
		return false, 0, fmt.Errorf("load liked comment: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO comment_reactions(comment_id, user_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, commentID, userID, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, 0, fmt.Errorf("create comment like: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE comments SET like_count = like_count + 1 WHERE id = ?`, commentID); err != nil {
			return false, 0, fmt.Errorf("increment comment like count: %w", err)
		}
		if err := addNotification(ctx, tx, recipientID, userID, "like", "comment", commentID, map[string]string{"post_id": postID}, s.now().UTC()); err != nil {
			return false, 0, err
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT like_count FROM comments WHERE id = ?`, commentID).Scan(&count); err != nil {
		return false, 0, fmt.Errorf("read comment like count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("commit comment like: %w", err)
	}
	return changed == 1, count, nil
}

func (s *Service) UnlikeComment(ctx context.Context, postID, commentID, userID string) (bool, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("begin comment unlike: %w", err)
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM comments c JOIN posts p ON p.id = c.post_id
		WHERE c.id = ? AND c.post_id = ? AND c.status = 'published' AND `+visiblePostPredicate,
		append([]any{commentID, postID}, visiblePostArgs(userID)...)...).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, 0, platform.Problem(http.StatusNotFound, "comment_not_found", "评论不存在")
	}
	if err != nil {
		return false, 0, fmt.Errorf("load unliked comment: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM comment_reactions WHERE comment_id = ? AND user_id = ?`, commentID, userID)
	if err != nil {
		return false, 0, fmt.Errorf("delete comment like: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE comments SET like_count = CASE WHEN like_count > 0 THEN like_count - 1 ELSE 0 END WHERE id = ?`, commentID); err != nil {
			return false, 0, fmt.Errorf("decrement comment like count: %w", err)
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT like_count FROM comments WHERE id = ?`, commentID).Scan(&count); err != nil {
		return false, 0, fmt.Errorf("read comment like count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("commit comment unlike: %w", err)
	}
	return changed == 1, count, nil
}

func (s *Service) updateCommentHTTP(w http.ResponseWriter, r *http.Request) {
	expected, err := ifMatchVersion(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var input struct {
		Body    string `json:"body"`
		Content string `json:"content"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if input.Body == "" {
		input.Body = input.Content
	}
	current, _ := identity.UserFromContext(r.Context())
	comment, err := s.UpdateComment(r.Context(), postID(r), commentID(r), current.ID, input.Body, expected)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, comment.Version))
	platform.WriteData(w, r, http.StatusOK, comment)
}

func (s *Service) deleteCommentHTTP(w http.ResponseWriter, r *http.Request) {
	expected, err := ifMatchVersion(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	version, err := s.DeleteComment(r.Context(), postID(r), commentID(r), current.ID, expected)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"deleted": true, "version": version})
}

func (s *Service) likeCommentHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, count, err := s.LikeComment(r.Context(), postID(r), commentID(r), current.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"liked": true, "changed": changed, "like_count": count})
}

func (s *Service) unlikeCommentHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	changed, count, err := s.UnlikeComment(r.Context(), postID(r), commentID(r), current.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"liked": false, "changed": changed, "like_count": count})
}

var _ = time.RFC3339Nano
