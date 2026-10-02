// FanBBS local API entrypoint.
// Run: FANBBS_DB=fanbbs.db /tmp/go1.26.5/bin/go run ./cmd/api
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fanbbs.local/backend/internal/app"
	"fanbbs.local/backend/internal/blob"
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
	application := app.NewWithBlob(db, localBlobs)
	seedDefault := "false"
	if adapter == "sqlite" || adapter == "sqlite3" {
		seedDefault = "true"
	}
	if env("FANBBS_SEED_DEMO", seedDefault) == "true" {
		if err := application.Identity.SeedDemo(ctx); err != nil {
			log.Fatal(err)
		}
	}

	server := &http.Server{
		Addr:              env("FANBBS_ADDR", ":8080"),
		Handler:           application.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Printf("FanBBS API listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-stopped
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
