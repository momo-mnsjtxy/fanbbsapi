package legacyimport_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"fanbbs.local/backend/internal/legacyimport"
	"fanbbs.local/backend/internal/platform"
)

func openDB(t *testing.T, name string) *legacyimport.Importer {
	t.Helper()
	db := openRawDB(t, name)
	return legacyimport.New(db)
}

func openRawDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := platform.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func fixture() legacyimport.Document {
	return legacyimport.Document{
		Source: "synthetic", MappingVersion: 1,
		Users: []legacyimport.LegacyUser{
			{ID: "u2", Handle: "legacy_bob", Email: "bob@example.test", DisplayName: "旧用户乙", Role: "editor", Status: "active", CreatedAt: "2020-01-02T00:00:00Z"},
			{ID: "u1", Handle: "legacy_alice", Email: "alice@example.test", DisplayName: "旧用户甲", Role: "contributor", Status: "active", CreatedAt: "2020-01-01T00:00:00Z"},
		},
		Taxonomy: []legacyimport.LegacyTaxonomy{
			{ID: "t1", Kind: "tag", Slug: "legacy", Name: "旧标签"},
			{ID: "c1", Kind: "category", Slug: "legacy-life", Name: "旧生活"},
		},
		Posts: []legacyimport.LegacyPost{
			{ID: "p1", AuthorID: "u1", Kind: "article", Title: "可导入文章", Body: "这是一篇合成旧数据文章。", CategoryID: "c1", Status: "publish", CreatedAt: "2020-02-01T00:00:00Z"},
			{ID: "p2", AuthorID: "u1", Kind: "article", Title: "付费文章", Body: "不能导入", Status: "publish", Paid: true},
			{ID: "p3", AuthorID: "missing", Kind: "article", Title: "孤儿文章", Body: "不能导入", Status: "publish"},
		},
		Comments: []legacyimport.LegacyComment{
			{ID: "m2", PostID: "p1", AuthorID: "u2", ParentID: "m1", Body: "合成回复", CreatedAt: "2020-02-03T00:00:00Z"},
			{ID: "m1", PostID: "p1", AuthorID: "u1", Body: "合成评论", CreatedAt: "2020-02-02T00:00:00Z"},
			{ID: "m3", PostID: "missing", AuthorID: "u1", Body: "孤儿评论"},
		},
	}
}

func TestSyntheticImportIsDeterministicAndQuarantines(t *testing.T) {
	ctx := context.Background()
	firstImporter := openDB(t, "first.db")
	first, err := firstImporter.Import(ctx, fixture())
	if err != nil {
		t.Fatal(err)
	}
	if first.Users != (legacyimport.Counts{Read: 2, Imported: 2}) || first.Taxonomy != (legacyimport.Counts{Read: 2, Imported: 2}) {
		t.Fatalf("unexpected imported base counts: %#v", first)
	}
	if first.Posts.Read != 3 || first.Posts.Imported != 1 || first.Posts.Quarantined != 2 {
		t.Fatalf("unexpected post counts: %#v", first.Posts)
	}
	if first.Comments.Read != 3 || first.Comments.Imported != 2 || first.Comments.Quarantined != 1 {
		t.Fatalf("unexpected comment counts: %#v", first.Comments)
	}
	if first.Reasons["paid_content_disabled"] != 1 || first.Reasons["orphan_post_author"] != 1 || first.Reasons["orphan_comment_post"] != 1 {
		t.Fatalf("unexpected quarantine reasons: %#v", first.Reasons)
	}
	for name, counts := range map[string]legacyimport.Counts{"users": first.Users, "taxonomy": first.Taxonomy, "posts": first.Posts, "comments": first.Comments} {
		if counts.Read != counts.Imported+counts.Quarantined {
			t.Fatalf("%s counts do not reconcile: %#v", name, counts)
		}
	}
	replayed, err := firstImporter.Import(ctx, fixture())
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.RunID != first.RunID || replayed.SourceHash != first.SourceHash {
		t.Fatalf("repeat import is not idempotent: first=%#v replay=%#v", first, replayed)
	}

	secondImporter := openDB(t, "second.db")
	second, err := secondImporter.Import(ctx, fixture())
	if err != nil {
		t.Fatal(err)
	}
	if second.RunID != first.RunID || second.SourceHash != first.SourceHash || second.Users != first.Users || second.Posts != first.Posts || second.Comments != first.Comments {
		t.Fatalf("fresh imports are not reproducible: first=%#v second=%#v", first, second)
	}
}

func TestImporterRefusesNonSyntheticSources(t *testing.T) {
	document := fixture()
	document.Source = "production"
	if _, err := openDB(t, "refuse.db").Import(context.Background(), document); err == nil {
		t.Fatal("importer accepted a non-synthetic source")
	}
}

