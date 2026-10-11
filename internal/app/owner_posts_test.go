package app_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestOwnerPostWorkspacePreservesPendingAndRejectedPrivacy(t *testing.T) {
	api := newTestAPI(t)
	member, _ := loginAs(t, api, "forest", "demo1234")
	other, _ := loginAs(t, api, "rain", "demo1234")
	if _, err := api.db.Exec(`UPDATE users SET role='moderator' WHERE id='usr_demo'`); err != nil {
		t.Fatal(err)
	}
	moderator, _ := login(t, api)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts", "", nil, nil), http.StatusUnauthorized)
	create := func(body string) string {
		return expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", member, map[string]any{"content": body, "status": "pending"}, nil), http.StatusCreated)["data"].(map[string]any)["id"].(string)
	}
	first := create("Private owner review workspace first draft")
	second := create("Private owner review workspace second draft")
	for _, id := range []string{first, second} {
		for _, token := range []string{"", other, member, moderator} {
			expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+id, token, nil, nil), http.StatusNotFound)
		}
		for _, token := range []string{other, moderator} {
			expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts/"+id, token, nil, nil), http.StatusNotFound)
		}
		response := api.request(http.MethodGet, "/api/v1/me/posts/"+id, member, nil, nil)
		item := expectStatus(t, response, http.StatusOK)["data"].(map[string]any)
		if item["post"].(map[string]any)["status"] != "pending" || item["moderation"].(map[string]any)["state"] != "pending" || response.Header().Get("ETag") != `"1"` {
			t.Fatalf("missing private review state: %#v", item)
		}
	}
	page := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts?status=pending&limit=1", member, nil, nil), http.StatusOK)
	if page["data"].([]any)[0].(map[string]any)["id"] != second {
		t.Fatalf("unexpected workspace order: %#v", page)
	}
	cursor := page["page"].(map[string]any)["next_cursor"].(string)
	create("New pending post must not shift older pages")
	next := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts?status=pending&limit=1&cursor="+url.QueryEscape(cursor), member, nil, nil), http.StatusOK)
	if len(next["data"].([]any)) != 1 || next["data"].([]any)[0].(map[string]any)["id"] != first {
		t.Fatalf("owner seek skipped or duplicated: %#v", next)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts?status=pending&cursor="+cursor, other, nil, nil), http.StatusUnprocessableEntity)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts?status=rejected&cursor="+cursor, member, nil, nil), http.StatusUnprocessableEntity)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts?status=deleted", member, nil, nil), http.StatusUnprocessableEntity)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/posts/"+first+"/moderation", moderator, map[string]any{"decision": "rejected", "reason": "Please clarify the supporting example."}, nil), http.StatusOK)
	detail := notificationData(t, api, "/api/v1/me/posts/"+first, member)
	if detail["post"].(map[string]any)["status"] != "rejected" || detail["moderation"].(map[string]any)["reason"] != "Please clarify the supporting example." {
		t.Fatalf("owner cannot inspect rejection: %#v", detail)
	}
	rejected := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts?status=rejected", member, nil, nil), http.StatusOK)
	if len(rejected["data"].([]any)) != 1 {
		t.Fatalf("rejected filter incorrect: %#v", rejected)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+first, "", nil, nil), http.StatusNotFound)
	// Soft-deleted published posts are absent even from the author workspace.
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/post_go", member, nil, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts/post_go", member, nil, nil), http.StatusNotFound)
	all := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/posts", member, nil, nil), http.StatusOK)["data"].([]any)
	if len(all) != 3 {
		t.Fatalf("deleted or foreign post leaked into workspace: %#v", all)
	}
}
