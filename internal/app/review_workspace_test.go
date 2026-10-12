package app_test

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"testing"
)

func TestModeratorWorkspaceMediaAndVersionSmoke(t *testing.T) {
	api := newTestAPI(t)
	author, _ := loginAs(t, api, "rain", "demo1234")
	if _, err := api.db.Exec(`UPDATE users SET role='moderator' WHERE id='usr_demo'`); err != nil {
		t.Fatal(err)
	}
	moderator, _ := login(t, api)
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	upload := expectStatus(t, api.multipart(http.MethodPost, "/api/v1/uploads", author, "review.png", png, nil), http.StatusCreated)["data"].(map[string]any)
	asset := upload["id"].(string)
	post := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", author, map[string]any{"content": "Pending image review", "status": "pending", "media_ids": []string{asset}}, nil), http.StatusCreated)["data"].(map[string]any)
	id := post["id"].(string)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/posts/"+id, author, nil, nil), http.StatusForbidden)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/media/"+asset, moderator, nil, nil), http.StatusNotFound)
	detail := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/posts/"+id, moderator, nil, nil), http.StatusOK)["data"].(map[string]any)
	if detail["post"].(map[string]any)["status"] != "pending" {
		t.Fatal("moderator cannot inspect pending detail")
	}
	media := api.request(http.MethodGet, "/api/v1/admin/posts/"+id+"/media/"+asset, moderator, nil, nil)
	if media.Code != 200 || media.Header().Get("Cache-Control") != "private, no-store" || !bytes.Equal(media.Body.Bytes(), png) {
		t.Fatalf("review media response=%d", media.Code)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/posts/post_go/media/"+asset, moderator, nil, nil), http.StatusNotFound)
	decision := map[string]any{"decision": "published", "reason": "The complete attachment was reviewed"}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/posts/"+id+"/moderation", moderator, decision, map[string]string{"If-Match": `"999"`}), http.StatusConflict)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/posts/"+id+"/moderation", moderator, decision, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	history := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/posts/"+id+"/review-history", moderator, nil, nil), http.StatusOK)["data"].([]any)
	if len(history) != 1 || history[0].(map[string]any)["after"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("review history missing decision version: %#v", history)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/posts/"+id+"/review-history", author, nil, nil), http.StatusForbidden)
}
