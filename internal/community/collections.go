package community

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Collection is a named curated list with an explicit privacy boundary.
// Bookmarks intentionally remain a separate, owner-only feature.
type Collection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	Version     int    `json:"version"`
	ItemCount   int    `json:"item_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	Owner       Author `json:"owner"`
}

type CollectionDetail struct {
	Collection Collection `json:"collection"`
	Posts      []Post     `json:"posts"`
	NextCursor string     `json:"next_cursor"`
}

type CollectionInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
}

const collectionSelect = `
	SELECT c.id,c.name,c.description,c.visibility,c.version,c.created_at,c.updated_at,
	       u.id,u.handle,u.display_name,u.avatar_url,
	       (SELECT COUNT(*) FROM collection_items item WHERE item.collection_id=c.id)
	FROM collections c JOIN users u ON u.id=c.owner_id`

func scanCollection(row scanner) (Collection, error) {
	var item Collection
	err := row.Scan(&item.ID, &item.Name, &item.Description, &item.Visibility, &item.Version,
		&item.CreatedAt, &item.UpdatedAt, &item.Owner.ID, &item.Owner.Handle,
		&item.Owner.DisplayName, &item.Owner.AvatarURL, &item.ItemCount)
	item.Owner.Name = item.Owner.DisplayName
	return item, err
}

func validateCollectionInput(input *CollectionInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Visibility = strings.TrimSpace(input.Visibility)
	if input.Visibility == "" {
		input.Visibility = "private"
	}
	fields := map[string][]string{}
	if length := len([]rune(input.Name)); length < 1 || length > 80 {
		fields["name"] = []string{"名称长度必须为 1 到 80 个字符"}
	}
	if len([]rune(input.Description)) > 500 {
		fields["description"] = []string{"描述不能超过 500 个字符"}
	}
	if input.Visibility != "private" && input.Visibility != "public" {
		fields["visibility"] = []string{"可见性必须是 private 或 public"}
	}
	if len(fields) > 0 {
		return platform.Validation(fields)
	}
	return nil
}

func (s *Service) CreateCollection(ctx context.Context, ownerID string, input CollectionInput) (Collection, error) {
	if err := validateCollectionInput(&input); err != nil {
		return Collection{}, err
	}
	id, err := platform.NewID("col")
	if err != nil {
		return Collection{}, err
	}
	stamp := s.now().UTC().Format(timeRFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO collections(id,owner_id,name,description,visibility,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		id, ownerID, input.Name, input.Description, input.Visibility, stamp, stamp); err != nil {
		return Collection{}, fmt.Errorf("create collection: %w", err)
	}
	return scanCollection(s.db.QueryRowContext(ctx, collectionSelect+` WHERE c.id=? AND c.owner_id=?`, id, ownerID))
}

// timeRFC3339Nano is local to this file to keep collection timestamps
// consistent with the rest of the service without exposing another helper.
const timeRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

func (s *Service) OwnerCollections(ctx context.Context, ownerID string, offset, limit int) ([]Collection, string, error) {
	rows, err := s.db.QueryContext(ctx, collectionSelect+` WHERE c.owner_id=? ORDER BY c.updated_at DESC,c.id DESC LIMIT ? OFFSET ?`, ownerID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list owner collections: %w", err)
	}
	defer rows.Close()
	items := make([]Collection, 0, limit+1)
	for rows.Next() {
		item, err := scanCollection(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return items, next, nil
}

func (s *Service) PublicCollections(ctx context.Context, ownerID, viewerID string, offset, limit int) ([]Collection, string, error) {
	if _, err := s.PublicProfile(ctx, ownerID, viewerID); err != nil {
		return nil, "", err
	}
	rows, err := s.db.QueryContext(ctx, collectionSelect+` WHERE c.owner_id=? AND c.visibility='public' ORDER BY c.updated_at DESC,c.id DESC LIMIT ? OFFSET ?`, ownerID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list public collections: %w", err)
	}
	defer rows.Close()
	items := make([]Collection, 0, limit+1)
	for rows.Next() {
		item, err := scanCollection(rows)
		if err != nil {
			return nil, "", err
		}
		// Total membership can reveal hidden followers-only posts. Public list
		// responses leave the exact visible count to the detail endpoint.
		item.ItemCount = 0
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return items, next, nil
}

func (s *Service) UpdateCollection(ctx context.Context, collectionID, ownerID string, expectedVersion int, input CollectionInput) (Collection, error) {
	if err := validateCollectionInput(&input); err != nil {
		return Collection{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE collections SET name=?,description=?,visibility=?,version=version+1,updated_at=? WHERE id=? AND owner_id=? AND version=?`,
		input.Name, input.Description, input.Visibility, s.now().UTC().Format(timeRFC3339Nano), collectionID, ownerID, expectedVersion)
	if err != nil {
		return Collection{}, fmt.Errorf("update collection: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		var current int
		if err := s.db.QueryRowContext(ctx, `SELECT version FROM collections WHERE id=? AND owner_id=?`, collectionID, ownerID).Scan(&current); err == sql.ErrNoRows {
			return Collection{}, platform.Problem(http.StatusNotFound, "collection_not_found", "收藏集不存在")
		} else if err != nil {
			return Collection{}, err
		}
		return Collection{}, platform.Problem(http.StatusConflict, "version_conflict", "收藏集已被其他操作更新，请刷新后重试")
	}
	return scanCollection(s.db.QueryRowContext(ctx, collectionSelect+` WHERE c.id=? AND c.owner_id=?`, collectionID, ownerID))
}

