package legacyimport_test

import (
	"context"
	"path/filepath"
	"testing"

	"fanbbs.local/backend/internal/legacyimport"
	"fanbbs.local/backend/internal/platform"
)

func openDB(t *testing.T, name string) *legacyimport.Importer {
	t.Helper()
	db, err := platform.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return legacyimport.New(db)
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
