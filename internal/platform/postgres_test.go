package platform

import "testing"

func TestPostgresQueryRebindsOnlyParameters(t *testing.T) {
	input := "SELECT ?, '?', \"?\", $$?$$, $tag$?$tag$ -- ?\n/* ? */ WHERE value = ?"
	want := "SELECT $1, '?', \"?\", $$?$$, $tag$?$tag$ -- ?\n/* ? */ WHERE value = $2"
	if got := postgresQuery(input); got != want {
		t.Fatalf("unexpected rebind:\n got: %s\nwant: %s", got, want)
	}
}

func TestPostgresQueryTranslatesSharedReadModel(t *testing.T) {
	input := `SELECT group_concat(tag.id || char(31), char(30)), u.rowid FROM users u WHERE u.id = ?`
	want := `SELECT string_agg(tag.id || chr(31), chr(30)), u.sequence FROM users u WHERE u.id = $1`
	if got := postgresQuery(input); got != want {
		t.Fatalf("unexpected dialect translation: %s", got)
	}
}
