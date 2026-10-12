package app_test

import (
	"encoding/base64"
	"net/http"
	"testing"
)

func TestAuthorMediaEditSmoke(t *testing.T) {
	api := newTestAPI(t)
	owner, _ := login(t, api)
	other, _ := loginAs(t, api, "rain", "demo1234")
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	upload := func(token string) string {
		return expectStatus(t, api.multipart(http.MethodPost, "/api/v1/uploads", token, "author-edit.png", png, nil), http.StatusCreated)["data"].(map[string]any)["id"].(string)
	}
	a, b, foreign := upload(owner), upload(owner), upload(other)
	post := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", owner, map[string]any{"title": "Attachment editing", "content": "Editable draft attachments", "status": "draft", "media_ids": []string{a}}, nil), http.StatusCreated)["data"].(map[string]any)
	id := post["id"].(string)
	patch := func(ids []string, version string, status int) map[string]any {
		return expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+id, owner, map[string]any{"media_ids": ids}, map[string]string{"If-Match": version}), status)
	}
	patch([]string{b, foreign}, `"1"`, http.StatusUnprocessableEntity)
	result := patch([]string{b, a}, `"1"`, http.StatusOK)["data"].(map[string]any)
	media := result["media"].([]any)
	if len(media) != 2 || media[0].(map[string]any)["id"] != b || media[1].(map[string]any)["id"] != a {
		t.Fatalf("media ordering: %#v", media)
	}
	patch([]string{a}, `"1"`, http.StatusConflict)
	patch([]string{a, a}, `"2"`, http.StatusUnprocessableEntity)
	result = patch([]string{}, `"2"`, http.StatusOK)["data"].(map[string]any)
	if len(result["media"].([]any)) != 0 {
		t.Fatal("attachments not removed")
	}
	var count int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='post_media_edit' AND target_id=?`, id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/media/"+a, other, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/me/posts/"+id+"/review", owner, map[string]any{"action": "submit", "reason": "Review this complete draft"}, map[string]string{"If-Match": `"3"`}), http.StatusOK)
	patch([]string{a}, `"4"`, http.StatusConflict)
}
