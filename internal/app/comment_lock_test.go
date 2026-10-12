package app_test

import (
	"net/http"
	"testing"
)

func TestCommentLockLifecycleSmoke(t *testing.T) {
	api := newTestAPI(t)
	author, _ := loginAs(t, api, "rain", "demo1234")
	if _, err := api.db.Exec(`UPDATE users SET role='moderator' WHERE id='usr_demo'`); err != nil {
		t.Fatal(err)
	}
	mod, _ := login(t, api)
	post := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", author, map[string]any{"content": "Discussion with moderated comment lock"}, nil), http.StatusCreated)["data"].(map[string]any)
	id := post["id"].(string)
	commentPath := "/api/v1/posts/" + id + "/comments"
	path := "/api/v1/admin/posts/" + id + "/comments-lock"
	input := map[string]any{"content": "Existing comment remains visible"}
	comment := expectStatus(t, api.request(http.MethodPost, commentPath, author, input, map[string]string{"Idempotency-Key": "comment-before-lock"}), http.StatusCreated)["data"].(map[string]any)
	closeInput := map[string]any{"locked": true, "reason": "Pause new replies for moderation"}
	expectStatus(t, api.request(http.MethodPut, path, author, closeInput, map[string]string{"If-Match": `"1"`}), http.StatusForbidden)
	expectStatus(t, api.request(http.MethodPut, path, mod, closeInput, map[string]string{"If-Match": `"99"`}), http.StatusConflict)
	expectStatus(t, api.request(http.MethodPut, path, mod, closeInput, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	view := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+id, author, nil, nil), http.StatusOK)["data"].(map[string]any)
	if view["comments_locked"] != true {
		t.Fatal("lock state missing from post")
	}
	expectStatus(t, api.request(http.MethodPost, commentPath, author, input, nil), http.StatusConflict)
	expectStatus(t, api.request(http.MethodPost, commentPath+"/"+comment["id"].(string)+"/replies", mod, input, nil), http.StatusConflict)
	expectStatus(t, api.request(http.MethodPost, commentPath, author, input, map[string]string{"Idempotency-Key": "comment-before-lock"}), http.StatusOK)
	visible := expectStatus(t, api.request(http.MethodGet, commentPath, author, nil, nil), http.StatusOK)["data"].([]any)
	if len(visible) != 1 {
		t.Fatal("lock hid existing discussion")
	}
	expectStatus(t, api.request(http.MethodPut, path, mod, map[string]any{"locked": false, "reason": "Discussion can now resume"}, map[string]string{"If-Match": `"2"`}), http.StatusOK)
	expectStatus(t, api.request(http.MethodPost, commentPath, author, input, nil), http.StatusCreated)
	var count int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE target_id=? AND action='post_comments_lock'`, id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("lock audits=%d err=%v", count, err)
	}
}
