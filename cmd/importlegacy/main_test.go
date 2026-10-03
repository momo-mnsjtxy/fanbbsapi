package main

import (
	"strings"
	"testing"
)

func TestDecodeDocumentAcceptsExactlyOneStrictDocument(t *testing.T) {
	document, err := decodeDocument(strings.NewReader(`{"source":"synthetic","mapping_version":1}`), "normalized")
	if err != nil {
		t.Fatal(err)
	}
	if document.Source != "synthetic" || document.MappingVersion != 1 {
		t.Fatalf("unexpected document: %#v", document)
	}
}

func TestDecodeDocumentRejectsUnknownFieldsAndTrailingDocuments(t *testing.T) {
	for _, input := range []string{
		`{"source":"synthetic","mapping_version":1,"unexpected":true}`,
		`{"source":"synthetic","mapping_version":1}{"source":"synthetic","mapping_version":1}`,
	} {
		if _, err := decodeDocument(strings.NewReader(input), "normalized"); err == nil {
			t.Fatalf("accepted unsafe fixture input %q", input)
		}
	}
}
