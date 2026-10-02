package community

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fanbbs.local/backend/internal/blob"
	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

var onePixelPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x04, 0x00, 0x00, 0x00, 0xb5, 0x1c, 0x0c,
	0x02, 0x00, 0x00, 0x00, 0x0b, 0x49, 0x44, 0x41,
	0x54, 0x78, 0xda, 0x63, 0x64, 0xf8, 0x0f, 0x00,
	0x01, 0x05, 0x01, 0x01, 0x27, 0x18, 0xe3, 0x66,
	0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
	0xae, 0x42, 0x60, 0x82,
}

type failingDeleteStore struct {
	blob.Store
	fail bool
}

func (store *failingDeleteStore) Delete(ctx context.Context, key string) error {
	if store.fail {
		return errors.New("temporary local delete failure")
	}
	return store.Store.Delete(ctx, key)
}

func newMediaService(t *testing.T) (*Service, string, *failingDeleteStore) {
	t.Helper()
	db, err := platform.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := identity.NewService(db).SeedDemo(context.Background()); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "blobs")
	local, err := blob.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	store := &failingDeleteStore{Store: local}
	return NewService(db, store), root, store
}

func TestCleanupExpiredMediaDeletesOnlyOrphansAndRetriesBlobFailure(t *testing.T) {
	ctx := context.Background()
	service, root, store := newMediaService(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return old }
	orphan, err := service.Upload(ctx, "usr_demo", "post", "orphan.png", "orphan", bytes.NewReader(onePixelPNG))
	if err != nil {
		t.Fatal(err)
	}
	attached, err := service.Upload(ctx, "usr_demo", "post", "attached.png", "attached", bytes.NewReader(onePixelPNG))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CreatePost(ctx, "usr_demo", "", CreatePostInput{Content: "这份媒体必须保持关联", MediaIDs: []string{attached.ID}}); err != nil {
		t.Fatal(err)
	}
	var orphanKey, attachedKey string
	if err := service.db.QueryRow(`SELECT object_key FROM media_assets WHERE id = ?`, orphan.ID).Scan(&orphanKey); err != nil {
		t.Fatal(err)
	}
	if err := service.db.QueryRow(`SELECT object_key FROM media_assets WHERE id = ?`, attached.ID).Scan(&attachedKey); err != nil {
		t.Fatal(err)
	}

	store.fail = true
	service.now = func() time.Time { return old.Add(48 * time.Hour) }
	result, err := service.CleanupExpiredMedia(ctx, old.Add(24*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expired != 1 || result.Deleted != 0 || result.Pending != 1 {
		t.Fatalf("unexpected failed-delete cleanup result: %#v", result)
	}
	var count int
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, orphan.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan metadata survived cleanup: count=%d err=%v", count, err)
	}
	if _, err := os.Stat(filepath.Join(root, orphanKey)); err != nil {
		t.Fatalf("failed blob delete should remain queued on disk: %v", err)
	}
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, attached.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("attached metadata was deleted: count=%d err=%v", count, err)
	}
	if _, err := os.Stat(filepath.Join(root, attachedKey)); err != nil {
		t.Fatalf("attached blob was deleted: %v", err)
	}

	store.fail = false
	result, err = service.CleanupExpiredMedia(ctx, old.Add(24*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expired != 0 || result.Deleted != 1 || result.Pending != 0 {
		t.Fatalf("unexpected retry result: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, orphanKey)); !os.IsNotExist(err) {
		t.Fatalf("queued orphan blob was not deleted: %v", err)
	}
}

func TestAbandonMediaIsOwnerScopedAndRefusesAttachedAssets(t *testing.T) {
	ctx := context.Background()
	service, root, _ := newMediaService(t)
	asset, err := service.Upload(ctx, "usr_demo", "post", "draft.png", "draft", bytes.NewReader(onePixelPNG))
	if err != nil {
		t.Fatal(err)
	}
	var objectKey string
	if err := service.db.QueryRow(`SELECT object_key FROM media_assets WHERE id = ?`, asset.ID).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	if err := service.AbandonMedia(ctx, "usr_rain", asset.ID); !isAppError(err, "media_not_found") {
		t.Fatalf("foreign owner received unexpected result: %v", err)
	}
	if err := service.AbandonMedia(ctx, "usr_demo", asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, objectKey)); !os.IsNotExist(err) {
		t.Fatalf("abandoned blob remains: %v", err)
	}

	attached, err := service.Upload(ctx, "usr_demo", "post", "used.png", "used", bytes.NewReader(onePixelPNG))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CreatePost(ctx, "usr_demo", "", CreatePostInput{Content: "这份媒体已经关联", MediaIDs: []string{attached.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := service.AbandonMedia(ctx, "usr_demo", attached.ID); !isAppError(err, "media_attached") {
		t.Fatalf("attached media received unexpected result: %v", err)
	}
	var count int
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, attached.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("attached media metadata changed: count=%d err=%v", count, err)
	}
}

func isAppError(err error, code string) bool {
	var appError *platform.AppError
	return errors.As(err, &appError) && appError.Code == code
}
