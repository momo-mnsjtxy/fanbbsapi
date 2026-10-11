package platform_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"fanbbs.local/backend/internal/blob"
	"fanbbs.local/backend/internal/community"
	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func TestPostgresNotificationPreferencesAndSeek(t *testing.T) {
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
	defer db.Close()
	ids := identity.NewService(db)
	alice, err := ids.Register(ctx, identity.RegisterInput{Handle: "inbox_alice", Email: "inbox-alice@example.test", Password: "test password 123", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := ids.Register(ctx, identity.RegisterInput{Handle: "inbox_bob", Email: "inbox-bob@example.test", Password: "test password 123", DisplayName: "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := community.NewService(db, blobs)
	disabled, enabled := false, true
	if err := service.UpdateNotificationPreferences(ctx, alice.User.ID, map[string]*bool{"follow": &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Follow(ctx, bob.User.ID, alice.User.ID); err != nil {
		t.Fatal(err)
	}
	counts, err := service.NotificationUnreadCounts(ctx, alice.User.ID)
	if err != nil || counts.Total != 0 {
		t.Fatalf("muted counts=%#v err=%v", counts, err)
	}
	events, _, _, err := service.Events(ctx, alice.User.ID, "", 20)
	if err != nil || len(events) != 1 || !events[0].Notification.Muted {
		t.Fatalf("muted event missing: %#v %v", events, err)
	}
	if err := service.UpdateNotificationPreferences(ctx, alice.User.ID, map[string]*bool{"follow": &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Unfollow(ctx, bob.User.ID, alice.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Follow(ctx, bob.User.ID, alice.User.ID); err != nil {
		t.Fatal(err)
	}
	items, next, err := service.Notifications(ctx, alice.User.ID, true, "follow", "", 1)
	if err != nil || len(items) != 1 || next != "" {
		t.Fatalf("visible page=%#v next=%s err=%v", items, next, err)
	}
	counts, err = service.NotificationUnreadCounts(ctx, alice.User.ID)
	if err != nil || counts.Total != 1 || counts.ByType["follow"] != 1 {
		t.Fatalf("unmute counts=%#v err=%v", counts, err)
	}
	if _, err := service.MarkNotificationsRead(ctx, bob.User.ID, []string{items[0].ID}); err != nil {
		t.Fatal(err)
	}
	counts, _ = service.NotificationUnreadCounts(ctx, alice.User.ID)
	if counts.Total != 1 {
		t.Fatal("foreign owner marked event")
	}
	if _, err := service.MarkNotificationsRead(ctx, alice.User.ID, []string{items[0].ID}); err != nil {
		t.Fatal(err)
	}
	counts, _ = service.NotificationUnreadCounts(ctx, alice.User.ID)
	if counts.Total != 0 {
		t.Fatal("owner read did not clear count")
	}
	conversation, _, err := service.CreateConversation(ctx, alice.User.ID, community.CreateConversationInput{MemberIDs: []string{bob.User.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var messageIDs []string
	for i := 0; i < 12; i++ {
		message, _, err := service.SendMessage(ctx, conversation.ID, alice.User.ID, fmt.Sprintf("pg-receipt-%d", i), "Concurrent receipt fixture")
		if err != nil {
			t.Fatal(err)
		}
		messageIDs = append(messageIDs, message.ID)
	}
	countsBefore, err := service.ConversationCounts(ctx, bob.User.ID)
	if err != nil || countsBefore.Messages != 12 {
		t.Fatalf("chat counts before=%#v err=%v", countsBefore, err)
	}
	// Contending transactions intentionally race older and newer checkpoints.
	// The database UPDATE predicate, not a stale read in application memory,
	// must prevent receipt regression.
	var wait sync.WaitGroup
	errors := make(chan error, len(messageIDs)*2)
	start := make(chan struct{})
	for i := 0; i < len(messageIDs)*2; i++ {
		id := messageIDs[i%len(messageIDs)]
		wait.Go(func() {
			<-start
			_, err := service.MarkConversationRead(ctx, conversation.ID, bob.User.ID, id)
			errors <- err
		})
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	detail, err := service.Conversation(ctx, conversation.ID, bob.User.ID)
	if err != nil || detail.LastReadMessageID != messageIDs[len(messageIDs)-1] || detail.UnreadCount != 0 {
		t.Fatalf("concurrent receipt regressed: %#v err=%v", detail, err)
	}
	inboxCounts, err := service.NotificationUnreadCounts(ctx, bob.User.ID)
	if err != nil || inboxCounts.Total != 0 {
		t.Fatalf("read chat has stale badges: %#v err=%v", inboxCounts, err)
	}
	if _, err := ids.RenameDeviceSession(ctx, alice.User.ID, alice.SessionID, "PostgreSQL laptop"); err != nil {
		t.Fatal(err)
	}
	if _, err := ids.RenameDeviceSession(ctx, bob.User.ID, alice.SessionID, "not mine"); err == nil {
		t.Fatal("foreign session rename succeeded")
	}
	refreshed, err := ids.Refresh(ctx, alice.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := ids.DeviceSessions(ctx, alice.User.ID, refreshed.SessionID)
	if err != nil || len(sessions) != 1 || sessions[0].Name != "PostgreSQL laptop" || sessions[0].LastActiveAt == "" {
		t.Fatalf("rotated metadata=%#v err=%v", sessions, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE sessions SET last_active_unix = 0 WHERE id = ?`, refreshed.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ids.Authenticate(ctx, refreshed.AccessToken); err != nil {
		t.Fatal(err)
	}
	var activity int64
	if err := db.QueryRowContext(ctx, `SELECT last_active_unix FROM sessions WHERE id = ?`, refreshed.SessionID).Scan(&activity); err != nil || activity <= 0 {
		t.Fatalf("activity=%d err=%v", activity, err)
	}
	pending, _, err := service.CreatePost(ctx, bob.User.ID, "pg-owner-post", community.CreatePostInput{Title: "Private review fixture", Body: "Only the owner may inspect this pending submission", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	ownerDetail, err := service.OwnerPost(ctx, bob.User.ID, pending.ID)
	if err != nil || ownerDetail.Moderation == nil || ownerDetail.Moderation.State != "pending" {
		t.Fatalf("owner detail=%#v err=%v", ownerDetail, err)
	}
	if _, err := service.OwnerPost(ctx, alice.User.ID, pending.ID); err == nil {
		t.Fatal("foreign owner accessed pending content")
	}
	ownerPosts, _, err := service.OwnerPosts(ctx, bob.User.ID, "pending", "", 10)
	if err != nil || len(ownerPosts) != 1 || ownerPosts[0].ID != pending.ID {
		t.Fatalf("owner pending list=%#v err=%v", ownerPosts, err)
	}
	if err := platform.ApplyPostgresMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
}
