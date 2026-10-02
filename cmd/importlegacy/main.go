// importlegacy rehearses mappings from a synthetic JSON fixture only. It opens
// the target database only after the flag, source marker and input schema are verified.
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
	inputFormat := flag.String("format", "normalized", "synthetic input layout: normalized or fanbbs-java")
	databasePath := flag.String("db", "legacy-import.db", "local SQLite target")
	adapter := flag.String("driver", "sqlite", "target adapter: sqlite or postgres")
	databaseURL := flag.String("database-url", "", "PostgreSQL target URL (required with --driver postgres)")
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
	switch *inputFormat {
	case "normalized":
		if err := decoder.Decode(&document); err != nil {
			log.Fatal(err)
		}
	case "fanbbs-java":
		var snapshot legacyimport.AuthenticSnapshot
		if err := decoder.Decode(&snapshot); err != nil {
			log.Fatal(err)
		}
		document, err = legacyimport.AdaptAuthenticSnapshot(snapshot)
		if err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("refusing import: unknown synthetic input format %q", *inputFormat)
	}
	if document.Source != "synthetic" || document.MappingVersion != 1 {
		log.Fatal("refusing import: document must declare source synthetic and mapping_version 1")
	}
	target := *databasePath
	if *databaseURL != "" {
		target = *databaseURL
	}
	db, err := platform.OpenDatabase(context.Background(), *adapter, target)
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
