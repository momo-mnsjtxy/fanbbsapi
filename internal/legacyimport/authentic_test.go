package legacyimport_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"fanbbs.local/backend/internal/legacyimport"
	"fanbbs.local/backend/internal/platform"
)

func authenticFixture() legacyimport.AuthenticSnapshot {
	return legacyimport.AuthenticSnapshot{
		Source: "synthetic", Schema: legacyimport.AuthenticSchema, MappingVersion: 1,
		Users: []legacyimport.AuthenticUser{
			{UID: 1, Name: "Legacy_Alice", ScreenName: "旧用户甲", Password: "$P$synthetic-legacy-hash", Mail: "alice@example.test", Introduce: "旧签名", Avatar: "https://synthetic.invalid/avatar.png", Status: 1, Group: "contributor", Created: 1_577_836_800},
			{UID: 2, Name: "legacy_bob", ScreenName: "旧用户乙", Password: "$P$another-synthetic-hash", Mail: "bob@example.test", Status: 1, Group: "editor", Created: 1_577_923_200},
		},
		Metas: []legacyimport.AuthenticMeta{
			{MID: 10, Name: "旧生活", Slug: "legacy-life", Type: "category", ImgURL: "https://synthetic.invalid/category.png"},
			{MID: 11, Name: "旧标签", Type: "tag"},
		},
		Relationships: []legacyimport.AuthenticRelationship{
			{CID: 100, MID: 10}, {CID: 100, MID: 11}, {CID: 100, MID: 11},
		},
		Contents: []legacyimport.AuthenticContent{
			{CID: 100, MID: 10, Title: "合成旧图片帖", Text: "这是离线合成的旧内容。", AuthorID: 1, Type: "photo", Status: "publish", Images: `["https://synthetic.invalid/post.png"]`, Created: 1_580_515_200},
			{CID: 101, MID: 10, Title: "合成付费视频", Text: "付费内容不会导入。", AuthorID: 1, Type: "video", Status: "publish", Price: 9, Videos: `[{"src":"https://synthetic.invalid/video.mp4","poster":"https://synthetic.invalid/poster.png"}]`, Created: 1_580_601_600},
		},
		Comments: []legacyimport.AuthenticComment{
			{ID: 201, CID: 100, UID: 2, Text: "合成回复", Parent: 200, All: 200, Type: 0, Created: 1_580_774_400},
			{ID: 200, CID: 100, UID: 1, Text: "合成评论", Images: `["https://synthetic.invalid/comment.png"]`, Type: 0, Created: 1_580_688_000},
		},
		Fans: []legacyimport.AuthenticFan{
			{ID: 300, UID: 1, ToUID: 2, Created: 1_580_860_800},
			{ID: 301, UID: 1, ToUID: 99, Created: 1_580_947_200},
		},
	}
}

func openAuthenticDB(t *testing.T) (*sql.DB, *legacyimport.Importer) {
	t.Helper()
	db, err := platform.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "authentic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, legacyimport.New(db)
}

func TestAuthenticShapeAdapterAndOfflineImport(t *testing.T) {
	document, err := legacyimport.AdaptAuthenticSnapshot(authenticFixture())
	if err != nil {
		t.Fatal(err)
	}
	if document.Users[0].Handle != "legacy_alice" || document.Users[0].Bio != "旧签名" {
		t.Fatalf("unexpected authentic user mapping: %#v", document.Users[0])
	}
	if document.Taxonomy[1].Slug != "legacy-meta-11" {
		t.Fatalf("empty legacy slug did not receive deterministic fallback: %#v", document.Taxonomy[1])
	}
	if document.Posts[0].Kind != "image" || len(document.Posts[0].TagIDs) != 1 || document.Posts[0].TagIDs[0] != "11" {
		t.Fatalf("unexpected content/relationship mapping: %#v", document.Posts[0])
	}
	if len(document.Media) != 6 {
		t.Fatalf("unexpected media inventory: %#v", document.Media)
	}

	db, importer := openAuthenticDB(t)
	report, err := importer.Import(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	if report.Users != (legacyimport.Counts{Read: 2, Imported: 2}) || report.Taxonomy != (legacyimport.Counts{Read: 2, Imported: 2}) {
		t.Fatalf("unexpected base counts: %#v", report)
	}
	if report.Posts != (legacyimport.Counts{Read: 2, Imported: 1, Quarantined: 1}) || report.Comments != (legacyimport.Counts{Read: 2, Imported: 2}) {
		t.Fatalf("unexpected content counts: %#v", report)
	}
	if report.Follows != (legacyimport.Counts{Read: 2, Imported: 1, Quarantined: 1}) || report.Media != (legacyimport.Counts{Read: 6, Quarantined: 6}) {
		t.Fatalf("unexpected social/media counts: %#v", report)
	}
	if report.Reasons["paid_content_disabled"] != 1 || report.Reasons["orphan_follow_followed"] != 1 || report.Reasons["media_recopy_required"] != 6 {
		t.Fatalf("unexpected quarantine reasons: %#v", report.Reasons)
	}

	var handle, passwordHash, bio string
	if err := db.QueryRow(`SELECT handle, password_hash, bio FROM users WHERE email = 'alice@example.test'`).Scan(&handle, &passwordHash, &bio); err != nil {
		t.Fatal(err)
	}
	if handle != "legacy_alice" || passwordHash != "legacy-login-disabled" || bio != "旧签名" {
		t.Fatalf("legacy credentials/profile mapping is unsafe or incomplete: handle=%q hash=%q bio=%q", handle, passwordHash, bio)
	}
	var postKind string
	if err := db.QueryRow(`SELECT kind FROM posts WHERE title = '合成旧图片帖'`).Scan(&postKind); err != nil {
		t.Fatal(err)
	}
	if postKind != "image" {
		t.Fatalf("legacy photo kind mapped to %q", postKind)
	}
	for table, want := range map[string]int{"post_tags": 1, "follows": 1, "comments": 2, "media_assets": 0} {
		var got int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s row count = %d, want %d", table, got, want)
		}
	}
	var leaked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE password_hash IN ('$P$synthetic-legacy-hash', '$P$another-synthetic-hash')`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("legacy password hash became a live credential")
	}
}

func TestAuthenticShapeAdapterRefusesUndeclaredOrRealSources(t *testing.T) {
	tests := []legacyimport.AuthenticSnapshot{
		{Source: "production", Schema: legacyimport.AuthenticSchema, MappingVersion: 1},
		{Source: "synthetic", Schema: "guessed-schema", MappingVersion: 1},
		{Source: "synthetic", Schema: legacyimport.AuthenticSchema, MappingVersion: 2},
	}
	for _, snapshot := range tests {
		if _, err := legacyimport.AdaptAuthenticSnapshot(snapshot); err == nil {
			t.Fatalf("adapter accepted unsafe snapshot declaration: %#v", snapshot)
		}
	}
}
