// importlegacy rehearses mappings from a synthetic JSON fixture only. It opens
// the target database only after the flag, source marker and input schema are verified.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	dryRun := flag.Bool("dry-run", false, "execute the full import transaction and roll it back")
	flag.Parse()
	if !*synthetic || *inputPath == "" {
		log.Fatal("refusing import: pass --synthetic and --input")
	}
	file, err := os.Open(*inputPath)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	document, err := decodeDocument(file, *inputFormat)
	if err != nil {
		log.Fatal(err)
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
	importer := legacyimport.New(db)
	var report legacyimport.Report
	if *dryRun {
		report, err = importer.Rehearse(context.Background(), document)
	} else {
		report, err = importer.Import(context.Background(), document)
	}
	if err != nil {
		log.Fatal(err)
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
}

func decodeDocument(reader io.Reader, inputFormat string) (legacyimport.Document, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var document legacyimport.Document
	var err error
	switch inputFormat {
	case "normalized":
		err = decoder.Decode(&document)
	case "fanbbs-java":
		var snapshot legacyimport.AuthenticSnapshot
		if err = decoder.Decode(&snapshot); err == nil {
			document, err = legacyimport.AdaptAuthenticSnapshot(snapshot)
		}
	default:
		return legacyimport.Document{}, fmt.Errorf("refusing import: unknown synthetic input format %q", inputFormat)
	}
	if err != nil {
		return legacyimport.Document{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return legacyimport.Document{}, fmt.Errorf("refusing import: input must contain exactly one JSON document")
		}
		return legacyimport.Document{}, fmt.Errorf("refusing import: trailing input: %w", err)
	}
	return document, nil
}
