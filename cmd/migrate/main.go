// Migration CLI applies every embedded migration and exits.
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"fanbbs.local/backend/internal/platform"
)

func main() {
	defaultAdapter := environment("FANBBS_DATABASE_DRIVER", "sqlite")
	defaultTarget := environment("FANBBS_DATABASE_URL", environment("FANBBS_DB", "fanbbs.db"))
	adapter := flag.String("driver", defaultAdapter, "database adapter: sqlite or postgres")
	target := flag.String("database-url", defaultTarget, "SQLite path or PostgreSQL connection URL")
	flag.Parse()
	db, err := platform.OpenDatabase(context.Background(), *adapter, *target)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("%s migrations applied", *adapter)
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
