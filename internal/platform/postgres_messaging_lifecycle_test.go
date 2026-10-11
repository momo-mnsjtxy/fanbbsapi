package platform_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"fanbbs.local/backend/internal/blob"
	"fanbbs.local/backend/internal/community"
	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func TestPostgresMessagingReceiptRepairAndSendLeaveSerialization(t *testing.T) {
	for _, table := range []string{"notifications", "messages"} {
		t.Run("pause-before-"+table, func(t *testing.T) { testPostgresMessagingLifecycle(t, table) })
	}
}

func testPostgresMessagingLifecycle(t *testing.T, gateTable string) {
	dsn := os.Getenv("FANBBS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FANBBS_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resetPostgres(t, ctx, dsn)
	db, err := platform.OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	identityService := identity.NewService(db)
	alice, err := identityService.Register(ctx, identity.RegisterInput{Handle: "lifecycle_alice", Email: "life-alice@example.test", Password: "synthetic password", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := identityService.Register(ctx, identity.RegisterInput{Handle: "lifecycle_bob", Email: "life-bob@example.test", Password: "synthetic password", DisplayName: "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	local, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := community.NewService(db, local)
	conversation, _, err := service.CreateConversation(ctx, alice.User.ID, community.CreateConversationInput{MemberIDs: []string{bob.User.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		message, _, err := service.SendMessage(ctx, conversation.ID, alice.User.ID, fmt.Sprintf("repair-%d", i), "Receipt fixture")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, message.ID)
	}
	if _, err := db.ExecContext(ctx, `UPDATE conversation_members SET read_sequence=(SELECT sequence FROM messages WHERE id=?),read_at='2026-01-01T00:00:00Z' WHERE conversation_id=? AND user_id=?`, ids[1], conversation.ID, bob.User.ID); err != nil {
		t.Fatal(err)
	}
	receipt, err := service.MarkConversationRead(ctx, conversation.ID, bob.User.ID, ids[0])
	if err != nil || receipt.Changed || receipt.LastReadMessageID != ids[1] {
		t.Fatalf("older receipt repair=%#v err=%v", receipt, err)
	}
	counts, err := service.NotificationUnreadCounts(ctx, bob.User.ID)
	if err != nil || counts.Total != 1 {
		t.Fatalf("repair did not retain only newer badge: %#v %v", counts, err)
	}
	// Deterministic gate: hold a session advisory lock, then pause the sender's
	// notification trigger on that lock. Leave must wait for the same conversation
	// transaction, not dismiss an incomplete recipient snapshot and return early.
	const gate = int64(847261011)
	controller, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if _, err := controller.ExecContext(ctx, `SELECT pg_advisory_lock(?)`, gate); err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { _, _ = controller.ExecContext(context.Background(), `SELECT pg_advisory_unlock(?)`, gate) })
	}
	var workers sync.WaitGroup
	defer func() { release(); workers.Wait() }()
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION pause_lifecycle_notification() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(847261011); RETURN NEW; END $$; CREATE TRIGGER pause_lifecycle_notification BEFORE INSERT ON `+gateTable+` FOR EACH ROW EXECUTE FUNCTION pause_lifecycle_notification()`); err != nil {
		t.Fatal(err)
	}
	sendDone := make(chan error, 1)
	var sentMessageID string
	workers.Go(func() {
		message, _, err := service.SendMessage(ctx, conversation.ID, alice.User.ID, "send-leave-gate", "Send commits before leave dismissal")
		sentMessageID = message.ID
		sendDone <- err
	})
	waitForBlockedQuery(t, ctx, db, "%INSERT INTO "+gateTable+"%")
	leaveDone := make(chan error, 1)
	workers.Go(func() { leaveDone <- service.LeaveConversation(ctx, conversation.ID, bob.User.ID) })
	// The gate remains held, so this is an actual lock-order assertion rather
	// than a sleep-based race. An old uncoordinated Leave returns instead.
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-leaveDone:
			t.Fatalf("leave returned before sender notification committed: %v", err)
		default:
		}
		var blocked bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND query LIKE '%SELECT c.id FROM conversations c%' AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("leave did not reach conversation lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	release()
	if err := <-sendDone; err != nil {
		t.Fatal(err)
	}
	if err := <-leaveDone; err != nil {
		t.Fatal(err)
	}
	counts, err = service.NotificationUnreadCounts(ctx, bob.User.ID)
	if err != nil || counts.Total != 0 {
		t.Fatalf("left recipient has stranded badges: %#v %v", counts, err)
	}
	var delivered int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id=? AND subject_id=? AND type='message' AND read_at IS NOT NULL`, bob.User.ID, sentMessageID).Scan(&delivered); err != nil || delivered != 1 {
		t.Fatalf("send had no durable recipient notification: count=%d err=%v", delivered, err)
	}
	chats, err := service.ConversationCounts(ctx, bob.User.ID)
	if err != nil || chats.Messages != 0 {
		t.Fatalf("left recipient retains unread conversation: %#v %v", chats, err)
	}
	if _, _, err := service.SendMessage(ctx, conversation.ID, alice.User.ID, "after-left", "No recipients remain"); err == nil {
		t.Fatal("send after leave unexpectedly succeeded")
	}
}

func waitForBlockedQuery(t *testing.T, ctx context.Context, db *sql.DB, pattern string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND query LIKE ? AND cardinality(pg_blocking_pids(pid))>0)`, pattern).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("query did not reach test gate: %s", pattern)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
