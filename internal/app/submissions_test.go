package app_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestAuthorDraftReviewLifecycleSmoke(t *testing.T) {
	api := newTestAPI(t)
	author, _ := loginAs(t, api, "rain", "demo1234")
	other, _ := loginAs(t, api, "forest", "demo1234")
	if _, err := api.db.Exec(`UPDATE users SET role='moderator' WHERE id='usr_demo'`); err != nil {
		t.Fatal(err)
	}
	moderator, _ := login(t, api)
	post := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", author, map[string]any{"content": "A persistent draft awaiting review", "status": "draft"}, nil), http.StatusCreated)["data"].(map[string]any)
	id := post["id"].(string)
	version := func() map[string]string { return map[string]string{"If-Match": fmt.Sprintf(`"%.0f"`, post["version"])} }
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+id, "", nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+id, other, map[string]any{"content": "foreign edit"}, version()), http.StatusNotFound)
	edit := func(body string) {
		post = expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+id, author, map[string]any{"content": body}, version()), http.StatusOK)["data"].(map[string]any)
	}
	transition := func(action string) {
		post = expectStatus(t, api.request(http.MethodPost, "/api/v1/me/posts/"+id+"/review", author, map[string]any{"action": action, "reason": "Synthetic author submission reason"}, version()), http.StatusOK)["data"].(map[string]any)
	}
	edit("Draft revision before submission")
	transition("submit")
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+id, author, map[string]any{"content": "not while pending"}, version()), http.StatusConflict)
	transition("withdraw")
	edit("Corrected after withdrawal")
	transition("submit")
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/posts/"+id+"/moderation", moderator, map[string]any{"decision": "rejected", "reason": "Clarify the example before approval"}, nil), http.StatusOK)
	post = notificationData(t, api, "/api/v1/me/posts/"+id, author)["post"].(map[string]any)
	edit("Clarified example after rejection")
	transition("submit")
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/posts/"+id+"/moderation", moderator, map[string]any{"decision": "published", "reason": "Corrected draft is approved"}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+id, "", nil, nil), http.StatusOK)
	var audits int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE target_id=? AND action IN ('submission_submit','submission_withdraw')`, id).Scan(&audits); err != nil || audits != 4 {
		t.Fatalf("submission audit count=%d err=%v", audits, err)
	}
}
