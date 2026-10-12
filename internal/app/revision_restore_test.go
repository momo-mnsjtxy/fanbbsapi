package app_test

import (
	"net/http"
	"testing"
)

func TestAuthorRevisionRestoreSmoke(t *testing.T) {
	api := newTestAPI(t)
	owner, _ := login(t, api)
	other, _ := loginAs(t, api, "rain", "demo1234")
	post := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", owner, map[string]any{"title": "Original draft title", "content": "Original body", "visibility": "followers", "status": "draft"}, nil), http.StatusCreated)["data"].(map[string]any)
	id := post["id"].(string)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+id, owner, map[string]any{"title": "Changed draft title", "content": "Changed body", "visibility": "public"}, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	body := map[string]any{"version": 1, "reason": "Recover original draft content"}
	path := "/api/v1/posts/" + id + "/revisions/restore"
	expectStatus(t, api.request(http.MethodPost, path, other, body, map[string]string{"If-Match": `"2"`}), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPost, path, owner, body, map[string]string{"If-Match": `"1"`}), http.StatusConflict)
	restored := expectStatus(t, api.request(http.MethodPost, path, owner, body, map[string]string{"If-Match": `"2"`}), http.StatusOK)["data"].(map[string]any)
	if restored["content"] != "Original body" || restored["visibility"] != "followers" || restored["version"] != float64(3) || restored["status"] != "draft" {
		t.Fatalf("incorrect restored draft: %#v", restored)
	}
	history := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+id+"/revisions", owner, nil, nil), http.StatusOK)["data"].([]any)
	if len(history) != 2 || history[0].(map[string]any)["body"] != "Changed body" {
		t.Fatalf("history was lost: %#v", history)
	}
	var count int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE target_id=? AND action='post_revision_restore'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("restore audit=%d err=%v", count, err)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/me/posts/"+id+"/review", owner, map[string]any{"action": "submit", "reason": "Ready for review now"}, map[string]string{"If-Match": `"3"`}), http.StatusOK)
	expectStatus(t, api.request(http.MethodPost, path, owner, body, map[string]string{"If-Match": `"4"`}), http.StatusConflict)
}