func TestImporterRefusesDuplicateSourceIDsBeforeMutation(t *testing.T) {
	ctx := context.Background()
	db := openRawDB(t, "duplicate-ids.db")
	document := fixture()
	document.Posts = append(document.Posts, document.Posts[0])
	if _, err := legacyimport.New(db).Import(ctx, document); err == nil {
		t.Fatal("importer accepted ambiguous duplicate source IDs")
	}
	for _, table := range []string{"users", "posts", "legacy_import_runs", "legacy_quarantine"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("duplicate-ID rejection left %d rows in %s", count, table)
		}
	}
}

func TestSyntheticImportRehearsalReportsAndRollsBack(t *testing.T) {
	ctx := context.Background()
	db := openRawDB(t, "rehearsal.db")
	importer := legacyimport.New(db)
	document := fixture()
	before, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	report, err := importer.Rehearse(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("rehearsal mutated source document: before=%s after=%s", before, after)
	}
	if !report.DryRun || report.Replayed {
		t.Fatalf("unexpected rehearsal flags: %#v", report)
	}
	if report.Users.Imported != 2 || report.Posts.Imported != 1 || report.Comments.Imported != 2 {
		t.Fatalf("rehearsal did not execute full mapping: %#v", report)
	}
	for _, table := range []string{"users", "posts", "comments", "legacy_import_runs", "legacy_quarantine"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("rehearsal committed %d rows to %s", count, table)
		}
	}

	committed, err := importer.Import(ctx, fixture())
	if err != nil {
		t.Fatal(err)
	}
	if committed.DryRun || committed.Replayed || committed.RunID != report.RunID || committed.SourceHash != report.SourceHash {
		t.Fatalf("commit after rehearsal is inconsistent: rehearsal=%#v committed=%#v", report, committed)
	}
}

func TestSyntheticImportQuarantinesTargetConflictsWithoutAborting(t *testing.T) {
	ctx := context.Background()
	db := openRawDB(t, "conflicts.db")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users(id, handle, email, password_hash, display_name, role, status, created_at, updated_at)
		VALUES ('existing', 'legacy_alice', 'existing@example.test', 'disabled', 'Existing', 'member', 'active', '2020-01-01T00:00:00Z', '2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	report, err := legacyimport.New(db).Import(ctx, fixture())
	if err != nil {
		t.Fatal(err)
	}
	if report.Users != (legacyimport.Counts{Read: 2, Imported: 1, Quarantined: 1}) {
		t.Fatalf("unexpected user conflict reconciliation: %#v", report.Users)
	}
	if report.Reasons["target_conflict"] != 1 || report.Reasons["orphan_post_author"] != 3 {
		t.Fatalf("unexpected conflict cascade: %#v", report.Reasons)
	}
	for name, counts := range map[string]legacyimport.Counts{
		"users": report.Users, "taxonomy": report.Taxonomy, "posts": report.Posts,
		"comments": report.Comments, "follows": report.Follows, "media": report.Media,
	} {
		if counts.Read != counts.Imported+counts.Quarantined {
			t.Fatalf("%s counts do not reconcile: %#v", name, counts)
		}
	}
	var quarantined int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_quarantine WHERE run_id = ?`, report.RunID).Scan(&quarantined); err != nil {
		t.Fatal(err)
	}
	wantQuarantined := report.Users.Quarantined + report.Taxonomy.Quarantined + report.Posts.Quarantined + report.Comments.Quarantined + report.Follows.Quarantined + report.Media.Quarantined
	if quarantined != wantQuarantined {
		t.Fatalf("quarantine rows=%d, want %d", quarantined, wantQuarantined)
	}
}

func TestSyntheticImportRehearsalRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	db := openRawDB(t, "failed-rehearsal.db")
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER reject_rehearsal_post BEFORE INSERT ON posts
		BEGIN SELECT RAISE(ABORT, 'forced rehearsal failure'); END`); err != nil {
		t.Fatal(err)
	}
	document := legacyimport.Document{
		Source: "synthetic", MappingVersion: 1,
		Users: []legacyimport.LegacyUser{{
			ID: "rehearsal-user", Handle: "rehearsal_user", Email: "rehearsal@example.test",
			DisplayName: "Rehearsal", Role: "member", Status: "active",
		}},
		Posts: []legacyimport.LegacyPost{
			{ID: "a-paid", AuthorID: "rehearsal-user", Kind: "article", Title: "Paid", Body: "Quarantine first", Status: "publish", Paid: true},
			{ID: "z-valid", AuthorID: "rehearsal-user", Kind: "article", Title: "Valid", Body: "Trigger failure", Status: "publish"},
		},
	}
	if _, err := legacyimport.New(db).Rehearse(ctx, document); err == nil {
		t.Fatal("rehearsal unexpectedly succeeded")
	}
	for _, table := range []string{"users", "posts", "legacy_import_runs", "legacy_quarantine"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed rehearsal left %d rows in %s", count, table)
		}
	}
}
