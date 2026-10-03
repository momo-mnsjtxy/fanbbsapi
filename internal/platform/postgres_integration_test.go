package platform_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"fanbbs.local/backend/internal/blob"
	"fanbbs.local/backend/internal/community"
	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/legacyimport"
	"fanbbs.local/backend/internal/platform"
	"github.com/jackc/pgx/v5"
)

func TestPostgresAdapterAndSyntheticMigrationRehearsal(t *testing.T) {
	dsn := os.Getenv("FANBBS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FANBBS_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	resetPostgres(t, ctx, dsn)

	db, err := platform.OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var migrationCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 10 {
		t.Fatalf("expected 10 PostgreSQL migrations, got %d", migrationCount)
	}
	if err := platform.ApplyPostgresMigrations(ctx, db); err != nil {
		t.Fatalf("idempotent migration rerun: %v", err)
	}

	identityService := identity.NewService(db)
	alice, err := identityService.Register(ctx, identity.RegisterInput{Handle: "pg_alice", Email: "pg-alice@example.test", Password: "correct horse", DisplayName: "Postgres Alice"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := identityService.Register(ctx, identity.RegisterInput{Handle: "pg_bob", Email: "pg-bob@example.test", Password: "correct horse", DisplayName: "Postgres Bob"})
	if err != nil {
		t.Fatal(err)
	}
	loggedIn, err := identityService.Login(ctx, "PG_ALICE", "correct horse")
	if err != nil {
		t.Fatalf("login through PostgreSQL: %v", err)
	}
	if authenticated, _, err := identityService.Authenticate(ctx, loggedIn.AccessToken); err != nil || authenticated.ID != alice.User.ID {
		t.Fatalf("authenticate through PostgreSQL: user=%#v err=%v", authenticated, err)
	}

	blobs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	communityService := community.NewService(db, blobs)
	post, replayed, err := communityService.CreatePost(ctx, alice.User.ID, "pg-post-key", community.CreatePostInput{
		Kind: "article", Title: "PostgreSQL production path", Body: "The same domain service is running on pgx.", Visibility: "followers",
	})
	if err != nil || replayed {
		t.Fatalf("create post: replayed=%v err=%v", replayed, err)
	}
	second, replayed, err := communityService.CreatePost(ctx, alice.User.ID, "pg-post-key", community.CreatePostInput{
		Kind: "article", Title: "PostgreSQL production path", Body: "The same domain service is running on pgx.", Visibility: "followers",
	})
	if err != nil || !replayed || second.ID != post.ID {
		t.Fatalf("idempotent post replay: post=%#v replayed=%v err=%v", second, replayed, err)
	}
	if anonymous, _, err := communityService.ListPosts(ctx, "latest", "", 0, 10); err != nil || len(anonymous) != 0 {
		t.Fatalf("followers-only post leaked to anonymous feed: posts=%d err=%v", len(anonymous), err)
	}
	if created, _, err := communityService.Follow(ctx, bob.User.ID, alice.User.ID); err != nil || !created {
		t.Fatalf("follow: created=%v err=%v", created, err)
	}
	if feed, _, err := communityService.ListPosts(ctx, "following", bob.User.ID, 0, 10); err != nil || len(feed) != 1 || feed[0].ID != post.ID {
		t.Fatalf("following feed: posts=%#v err=%v", feed, err)
	}

	comment, replayed, err := communityService.AddComment(ctx, post.ID, bob.User.ID, "", "pg-comment-key", "PostgreSQL comment")
	if err != nil || replayed {
		t.Fatalf("add comment: replayed=%v err=%v", replayed, err)
	}
	replayedComment, replayed, err := communityService.AddComment(ctx, post.ID, bob.User.ID, "", "pg-comment-key", "PostgreSQL comment")
	if err != nil || !replayed || replayedComment.ID != comment.ID {
		t.Fatalf("comment replay: comment=%#v replayed=%v err=%v", replayedComment, replayed, err)
	}
	if liked, count, err := communityService.LikeComment(ctx, post.ID, comment.ID, alice.User.ID); err != nil || !liked || count != 1 {
		t.Fatalf("comment like: liked=%v count=%d err=%v", liked, count, err)
	}
	if comments, _, err := communityService.ListComments(ctx, post.ID, alice.User.ID, 0, 10); err != nil || len(comments) != 1 || !comments[0].Liked {
		t.Fatalf("list comments: comments=%#v err=%v", comments, err)
	}
	updated, err := communityService.UpdatePost(ctx, post.ID, alice.User.ID, 1, community.UpdatePostInput{Title: pointer("PostgreSQL production adapter")})
	if err != nil || updated.Version != 2 {
		t.Fatalf("optimistic post update: post=%#v err=%v", updated, err)
	}

	importer := legacyimport.New(db)
	report, err := importer.Import(ctx, syntheticFixture("ok"))
	if err != nil {
		t.Fatalf("synthetic PostgreSQL import: %v", err)
	}
	if report.Users != (legacyimport.Counts{Read: 2, Imported: 2}) || report.Taxonomy != (legacyimport.Counts{Read: 2, Imported: 2}) || report.Posts != (legacyimport.Counts{Read: 3, Imported: 1, Quarantined: 2}) || report.Comments != (legacyimport.Counts{Read: 3, Imported: 2, Quarantined: 1}) {
		t.Fatalf("unexpected import reconciliation: %#v", report)
	}
	var quarantineCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_quarantine WHERE run_id = ?`, report.RunID).Scan(&quarantineCount); err != nil || quarantineCount != 3 {
		t.Fatalf("quarantine rows=%d err=%v", quarantineCount, err)
	}
	replay, err := importer.Import(ctx, syntheticFixture("ok"))
	if err != nil || !replay.Replayed || replay.RunID != report.RunID {
		t.Fatalf("import replay=%#v err=%v", replay, err)
	}
	conflict, err := importer.Import(ctx, targetConflictFixture())
	if err != nil {
		t.Fatalf("PostgreSQL target conflict aborted import transaction: %v", err)
	}
	if conflict.Users != (legacyimport.Counts{Read: 1, Quarantined: 1}) || conflict.Reasons["target_conflict"] != 1 {
		t.Fatalf("unexpected PostgreSQL target conflict reconciliation: %#v", conflict)
	}
	rehearsalBefore := tableCounts(t, ctx, db)
	rehearsal, err := importer.Rehearse(ctx, syntheticFixture("dryrun"))
	if err != nil || !rehearsal.DryRun || rehearsal.Replayed {
		t.Fatalf("PostgreSQL dry-run rehearsal=%#v err=%v", rehearsal, err)
	}
	if rehearsalAfter := tableCounts(t, ctx, db); rehearsalAfter != rehearsalBefore {
		t.Fatalf("PostgreSQL dry-run committed rows: before=%#v after=%#v", rehearsalBefore, rehearsalAfter)
	}

	before := tableCounts(t, ctx, db)
	if _, err := db.ExecContext(ctx, `
		CREATE FUNCTION reject_rehearsal_post() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic rollback rehearsal'; END;
		$$;
		CREATE TRIGGER reject_rehearsal_post BEFORE INSERT ON posts FOR EACH ROW EXECUTE FUNCTION reject_rehearsal_post()`); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Import(ctx, rollbackFixture()); err == nil {
		t.Fatal("rollback rehearsal unexpectedly succeeded")
	}
	after := tableCounts(t, ctx, db)
	if before != after {
		t.Fatalf("failed synthetic import was not atomic: before=%#v after=%#v", before, after)
	}
}

func resetPostgres(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)
	if _, err := connection.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
}

func pointer(value string) *string { return &value }

type counts struct{ Users, Posts, Runs, Quarantine int }

func tableCounts(t *testing.T, ctx context.Context, db *sql.DB) counts {
	t.Helper()
	var result counts
	for _, item := range []struct {
		query string
		value *int
	}{{`SELECT COUNT(*) FROM users`, &result.Users}, {`SELECT COUNT(*) FROM posts`, &result.Posts}, {`SELECT COUNT(*) FROM legacy_import_runs`, &result.Runs}, {`SELECT COUNT(*) FROM legacy_quarantine`, &result.Quarantine}} {
		if err := db.QueryRowContext(ctx, item.query).Scan(item.value); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func syntheticFixture(suffix string) legacyimport.Document {
	return legacyimport.Document{
		Source: "synthetic", MappingVersion: 1,
		Users: []legacyimport.LegacyUser{
			{ID: "u1-" + suffix, Handle: "legacy_alice_" + suffix, Email: "legacy-alice-" + suffix + "@example.test", DisplayName: "Legacy Alice " + suffix, Role: "contributor", Status: "active", CreatedAt: "2020-01-01T00:00:00Z"},
			{ID: "u2-" + suffix, Handle: "legacy_bob_" + suffix, Email: "legacy-bob-" + suffix + "@example.test", DisplayName: "Legacy Bob " + suffix, Role: "editor", Status: "active", CreatedAt: "2020-01-02T00:00:00Z"},
		},
		Taxonomy: []legacyimport.LegacyTaxonomy{
			{ID: "c1-" + suffix, Kind: "category", Slug: "legacy-life-" + suffix, Name: "Legacy life " + suffix},
			{ID: "t1-" + suffix, Kind: "tag", Slug: "legacy-tag-" + suffix, Name: "Legacy tag " + suffix},
		},
		Posts: []legacyimport.LegacyPost{
			{ID: "p1-" + suffix, AuthorID: "u1-" + suffix, Kind: "article", Title: "Imported article", Body: "Synthetic body", CategoryID: "c1-" + suffix, Status: "publish", CreatedAt: "2020-02-01T00:00:00Z"},
			{ID: "p2-" + suffix, AuthorID: "u1-" + suffix, Kind: "article", Title: "Paid article", Body: "Disabled", Status: "publish", Paid: true},
			{ID: "p3-" + suffix, AuthorID: "missing", Kind: "article", Title: "Orphan article", Body: "Disabled", Status: "publish"},
		},
		Comments: []legacyimport.LegacyComment{
			{ID: "m1-" + suffix, PostID: "p1-" + suffix, AuthorID: "u1-" + suffix, Body: "Synthetic comment", CreatedAt: "2020-02-02T00:00:00Z"},
			{ID: "m2-" + suffix, PostID: "p1-" + suffix, AuthorID: "u2-" + suffix, ParentID: "m1-" + suffix, Body: "Synthetic reply", CreatedAt: "2020-02-03T00:00:00Z"},
			{ID: "m3-" + suffix, PostID: "missing", AuthorID: "u1-" + suffix, Body: "Orphan comment"},
		},
	}
}

func rollbackFixture() legacyimport.Document {
	return legacyimport.Document{
		Source: "synthetic", MappingVersion: 1,
		Users: []legacyimport.LegacyUser{{ID: "rollback-user", Handle: "rollback_user", Email: "rollback@example.test", DisplayName: "Rollback User", Role: "member", Status: "active"}},
		Posts: []legacyimport.LegacyPost{
			{ID: "a-paid", AuthorID: "rollback-user", Kind: "article", Title: "Quarantine before failure", Body: "Disabled", Status: "publish", Paid: true},
			{ID: "z-valid", AuthorID: "rollback-user", Kind: "article", Title: "Trigger rollback", Body: "Must not persist", Status: "publish"},
		},
	}
}

func targetConflictFixture() legacyimport.Document {
	return legacyimport.Document{
		Source: "synthetic", MappingVersion: 1,
		Users: []legacyimport.LegacyUser{{
			ID: "target-conflict-user", Handle: "pg_alice", Email: "target-conflict@example.test",
			DisplayName: "Target Conflict", Role: "member", Status: "active",
		}},
	}
}
