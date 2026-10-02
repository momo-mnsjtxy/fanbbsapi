package platform

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"fanbbs.local/backend/migrations"
	_ "github.com/mattn/go-sqlite3"
)

func TestMessagingMigrationPreservesNotificationsAndForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	raw, err := sql.Open("sqlite3", "file:"+path+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_initial.sql", "002_accounts_social_moderation.sql", "003_media_edits_legacy_import.sql"} {
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("apply fixture migration %s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`
		INSERT INTO users(id, handle, email, password_hash, display_name, role, status, created_at)
		VALUES ('usr_upgrade', 'upgrade', 'upgrade@example.test', 'fixture-hash', '升级测试', 'member', 'active', '2026-01-01T00:00:00Z');
		INSERT INTO notifications(id, user_id, type, subject_type, subject_id, payload, created_at)
		VALUES ('ntf_upgrade', 'usr_upgrade', 'comment', 'post', 'post_fixture', '{}', '2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var preserved int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE id = 'ntf_upgrade' AND type = 'comment'`).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if preserved != 1 {
		t.Fatalf("existing notification was not preserved: %d", preserved)
	}
	if _, err := db.Exec(`INSERT INTO notifications(id, user_id, type, subject_type, subject_id, payload, created_at) VALUES ('ntf_message', 'usr_upgrade', 'message', 'message', 'msg_fixture', '{}', '2026-01-01T00:00:01Z')`); err != nil {
		t.Fatalf("message notification kind is unavailable: %v", err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID int64
		var parent string
		var fkID int
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("foreign key violation after migration: table=%s row=%d parent=%s fk=%d", table, rowID, parent, fkID)
	}
}
