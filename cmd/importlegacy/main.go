// importlegacy rehearses mappings from a synthetic JSON fixture only. It opens
// the target database only after both the flag and document marker are verified.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"fanbbs.local/backend/internal/legacyimport"
	"fanbbs.local/backend/internal/platform"
)

func main() {
	inputPath := flag.String("input", "", "synthetic legacy JSON fixture")
	databasePath := flag.String("db", "legacy-import.db", "local SQLite target")
	synthetic := flag.Bool("synthetic", false, "required acknowledgement that this is generated test data")
	flag.Parse()
	if !*synthetic || *inputPath == "" {
		log.Fatal("refusing import: pass --synthetic and --input")
	}
	file, err := os.Open(*inputPath)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var document legacyimport.Document
	if err := decoder.Decode(&document); err != nil {
		log.Fatal(err)
	}
	if document.Source != "synthetic" {
		log.Fatal("refusing import: document source is not synthetic")
	}
	db, err := platform.OpenSQLite(context.Background(), *databasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	report, err := legacyimport.New(db).Import(context.Background(), document)
	if err != nil {
		log.Fatal(err)
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
}
