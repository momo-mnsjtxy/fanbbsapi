// The worker runs one bounded local orphan-media cleanup pass. Scheduling stays
// with the deployment platform, keeping this process deterministic and easy to
// operate as a cron job without enabling any external integration.
package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"fanbbs.local/backend/internal/blob"
	"fanbbs.local/backend/internal/community"
	"fanbbs.local/backend/internal/platform"
)

func main() {
	ctx := context.Background()
	adapter := env("FANBBS_DATABASE_DRIVER", "sqlite")
	databaseTarget := env("FANBBS_DATABASE_URL", env("FANBBS_DB", "fanbbs.db"))
	db, err := platform.OpenDatabase(ctx, adapter, databaseTarget)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	localBlobs, err := blob.NewLocal(env("FANBBS_BLOB_DIR", "data/blobs"))
	if err != nil {
		log.Fatal(err)
	}
	retention, err := time.ParseDuration(env("FANBBS_MEDIA_RETENTION", "24h"))
	if err != nil || retention <= 0 {
		log.Fatal("FANBBS_MEDIA_RETENTION must be a positive duration")
	}
	limit, err := strconv.Atoi(env("FANBBS_MEDIA_CLEANUP_LIMIT", "100"))
	if err != nil || limit < 1 || limit > 1000 {
		log.Fatal("FANBBS_MEDIA_CLEANUP_LIMIT must be between 1 and 1000")
	}
	result, err := community.NewService(db, localBlobs).CleanupExpiredMedia(ctx, time.Now().UTC().Add(-retention), limit)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("media cleanup expired=%d deleted=%d pending=%d", result.Expired, result.Deleted, result.Pending)
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