func (s *Service) DeleteCollection(ctx context.Context, collectionID, ownerID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM collections WHERE id=? AND owner_id=?`, collectionID, ownerID)
	if err != nil {
		return false, fmt.Errorf("delete collection: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return false, platform.Problem(http.StatusNotFound, "collection_not_found", "收藏集不存在")
	}
	return true, nil
}

func (s *Service) SetCollectionPost(ctx context.Context, collectionID, postID, ownerID string, add bool) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM collections WHERE id=? AND owner_id=?`, collectionID, ownerID).Scan(&exists); err == sql.ErrNoRows {
		return false, platform.Problem(http.StatusNotFound, "collection_not_found", "收藏集不存在")
	} else if err != nil {
		return false, err
	}
	var result sql.Result
	if add {
		if err := ensureVisiblePost(ctx, tx, postID, ownerID); err != nil {
			return false, err
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO collection_items(collection_id,post_id,added_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, collectionID, postID, s.now().UTC().Format(timeRFC3339Nano))
	} else {
		result, err = tx.ExecContext(ctx, `DELETE FROM collection_items WHERE collection_id=? AND post_id=?`, collectionID, postID)
	}
	if err != nil {
		return false, fmt.Errorf("change collection item: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE collections SET updated_at=? WHERE id=?`, s.now().UTC().Format(timeRFC3339Nano), collectionID); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed == 1, nil
}

func (s *Service) Collection(ctx context.Context, collectionID, viewerID string, offset, limit int) (CollectionDetail, string, error) {
	item, err := scanCollection(s.db.QueryRowContext(ctx, collectionSelect+` WHERE c.id=? AND (c.owner_id=? OR c.visibility='public')`, collectionID, viewerID))
	if err == sql.ErrNoRows {
		return CollectionDetail{}, "", platform.Problem(http.StatusNotFound, "collection_not_found", "收藏集不存在")
	}
	if err != nil {
		return CollectionDetail{}, "", fmt.Errorf("load collection: %w", err)
	}
	if item.Owner.ID != viewerID {
		if _, err := s.PublicProfile(ctx, item.Owner.ID, viewerID); err != nil {
			return CollectionDetail{}, "", err
		}
	}
	args := []any{viewerID, viewerID, viewerID, collectionID}
	args = append(args, visiblePostArgs(viewerID)...)
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, postSelect+`
		JOIN collection_items selected_collection_item ON selected_collection_item.post_id=p.id
		WHERE selected_collection_item.collection_id=? AND `+visiblePostPredicate+`
		ORDER BY selected_collection_item.added_at DESC,p.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return CollectionDetail{}, "", fmt.Errorf("list collection posts: %w", err)
	}
	defer rows.Close()
	posts := make([]Post, 0, limit+1)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return CollectionDetail{}, "", err
		}
		post.Body = ""
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return CollectionDetail{}, "", err
	}
	next := ""
	if len(posts) > limit {
		posts = posts[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	// The owner's collectionSelect count is the exact curated membership and
	// must not vary with the current page. Public readers instead get an exact
	// count of posts visible to that viewer, without leaking restricted items.
	if item.Owner.ID != viewerID {
		countArgs := []any{collectionID}
		countArgs = append(countArgs, visiblePostArgs(viewerID)...)
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collection_items selected_collection_item JOIN posts p ON p.id=selected_collection_item.post_id WHERE selected_collection_item.collection_id=? AND `+visiblePostPredicate, countArgs...).Scan(&item.ItemCount); err != nil {
			return CollectionDetail{}, "", fmt.Errorf("count visible collection posts: %w", err)
		}
	}
	return CollectionDetail{Collection: item, Posts: posts, NextCursor: next}, next, nil
}

func (s *Service) createCollectionHTTP(w http.ResponseWriter, r *http.Request) {
	var input CollectionInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	item, err := s.CreateCollection(r.Context(), current.ID, input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	platform.WriteData(w, r, http.StatusCreated, item)
}

func (s *Service) ownerCollectionsHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	items, next, err := s.OwnerCollections(r.Context(), current.ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) publicCollectionsHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	viewer, _ := identity.UserFromContext(r.Context())
	items, next, err := s.PublicCollections(r.Context(), userID(r), viewer.ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) collectionHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	viewer, _ := identity.UserFromContext(r.Context())
	item, _, err := s.Collection(r.Context(), chi.URLParam(r, "collectionID"), viewer.ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) updateCollectionHTTP(w http.ResponseWriter, r *http.Request) {
	expected, err := ifMatchVersion(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var input CollectionInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	item, err := s.UpdateCollection(r.Context(), chi.URLParam(r, "collectionID"), current.ID, expected, input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) deleteCollectionHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	_, err := s.DeleteCollection(r.Context(), chi.URLParam(r, "collectionID"), current.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Service) collectionPostHTTP(add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		current, _ := identity.UserFromContext(r.Context())
		changed, err := s.SetCollectionPost(r.Context(), chi.URLParam(r, "collectionID"), postID(r), current.ID, add)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteData(w, r, http.StatusOK, map[string]bool{"included": add, "changed": changed})
	}
}
