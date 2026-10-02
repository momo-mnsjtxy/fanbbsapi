// Migration CLI applies every embedded migration and exits.
package main

import (
	"context"
	"log"
	"os"

	"fanbbs.local/backend/internal/platform"
)

func main() {
	path := os.Getenv("FANBBS_DB")
	if path == "" {
		path = "fanbbs.db"
	}
	db, err := platform.OpenSQLite(context.Background(), path)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("migrations applied to %s", path)
}
