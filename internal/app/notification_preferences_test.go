package app_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func notificationData(t *testing.T, api *testAPI, path, token string) map[string]any {
	t.Helper()
	return expectStatus(t, api.request(http.MethodGet, path, token, nil, nil), http.StatusOK)["data"].(map[string]any)
}

func TestNotificationPreferencesDefaultsScopeValidationAndFutureDelivery(t *testing.T) {
	api := newTestAPI(t)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	demo, _ := login(t, api)
	for _, path := range []string{"/api/v1/notifications/counts", "/api/v1/me/notification-preferences"} {
		expectStatus(t, api.request(http.MethodGet, path, "", nil, nil), http.StatusUnauthorized)
	}
	prefs := notificationData(t, api, "/api/v1/me/notification-preferences", rain)["preferences"].(map[string]any)
	if len(prefs) != 6 {
		t.Fatalf("incomplete defaults: %#v", prefs)
	}
	for kind, value := range prefs {
		if value != true {
			t.Fatalf("default %s=%v", kind, value)
		}
	}
	// Create historical data before muting: existing unread events must survive.
	expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/post_morning/reactions/like", demo, nil, nil), http.StatusOK)
	patch := map[string]any{"preferences": map[string]any{"like": false, "comment": false}}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/notification-preferences", rain, patch, nil), http.StatusOK)
	for _, invalid := range []map[string]any{
		{}, {"like": nil}, {"bogus": false}, {"moderation": false}, {"moderation": true}, {"follow": false, "bad": false},
	} {
		expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/notification-preferences", rain, map[string]any{"preferences": invalid}, nil), http.StatusUnprocessableEntity)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/notification-preferences", rain, map[string]any{"preferences": map[string]any{"like": "false"}}, nil), http.StatusBadRequest)
	prefs = notificationData(t, api, "/api/v1/me/notification-preferences", rain)["preferences"].(map[string]any)
	if prefs["like"] != false || prefs["follow"] != true || prefs["moderation"] != true {
		t.Fatalf("invalid batch changed preferences: %#v", prefs)
	}
	other := notificationData(t, api, "/api/v1/me/notification-preferences", demo)["preferences"].(map[string]any)
	if other["like"] != true {
		t.Fatal("preference leaked across owners")
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/post_morning/reactions/like", demo, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/post_morning/reactions/like", demo, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/post_morning/comments", demo, map[string]any{"content": "Muted notification still saves the comment."}, nil), http.StatusCreated)
	counts := notificationData(t, api, "/api/v1/notifications/counts", rain)
	if counts["total"] != float64(1) || counts["by_type"].(map[string]any)["like"] != float64(1) {
		t.Fatalf("mute deleted history or delivered new events: %#v", counts)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/notification-preferences", rain, map[string]any{"preferences": map[string]any{"like": true}}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/post_morning/reactions/like", demo, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/post_morning/reactions/like", demo, nil, nil), http.StatusOK)
	counts = notificationData(t, api, "/api/v1/notifications/counts", rain)
	if counts["total"] != float64(2) {
		t.Fatalf("unmute failed: %#v", counts)
	}
	expectStatus(t, api.request(http.MethodPut, "/api/v1/notifications/read", rain, map[string]any{"ids": []string{}}, nil), http.StatusOK)
	counts = notificationData(t, api, "/api/v1/notifications/counts", rain)
	if counts["total"] != float64(0) || len(counts["by_type"].(map[string]any)) != 6 {
		t.Fatalf("read counts mismatch: %#v", counts)
	}
}

func TestNotificationPreferencesBatchRollback(t *testing.T) {
	api := newTestAPI(t)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	if _, err := api.db.Exec(`CREATE TRIGGER fail_like_preference BEFORE INSERT ON notification_preferences WHEN NEW.type = 'like' BEGIN SELECT RAISE(ABORT, 'forced preference failure'); END`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/notification-preferences", rain,
		map[string]any{"preferences": map[string]any{"comment": false, "like": false}}, nil), http.StatusInternalServerError)
	var count int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM notification_preferences`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partial preference batch committed: %d", count)
	}
}

func TestNotificationSeekPaginationSurvivesReadsNewEventsAndRejectsWrongScope(t *testing.T) {
	api := newTestAPI(t)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	demo, _ := login(t, api)
	add := func(id, owner, kind string) {
		t.Helper()
		if _, err := api.db.Exec(`INSERT INTO notifications(id,user_id,type,actor_id,subject_type,subject_id,payload,created_at) VALUES (?,?,?,'usr_forest','post','post_morning','{}','2026-01-01T00:00:00Z')`, id, owner, kind); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 5; i++ {
		add(fmt.Sprintf("ntf_test_%d", i), "usr_rain", "like")
	}
	add("ntf_private", "usr_demo", "comment")
	first := expectStatus(t, api.request(http.MethodGet, "/api/v1/notifications?unread=true&type=like&limit=2", rain, nil, nil), http.StatusOK)
	firstItems := first["data"].([]any)
	if firstItems[0].(map[string]any)["id"] != "ntf_test_5" || firstItems[1].(map[string]any)["id"] != "ntf_test_4" {
		t.Fatalf("unstable ordering: %#v", firstItems)
	}
	cursor := first["page"].(map[string]any)["next_cursor"].(string)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/notifications/read", rain, map[string]any{"ids": []string{"ntf_test_5", "ntf_test_4", "ntf_private"}}, nil), http.StatusOK)
	add("ntf_test_6", "usr_rain", "like")
	second := expectStatus(t, api.request(http.MethodGet, "/api/v1/notifications?unread=true&type=like&limit=2&cursor="+url.QueryEscape(cursor), rain, nil, nil), http.StatusOK)
	secondItems := second["data"].([]any)
	if len(secondItems) != 2 || secondItems[0].(map[string]any)["id"] != "ntf_test_3" || secondItems[1].(map[string]any)["id"] != "ntf_test_2" {
		t.Fatalf("page skipped or duplicated after writes: %#v", secondItems)
	}
	for _, path := range []string{
		"/api/v1/notifications?unread=false&type=like&cursor=" + cursor,
		"/api/v1/notifications?unread=true&type=comment&cursor=" + cursor,
		"/api/v1/notifications?unread=wrong", "/api/v1/notifications?type=wrong", "/api/v1/notifications?limit=0", "/api/v1/notifications?cursor=bad",
	} {
		expectStatus(t, api.request(http.MethodGet, path, rain, nil, nil), http.StatusUnprocessableEntity)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/notifications?unread=true&type=like&cursor="+cursor, demo, nil, nil), http.StatusUnprocessableEntity)
	counts := notificationData(t, api, "/api/v1/notifications/counts", demo)
	if counts["total"] != float64(1) {
		t.Fatalf("foreign read changed owner event: %#v", counts)
	}
}

func TestMutedMessagesRetainReconnectSynchronizationAndMandatoryModeration(t *testing.T) {
	api := newTestAPI(t)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	demo, _ := login(t, api)
	forest, _ := loginAs(t, api, "forest", "demo1234")
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/notification-preferences", rain, map[string]any{
		"preferences": map[string]any{"message": false},
	}, nil), http.StatusOK)
	conversation := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", demo, map[string]any{
		"member_ids": []string{"usr_rain"},
	}, nil), http.StatusCreated)
	conversationID := conversation["data"].(map[string]any)["id"].(string)
	send := map[string]any{"client_message_id": "muted-sync-1", "body": "Still synchronizes while muted."}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversationID+"/messages", demo, send, nil), http.StatusCreated)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversationID+"/messages", demo, send, nil), http.StatusOK)
	inbox := expectStatus(t, api.request(http.MethodGet, "/api/v1/notifications", rain, nil, nil), http.StatusOK)
	if len(inbox["data"].([]any)) != 0 {
		t.Fatal("muted message was shown in inbox")
	}
	events := expectStatus(t, api.request(http.MethodGet, "/api/v1/events", rain, nil, nil), http.StatusOK)["data"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["notification"].(map[string]any)["muted"] != true {
		t.Fatalf("muting lost or duplicated reconnect fact: %#v", events)
	}
	messages := expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations/"+conversationID+"/messages", rain, nil, nil), http.StatusOK)
	if len(messages["data"].([]any)) != 1 {
		t.Fatal("muting lost message")
	}
	if _, err := api.db.Exec(`UPDATE users SET role = 'moderator' WHERE id = 'usr_forest'`); err != nil {
		t.Fatal(err)
	}
	report := expectStatus(t, api.request(http.MethodPost, "/api/v1/reports", demo, map[string]any{
		"target_type": "post", "target_id": "post_morning", "reason": "A synthetic review reason for mandatory notices.",
	}, nil), http.StatusCreated)
	reportID := report["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/reports/"+reportID+"/decision", forest,
		map[string]any{"decision": "remove_content", "reason": "Synthetic moderation decision."}, nil), http.StatusOK)
	counts := notificationData(t, api, "/api/v1/notifications/counts", rain)
	if counts["total"] != float64(1) || counts["by_type"].(map[string]any)["moderation"] != float64(1) {
		t.Fatalf("mandatory moderation notice missing: %#v", counts)
	}
}
