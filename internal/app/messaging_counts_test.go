package app_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestConversationUnreadCountsReceiptsAndLeaveStayConsistent(t *testing.T) {
	api := newTestAPI(t)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	demo, _ := login(t, api)
	forest, _ := loginAs(t, api, "forest", "demo1234")
	expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations/counts", "", nil, nil), http.StatusUnauthorized)
	create := func(sender string, members []string) string {
		return expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", sender, map[string]any{"member_ids": members}, nil), http.StatusCreated)["data"].(map[string]any)["id"].(string)
	}
	first := create(demo, []string{"usr_rain"})
	second := create(forest, []string{"usr_rain"})
	send := func(conversation, sender, key string) string {
		return expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversation+"/messages", sender, map[string]any{"client_message_id": key, "body": "Synthetic unread count message."}, nil), http.StatusCreated)["data"].(map[string]any)["id"].(string)
	}
	messages := []string{}
	for i := 0; i < 3; i++ {
		messages = append(messages, send(first, demo, fmt.Sprintf("count-%d", i)))
	}
	send(first, rain, "own-reply")
	send(second, forest, "other-thread")
	counts := notificationData(t, api, "/api/v1/conversations/counts", rain)
	if counts["conversations"] != float64(2) || counts["messages"] != float64(4) {
		t.Fatalf("aggregate includes own messages or misses conversation: %#v", counts)
	}
	detail := notificationData(t, api, "/api/v1/conversations/"+first, rain)
	if detail["unread_count"] != float64(3) {
		t.Fatalf("detail mismatch: %#v", detail)
	}
	expectStatus(t, api.request(http.MethodPut, "/api/v1/conversations/"+first+"/read", rain, map[string]any{"message_id": messages[1]}, nil), http.StatusOK)
	counts = notificationData(t, api, "/api/v1/conversations/counts", rain)
	if counts["messages"] != float64(2) {
		t.Fatalf("partial receipt cleared too much: %#v", counts)
	}
	inbox := notificationData(t, api, "/api/v1/notifications/counts", rain)
	if inbox["by_type"].(map[string]any)["message"] != float64(2) {
		t.Fatalf("inbox receipt not synchronized: %#v", inbox)
	}
	list := expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations", rain, nil, nil), http.StatusOK)["data"].([]any)
	for _, raw := range list {
		item := raw.(map[string]any)
		if item["id"] == first && (item["unread_count"] != float64(1) || item["last_read_message_id"] != messages[1]) {
			t.Fatalf("list receipt missing: %#v", item)
		}
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/conversations/"+first+"/membership", rain, nil, nil), http.StatusOK)
	counts = notificationData(t, api, "/api/v1/conversations/counts", rain)
	if counts["conversations"] != float64(1) || counts["messages"] != float64(1) {
		t.Fatalf("left conversation still counted: %#v", counts)
	}
	inbox = notificationData(t, api, "/api/v1/notifications/counts", rain)
	if inbox["total"] != float64(1) {
		t.Fatalf("leave did not dismiss inbox: %#v", inbox)
	}
	other := notificationData(t, api, "/api/v1/conversations/counts", demo)
	if other["messages"] != float64(1) {
		t.Fatalf("leave cleared another member's unread state: %#v", other)
	}
}

func TestConversationReceiptAndLeaveRollbackWhenBadgeUpdateFails(t *testing.T) {
	api := newTestAPI(t)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	demo, _ := login(t, api)
	conversation := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", demo, map[string]any{"member_ids": []string{"usr_rain"}}, nil), http.StatusCreated)["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversation+"/messages", demo, map[string]any{"client_message_id": "rollback-badge", "body": "Must not become partially read."}, nil), http.StatusCreated)
	if _, err := api.db.Exec(`CREATE TRIGGER fail_badge_read BEFORE UPDATE OF read_at ON notifications BEGIN SELECT RAISE(ABORT, 'forced badge failure'); END`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPut, "/api/v1/conversations/"+conversation+"/read", rain, map[string]any{}, nil), http.StatusInternalServerError)
	detail := notificationData(t, api, "/api/v1/conversations/"+conversation, rain)
	if detail["unread_count"] != float64(1) || detail["last_read_message_id"] != nil {
		t.Fatalf("failed transaction advanced receipt: %#v", detail)
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/conversations/"+conversation+"/membership", rain, nil, nil), http.StatusInternalServerError)
	detail = notificationData(t, api, "/api/v1/conversations/"+conversation, rain)
	if detail["unread_count"] != float64(1) {
		t.Fatalf("failed leave changed membership: %#v", detail)
	}
}

func TestConversationNoOpReceiptReconcilesOnlyCoveredBadges(t *testing.T) {
	api := newTestAPI(t)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	demo, _ := login(t, api)
	conversation := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", demo, map[string]any{"member_ids": []string{"usr_rain"}}, nil), http.StatusCreated)["data"].(map[string]any)["id"].(string)
	ids := []string{}
	for i := 0; i < 3; i++ {
		item := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversation+"/messages", demo, map[string]any{"client_message_id": fmt.Sprintf("reconcile-%d", i), "body": "Receipt reconciliation fixture"}, nil), http.StatusCreated)
		ids = append(ids, item["data"].(map[string]any)["id"].(string))
	}
	// Model a valid existing receipt independently of inbox state. Repeated
	// receipt requests must repair covered badges, without a migration/backfill.
	if _, err := api.db.Exec(`UPDATE conversation_members SET read_sequence=(SELECT sequence FROM messages WHERE id=?),read_at='2026-01-01T00:00:00Z' WHERE conversation_id=? AND user_id='usr_rain'`, ids[1], conversation); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{ids[0], ids[1]} {
		if _, err := api.db.Exec(`UPDATE notifications SET read_at=NULL WHERE user_id='usr_rain'`); err != nil {
			t.Fatal(err)
		}
		item := expectStatus(t, api.request(http.MethodPut, "/api/v1/conversations/"+conversation+"/read", rain, map[string]any{"message_id": target}, nil), http.StatusOK)["data"].(map[string]any)
		if item["changed"] != false || item["last_read_message_id"] != ids[1] || item["read_at"] != "2026-01-01T00:00:00Z" {
			t.Fatalf("no-op receipt changed checkpoint: %#v", item)
		}
		counts := notificationData(t, api, "/api/v1/notifications/counts", rain)
		if counts["total"] != float64(1) {
			t.Fatalf("no-op receipt did not reconcile exactly covered badges: %#v", counts)
		}
	}
	// Badge errors remain visible and atomic even when the receipt does not move.
	if _, err := api.db.Exec(`UPDATE notifications SET read_at=NULL WHERE user_id='usr_rain'; CREATE TRIGGER reject_noop_badge BEFORE UPDATE OF read_at ON notifications BEGIN SELECT RAISE(ABORT,'forced noop reconciliation failure'); END`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPut, "/api/v1/conversations/"+conversation+"/read", rain, map[string]any{"message_id": ids[0]}, nil), http.StatusInternalServerError)
	counts := notificationData(t, api, "/api/v1/notifications/counts", rain)
	if counts["total"] != float64(3) {
		t.Fatalf("failed no-op reconciliation partially changed badges: %#v", counts)
	}
}
