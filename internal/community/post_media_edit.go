package community

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"fanbbs.local/backend/internal/platform"
)

// replacePostMedia runs inside the versioned post-edit transaction. Detaching
// does not destroy a blob; only the existing explicit cleanup lifecycle does.
func (s *Service) replacePostMedia(ctx context.Context, tx *sql.Tx, postID, ownerID string, ids []string) error {
	if len(ids) > 8 {
		return platform.Validation(map[string][]string{"media_ids": {"每篇文章最多关联 8 个媒体文件"}})
	}
	next := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return platform.Validation(map[string][]string{"media_ids": {"附件 ID 不能为空或重复"}})
		}
		seen[id] = true
		next = append(next, id)
	}
	old := []string{}
	rows, err := tx.QueryContext(ctx, `SELECT asset_id FROM post_media WHERE post_id=? ORDER BY position`, postID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		old = append(old, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	// Validate before unlinking. The unique asset constraint also prevents two
	// posts from claiming the same upload across concurrent transactions.
	for _, id := range next {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM media_assets m WHERE m.id=? AND m.owner_id=? AND m.purpose='post' AND m.status='ready' AND NOT EXISTS(SELECT 1 FROM post_media pm WHERE pm.asset_id=m.id AND pm.post_id<>?)`, id, ownerID, postID).Scan(&exists)
		if err == sql.ErrNoRows {
			return platform.Validation(map[string][]string{"media_ids": {"附件不可用、无权使用或已关联到其他帖子"}})
		}
		if err != nil {
			return fmt.Errorf("validate edited media: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id=?`, postID); err != nil {
		return err
	}
	for position, id := range next {
		if _, err := tx.ExecContext(ctx, `INSERT INTO post_media(post_id,asset_id,position) VALUES(?,?,?)`, postID, id, position); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return platform.Validation(map[string][]string{"media_ids": {"附件已被其他帖子使用，请刷新后重试"}})
			}
			return err
		}
	}
	return insertAudit(ctx, tx, ownerID, "post_media_edit", "post", postID, map[string]any{"media_ids": old}, map[string]any{"media_ids": next}, "作者编辑稿件附件", platform.RequestID(ctx), s.now().UTC())
}
