package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"fanbbs.local/backend/internal/app"
	"fanbbs.local/backend/internal/blob"
	"fanbbs.local/backend/internal/platform"
)

type testAPI struct {
	t       *testing.T
	db      *sql.DB
	handler http.Handler
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	db, err := platform.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "fanbbs-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	localBlobs, err := blob.NewLocal(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	application := app.NewWithBlob(db, localBlobs)
	if err := application.Identity.SeedDemo(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &testAPI{t: t, db: db, handler: application.Handler}
}

func (api *testAPI) multipart(method, path, token, filename string, content []byte, fields map[string]string) *httptest.ResponseRecorder {
	api.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			api.t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		api.t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		api.t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		api.t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	api.handler.ServeHTTP(response, request)
	return response
}

func (api *testAPI) request(method, path, token string, body any, headers map[string]string) *httptest.ResponseRecorder {
	api.t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			api.t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	api.handler.ServeHTTP(response, request)
	return response
}

func decode(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %d %q: %v", response.Code, response.Body.String(), err)
	}
	return payload
}

func expectStatus(t *testing.T, response *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	return decode(t, response)
}

func login(t *testing.T, api *testAPI) (string, string) {
	return loginAs(t, api, "demo", "demo1234")
}

func loginAs(t *testing.T, api *testAPI, identity, password string) (string, string) {
	t.Helper()
	payload := expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"identity": identity, "password": password,
	}, nil), http.StatusOK)
	data := payload["data"].(map[string]any)
	return data["access_token"].(string), data["refresh_token"].(string)
}

func TestRegistrationProfilePasswordAndDeactivation(t *testing.T) {
	api := newTestAPI(t)
	register := expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"handle": "new_member", "email": "new.member@example.test", "password": "initial-pass-9", "display_name": "新成员",
	}, nil), http.StatusCreated)
	access := register["data"].(map[string]any)["access_token"].(string)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"handle": "new_member", "email": "another@example.test", "password": "initial-pass-9",
	}, nil), http.StatusConflict)

	profile := expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/profile", access, map[string]any{
		"display_name": "新的显示名", "bio": "本地账号生命周期测试", "avatar_url": "https://example.test/avatar.png",
	}, nil), http.StatusOK)
	if profile["data"].(map[string]any)["display_name"] != "新的显示名" {
		t.Fatalf("profile was not updated: %#v", profile)
	}
	secondAccess, _ := loginAs(t, api, "new.member@example.test", "initial-pass-9")
	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/password", access, map[string]any{
		"current_password": "initial-pass-9", "new_password": "replacement-pass-10",
	}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", secondAccess, nil, nil), http.StatusUnauthorized)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"identity": "new_member", "password": "initial-pass-9",
	}, nil), http.StatusUnauthorized)
	newAccess, _ := loginAs(t, api, "new_member", "replacement-pass-10")
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/me/account", newAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", newAccess, nil, nil), http.StatusUnauthorized)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"identity": "new_member", "password": "replacement-pass-10",
	}, nil), http.StatusForbidden)
}

func TestLocalMediaAvatarAndPostAttachment(t *testing.T) {
	api := newTestAPI(t)
	demoAccess, _ := login(t, api)
	rainAccess, _ := loginAs(t, api, "rain", "demo1234")
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.multipart(http.MethodPost, "/api/v1/uploads", "", "guest.png", png, nil), http.StatusUnauthorized)
	expectStatus(t, api.multipart(http.MethodPost, "/api/v1/uploads", demoAccess, "fake.png", []byte("<script>alert(1)</script>"), nil), http.StatusUnprocessableEntity)
	upload := expectStatus(t, api.multipart(http.MethodPost, "/api/v1/uploads", demoAccess, "../camera.png", png, map[string]string{"alt_text": "一个像素"}), http.StatusCreated)
	assetID := upload["data"].(map[string]any)["id"].(string)
	if response := api.request(http.MethodGet, "/api/v1/media/"+assetID, "", nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("unattached media leaked to guest: status=%d body=%s", response.Code, response.Body.String())
	}
	post := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", demoAccess, map[string]any{
		"content": "带有本地媒体引用的文章", "media_ids": []string{assetID},
	}, nil), http.StatusCreated)
	postData := post["data"].(map[string]any)
	if len(postData["media"].([]any)) != 1 {
		t.Fatalf("post did not expose attached media: %#v", postData)
	}
	mediaResponse := api.request(http.MethodGet, "/api/v1/media/"+assetID, "", nil, nil)
	if mediaResponse.Code != http.StatusOK || !bytes.Equal(mediaResponse.Body.Bytes(), png) || mediaResponse.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("public attached media response is wrong: status=%d headers=%v", mediaResponse.Code, mediaResponse.Header())
	}

	foreignUpload := expectStatus(t, api.multipart(http.MethodPost, "/api/v1/uploads", demoAccess, "foreign.png", png, nil), http.StatusCreated)
	foreignID := foreignUpload["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", rainAccess, map[string]any{
		"content": "不能引用他人的媒体", "media_ids": []string{foreignID},
	}, nil), http.StatusUnprocessableEntity)

	avatar := expectStatus(t, api.multipart(http.MethodPut, "/api/v1/me/avatar", demoAccess, "avatar.png", png, map[string]string{"alt_text": "头像"}), http.StatusCreated)
	avatarID := avatar["data"].(map[string]any)["id"].(string)
	me := expectStatus(t, api.request(http.MethodGet, "/api/v1/me", demoAccess, nil, nil), http.StatusOK)
	if me["data"].(map[string]any)["avatar_url"] != "/api/v1/media/"+avatarID {
		t.Fatalf("avatar reference was not updated: %#v", me)
	}
	if response := api.request(http.MethodGet, "/api/v1/media/"+avatarID, "", nil, nil); response.Code != http.StatusOK {
		t.Fatalf("active avatar is not public: status=%d", response.Code)
	}
}

func TestPostAndCommentOptimisticMutations(t *testing.T) {
	api := newTestAPI(t)
	demoAccess, _ := login(t, api)
	forestAccess, _ := loginAs(t, api, "forest", "demo1234")
	created := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", demoAccess, map[string]any{"content": "最初的文章正文"}, nil), http.StatusCreated)
	postID := created["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+postID, demoAccess, map[string]any{"content": "没有版本"}, nil), http.StatusPreconditionRequired)
	updated := expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+postID, demoAccess, map[string]any{"content": "更新后的文章正文"}, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	if updated["data"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("post version did not increment: %#v", updated)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+postID, demoAccess, map[string]any{"content": "过期写入"}, map[string]string{"If-Match": `"1"`}), http.StatusConflict)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+postID, forestAccess, map[string]any{"content": "越权写入"}, map[string]string{"If-Match": `"2"`}), http.StatusForbidden)

	comment := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/"+postID+"/comments", demoAccess, map[string]any{"content": "原始评论"}, nil), http.StatusCreated)
	commentID := comment["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+postID+"/comments/"+commentID, demoAccess, map[string]any{"content": "缺少版本"}, nil), http.StatusPreconditionRequired)
	edited := expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+postID+"/comments/"+commentID, demoAccess, map[string]any{"content": "更新后的评论"}, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	if edited["data"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("comment version did not increment: %#v", edited)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+postID+"/comments/"+commentID, demoAccess, map[string]any{"content": "过期评论"}, map[string]string{"If-Match": `"1"`}), http.StatusConflict)
	firstLike := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/comments/"+commentID+"/reactions/like", forestAccess, nil, nil), http.StatusOK)
	secondLike := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/comments/"+commentID+"/reactions/like", forestAccess, nil, nil), http.StatusOK)
	if firstLike["data"].(map[string]any)["changed"] != true || secondLike["data"].(map[string]any)["changed"] != false {
		t.Fatal("comment like PUT is not idempotent")
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/"+postID+"/comments/"+commentID, forestAccess, nil, map[string]string{"If-Match": `"2"`}), http.StatusForbidden)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/"+postID+"/comments/"+commentID, demoAccess, nil, map[string]string{"If-Match": `"1"`}), http.StatusConflict)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/"+postID+"/comments/"+commentID, demoAccess, nil, map[string]string{"If-Match": `"2"`}), http.StatusOK)
	detail := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID, demoAccess, nil, nil), http.StatusOK)
	if detail["data"].(map[string]any)["comment_count"] != float64(0) {
		t.Fatalf("comment delete did not maintain post counter: %#v", detail)
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/"+postID, demoAccess, nil, map[string]string{"If-Match": `"1"`}), http.StatusConflict)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/"+postID, demoAccess, nil, map[string]string{"If-Match": `"2"`}), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID, "", nil, nil), http.StatusNotFound)
}

func TestFollowerOnlyVisibilityIsEnforcedEverywhere(t *testing.T) {
	api := newTestAPI(t)
	rainAccess, _ := loginAs(t, api, "rain", "demo1234")
	followerAccess, _ := loginAs(t, api, "demo", "demo1234")
	strangerAccess, _ := loginAs(t, api, "forest", "demo1234")
	secret := "仅关注者能够看到的隐私边界内容"
	created := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", rainAccess, map[string]any{
		"content": secret, "visibility": "followers", "tag_ids": []string{"tag_city"},
	}, nil), http.StatusCreated)
	postID := created["data"].(map[string]any)["id"].(string)

	followerProfilePosts := expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_rain/posts", followerAccess, nil, nil), http.StatusOK)
	strangerProfilePosts := expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_rain/posts", strangerAccess, nil, nil), http.StatusOK)
	if len(followerProfilePosts["data"].([]any)) != 2 || len(strangerProfilePosts["data"].([]any)) != 1 {
		t.Fatalf("profile post visibility mismatch: follower=%#v stranger=%#v", followerProfilePosts, strangerProfilePosts)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/"+postID+"/comments", followerAccess, map[string]any{"content": "关注者可见评论"}, nil), http.StatusCreated)
	followerProfileComments := expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_demo/comments", followerAccess, nil, nil), http.StatusOK)
	strangerProfileComments := expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_demo/comments", strangerAccess, nil, nil), http.StatusOK)
	if len(followerProfileComments["data"].([]any)) != 1 || len(strangerProfileComments["data"].([]any)) != 0 {
		t.Fatalf("profile comment visibility mismatch: follower=%#v stranger=%#v", followerProfileComments, strangerProfileComments)
	}

	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID, followerAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID, strangerAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID, "", nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID+"/comments", followerAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID+"/comments", strangerAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/reactions/like", strangerAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/repost", strangerAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/reactions/like", followerAccess, nil, nil), http.StatusOK)
	repost := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/repost", followerAccess, nil, nil), http.StatusOK)
	repostData := repost["data"].(map[string]any)
	if repostData["visibility"] != "followers" || strings.Contains(repostData["title"].(string), secret) {
		t.Fatalf("restricted repost leaked source metadata: %#v", repostData)
	}

	followerSearch := expectStatus(t, api.request(http.MethodGet, "/api/v1/search?q="+url.QueryEscape("隐私边界")+"&type=posts", followerAccess, nil, nil), http.StatusOK)
	if len(followerSearch["data"].(map[string]any)["posts"].([]any)) != 1 {
		t.Fatalf("legitimate follower could not search visible post: %#v", followerSearch)
	}
	strangerSearch := expectStatus(t, api.request(http.MethodGet, "/api/v1/search?q="+url.QueryEscape("隐私边界")+"&type=posts", strangerAccess, nil, nil), http.StatusOK)
	if len(strangerSearch["data"].(map[string]any)["posts"].([]any)) != 0 {
		t.Fatalf("non-follower search leaked restricted post: %#v", strangerSearch)
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/users/usr_rain/follow", followerAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID, followerAccess, nil, nil), http.StatusNotFound)
}

func TestTaxonomyBookmarksFollowsAndNotifications(t *testing.T) {
	api := newTestAPI(t)
	demoAccess, _ := login(t, api)
	rainAccess, _ := loginAs(t, api, "rain", "demo1234")

	categories := expectStatus(t, api.request(http.MethodGet, "/api/v1/categories", "", nil, nil), http.StatusOK)
	tags := expectStatus(t, api.request(http.MethodGet, "/api/v1/tags", "", nil, nil), http.StatusOK)
	if len(categories["data"].([]any)) < 3 || len(tags["data"].([]any)) < 3 {
		t.Fatalf("taxonomy seed is incomplete: categories=%#v tags=%#v", categories, tags)
	}
	search := expectStatus(t, api.request(http.MethodGet, "/api/v1/search?q="+url.QueryEscape("城市")+"&type=all", "", nil, nil), http.StatusOK)
	if len(search["data"].(map[string]any)["tags"].([]any)) == 0 {
		t.Fatalf("tag search returned nothing: %#v", search)
	}

	firstBookmark := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/post_morning/bookmark", demoAccess, nil, nil), http.StatusOK)
	secondBookmark := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/post_morning/bookmark", demoAccess, nil, nil), http.StatusOK)
	if firstBookmark["data"].(map[string]any)["changed"] != true || secondBookmark["data"].(map[string]any)["changed"] != false {
		t.Fatal("bookmark PUT is not idempotent")
	}
	bookmarks := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/bookmarks", demoAccess, nil, nil), http.StatusOK)
	if len(bookmarks["data"].([]any)) != 1 {
		t.Fatalf("bookmark list mismatch: %#v", bookmarks)
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/post_morning/bookmark", demoAccess, nil, nil), http.StatusOK)
	secondDelete := expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/post_morning/bookmark", demoAccess, nil, nil), http.StatusOK)
	if secondDelete["data"].(map[string]any)["changed"] != false {
		t.Fatal("bookmark DELETE is not idempotent")
	}

	expectStatus(t, api.request(http.MethodPut, "/api/v1/users/usr_forest/follow", demoAccess, nil, nil), http.StatusOK)
	doubleFollow := expectStatus(t, api.request(http.MethodPut, "/api/v1/users/usr_forest/follow", demoAccess, nil, nil), http.StatusOK)
	if doubleFollow["data"].(map[string]any)["changed"] != false {
		t.Fatal("follow PUT is not idempotent")
	}
	expectStatus(t, api.request(http.MethodPut, "/api/v1/users/usr_demo/follow", demoAccess, nil, nil), http.StatusUnprocessableEntity)

	expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/post_morning/reactions/like", demoAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/post_morning/comments", demoAccess, map[string]any{"content": "用于验证持久通知的评论"}, nil), http.StatusCreated)
	notifications := expectStatus(t, api.request(http.MethodGet, "/api/v1/notifications?unread=true", rainAccess, nil, nil), http.StatusOK)
	items := notifications["data"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected one like and one comment notification, got %#v", items)
	}
	ids := []string{items[0].(map[string]any)["id"].(string)}
	mark := expectStatus(t, api.request(http.MethodPut, "/api/v1/notifications/read", rainAccess, map[string]any{"ids": ids}, nil), http.StatusOK)
	if mark["data"].(map[string]any)["changed"] != float64(1) {
		t.Fatalf("notification read state did not change: %#v", mark)
	}
	markAgain := expectStatus(t, api.request(http.MethodPut, "/api/v1/notifications/read", rainAccess, map[string]any{"ids": ids}, nil), http.StatusOK)
	if markAgain["data"].(map[string]any)["changed"] != float64(0) {
		t.Fatal("notification read PUT is not idempotent")
	}
}

func TestModerationRoleChecksAuditAndRollback(t *testing.T) {
	api := newTestAPI(t)
	demoAccess, _ := login(t, api)
	report := expectStatus(t, api.request(http.MethodPost, "/api/v1/reports", demoAccess, map[string]any{
		"target_type": "post", "target_id": "post_go", "reason": "这是一条需要人工复核的举报理由",
	}, map[string]string{"Idempotency-Key": "report-1"}), http.StatusCreated)
	reportID := report["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/reports", demoAccess, nil, nil), http.StatusForbidden)

	if _, err := api.db.Exec(`UPDATE users SET role = 'moderator' WHERE id = 'usr_rain'`); err != nil {
		t.Fatal(err)
	}
	moderatorAccess, _ := loginAs(t, api, "rain", "demo1234")
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/reports", moderatorAccess, nil, nil), http.StatusOK)
	decisionResponse := api.request(http.MethodPost, "/api/v1/admin/reports/"+reportID+"/decision", moderatorAccess,
		map[string]any{"decision": "remove_content", "reason": "确认内容违反社区规则"}, map[string]string{"X-Request-ID": "req_moderation_test"})
	expectStatus(t, decisionResponse, http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/post_go", "", nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/audit-events", moderatorAccess, nil, nil), http.StatusForbidden)

	if _, err := api.db.Exec(`UPDATE users SET role = 'admin' WHERE id = 'usr_rain'`); err != nil {
		t.Fatal(err)
	}
	audit := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/audit-events", moderatorAccess, nil, nil), http.StatusOK)
	auditItems := audit["data"].([]any)
	if len(auditItems) != 1 || auditItems[0].(map[string]any)["request_id"] != "req_moderation_test" {
		t.Fatalf("audit event is incomplete: %#v", audit)
	}
	if _, err := api.db.Exec(`UPDATE audit_events SET reason = 'tampered'`); err == nil {
		t.Fatal("immutable audit event accepted an update")
	}
	if _, err := api.db.Exec(`DELETE FROM audit_events`); err == nil {
		t.Fatal("immutable audit event accepted a delete")
	}

	secondReport := expectStatus(t, api.request(http.MethodPost, "/api/v1/reports", demoAccess, map[string]any{
		"target_type": "post", "target_id": "post_weekend", "reason": "另一条需要人工复核的举报理由",
	}, nil), http.StatusCreated)
	secondID := secondReport["data"].(map[string]any)["id"].(string)
	if _, err := api.db.Exec(`CREATE TRIGGER force_audit_failure BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT, 'forced audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/reports/"+secondID+"/decision", moderatorAccess,
		map[string]any{"decision": "remove_content", "reason": "此操作必须因审计失败而回滚"}, nil), http.StatusInternalServerError)
	var reportStatus, postStatus string
	if err := api.db.QueryRow(`SELECT status FROM reports WHERE id = ?`, secondID).Scan(&reportStatus); err != nil {
		t.Fatal(err)
	}
	if err := api.db.QueryRow(`SELECT status FROM posts WHERE id = 'post_weekend'`).Scan(&postStatus); err != nil {
		t.Fatal(err)
	}
	if reportStatus != "open" || postStatus != "published" {
		t.Fatalf("failed moderation transaction leaked state: report=%s post=%s", reportStatus, postStatus)
	}
}

func TestModerationRemovalMaintainsCommentAndRepostRelations(t *testing.T) {
	api := newTestAPI(t)
	demoAccess, _ := login(t, api)
	forestAccess, _ := loginAs(t, api, "forest", "demo1234")
	if _, err := api.db.Exec(`UPDATE users SET role = 'moderator' WHERE id = 'usr_rain'`); err != nil {
		t.Fatal(err)
	}
	moderatorAccess, _ := loginAs(t, api, "rain", "demo1234")

	var beforeComments int
	if err := api.db.QueryRow(`SELECT comment_count FROM posts WHERE id = 'post_morning'`).Scan(&beforeComments); err != nil {
		t.Fatal(err)
	}
	commentReport := expectStatus(t, api.request(http.MethodPost, "/api/v1/reports", demoAccess, map[string]any{
		"target_type": "comment", "target_id": "cmt_seed", "reason": "这条评论需要审核移除",
	}, nil), http.StatusCreated)
	commentReportID := commentReport["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/reports/"+commentReportID+"/decision", moderatorAccess,
		map[string]any{"decision": "remove_content", "reason": "确认移除违规评论"}, nil), http.StatusOK)
	var afterComments int
	if err := api.db.QueryRow(`SELECT comment_count FROM posts WHERE id = 'post_morning'`).Scan(&afterComments); err != nil {
		t.Fatal(err)
	}
	if afterComments != beforeComments-1 {
		t.Fatalf("moderation comment removal drifted counter: before=%d after=%d", beforeComments, afterComments)
	}

	firstRepost := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/post_morning/repost", demoAccess, nil, nil), http.StatusOK)
	repostID := firstRepost["data"].(map[string]any)["id"].(string)
	var beforeReposts int
	if err := api.db.QueryRow(`SELECT repost_count FROM posts WHERE id = 'post_morning'`).Scan(&beforeReposts); err != nil {
		t.Fatal(err)
	}
	repostReport := expectStatus(t, api.request(http.MethodPost, "/api/v1/reports", forestAccess, map[string]any{
		"target_type": "post", "target_id": repostID, "reason": "这条转发需要审核移除",
	}, nil), http.StatusCreated)
	repostReportID := repostReport["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/reports/"+repostReportID+"/decision", moderatorAccess,
		map[string]any{"decision": "remove_content", "reason": "确认移除违规转发"}, nil), http.StatusOK)
	var afterReposts, relationCount int
	if err := api.db.QueryRow(`SELECT repost_count FROM posts WHERE id = 'post_morning'`).Scan(&afterReposts); err != nil {
		t.Fatal(err)
	}
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM reposts WHERE repost_post_id = ?`, repostID).Scan(&relationCount); err != nil {
		t.Fatal(err)
	}
	if afterReposts != beforeReposts-1 || relationCount != 0 {
		t.Fatalf("moderation repost removal drifted relation: before=%d after=%d relation=%d", beforeReposts, afterReposts, relationCount)
	}
	secondRepost := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/post_morning/repost", demoAccess, nil, nil), http.StatusOK)
	if secondRepost["data"].(map[string]any)["id"] == repostID {
		t.Fatal("repost relation was not reusable after moderated removal")
	}
}

func TestCommunityHappyPathAndSessionRotation(t *testing.T) {
	api := newTestAPI(t)
	access, refresh := login(t, api)

	feed := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts?feed=recommended&limit=2", access, nil, nil), http.StatusOK)
	if len(feed["data"].([]any)) != 2 {
		t.Fatalf("expected two feed items: %#v", feed)
	}
	firstFeedPost := feed["data"].([]any)[0].(map[string]any)
	if firstFeedPost["content"] == "" || firstFeedPost["likes"] == nil || firstFeedPost["author"].(map[string]any)["name"] == "" {
		t.Fatalf("frontend compatibility fields missing: %#v", firstFeedPost)
	}
	if feed["meta"].(map[string]any)["request_id"] == "" {
		t.Fatal("success response is missing request_id")
	}
	for _, kind := range []string{"recommend", "latest", "global", "following"} {
		expectStatus(t, api.request(http.MethodGet, "/api/v1/feeds/"+kind+"?limit=1", access, nil, nil), http.StatusOK)
	}

	create := func() *httptest.ResponseRecorder {
		return api.request(http.MethodPost, "/api/v1/posts", access, map[string]any{
			"content": "这是由 API 集成测试发布的一条社区动态。", "visibility": "public",
		}, map[string]string{"Idempotency-Key": "happy-create-1"})
	}
	created := expectStatus(t, create(), http.StatusCreated)
	postID := created["data"].(map[string]any)["id"].(string)
	replayed := expectStatus(t, create(), http.StatusOK)
	if replayed["data"].(map[string]any)["id"] != postID {
		t.Fatal("idempotent create returned a different post")
	}

	commentResponse := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/"+postID+"/comments", access,
		map[string]any{"content": "第一条评论"}, map[string]string{"Idempotency-Key": "happy-comment-1"}), http.StatusCreated)
	commentID := commentResponse["data"].(map[string]any)["id"].(string)
	commentReplay := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/"+postID+"/comments", access,
		map[string]any{"content": "第一条评论"}, map[string]string{"Idempotency-Key": "happy-comment-1"}), http.StatusOK)
	if commentReplay["data"].(map[string]any)["id"] != commentID {
		t.Fatal("idempotent comment returned a different comment")
	}
	reply := expectStatus(t, api.request(http.MethodPost, fmt.Sprintf("/api/v1/posts/%s/comments/%s/replies", postID, commentID), access,
		map[string]any{"content": "回复第一条评论"}, nil), http.StatusCreated)
	if reply["data"].(map[string]any)["parent_id"] != commentID {
		t.Fatal("reply did not retain parent_id")
	}

	firstLike := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/reactions/like", access, nil, nil), http.StatusOK)
	secondLike := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/reactions/like", access, nil, nil), http.StatusOK)
	if firstLike["data"].(map[string]any)["changed"] != true || secondLike["data"].(map[string]any)["changed"] != false {
		t.Fatal("like PUT is not idempotent")
	}
	detail := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID, access, nil, nil), http.StatusOK)
	if detail["data"].(map[string]any)["like_count"] != float64(1) || detail["data"].(map[string]any)["comment_count"] != float64(2) {
		t.Fatalf("transactional counters are wrong: %#v", detail["data"])
	}

	firstRepost := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/reposts", access, nil, nil), http.StatusOK)
	secondRepost := expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/reposts", access, nil, nil), http.StatusOK)
	if firstRepost["data"].(map[string]any)["id"] != secondRepost["data"].(map[string]any)["id"] {
		t.Fatal("repost PUT is not idempotent")
	}

	rotated := expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": refresh}, nil), http.StatusOK)
	newAccess := rotated["data"].(map[string]any)["access_token"].(string)
	oldRefresh := api.request(http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": refresh}, nil)
	expectStatus(t, oldRefresh, http.StatusUnauthorized)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/logout", newAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", newAccess, map[string]any{"content": "不应发布"}, nil), http.StatusUnauthorized)
}

func TestUnauthorizedAndValidationErrors(t *testing.T) {
	api := newTestAPI(t)
	unauthorized := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", "", map[string]any{"content": "需要登录"}, nil), http.StatusUnauthorized)
	errorBody := unauthorized["error"].(map[string]any)
	if errorBody["code"] != "authentication_required" || errorBody["request_id"] == "" {
		t.Fatalf("unexpected structured auth error: %#v", errorBody)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts?feed=following", "", nil, nil), http.StatusUnauthorized)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/login", "", map[string]any{"identity": "demo", "password": "wrong"}, nil), http.StatusUnauthorized)

	access, _ := login(t, api)
	invalid := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", access, map[string]any{"content": ""}, nil), http.StatusUnprocessableEntity)
	fields := invalid["error"].(map[string]any)["field_errors"].(map[string]any)
	if fields["title"] == nil || fields["body"] == nil {
		t.Fatalf("validation fields missing: %#v", fields)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts?limit=500", "", nil, nil), http.StatusUnprocessableEntity)
}

func TestLikeTransactionRollsBackOnCounterFailure(t *testing.T) {
	api := newTestAPI(t)
	access, _ := login(t, api)
	created := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", access,
		map[string]any{"content": "用于验证事务回滚的动态内容。"}, nil), http.StatusCreated)
	postID := created["data"].(map[string]any)["id"].(string)
	trigger := fmt.Sprintf(`CREATE TRIGGER force_like_failure BEFORE UPDATE OF like_count ON posts WHEN NEW.id = '%s' BEGIN SELECT RAISE(ABORT, 'forced counter failure'); END`, postID)
	if _, err := api.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPut, "/api/v1/posts/"+postID+"/reactions/like", access, nil, nil), http.StatusInternalServerError)

	var reactions, count int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM reactions WHERE post_id = ?`, postID).Scan(&reactions); err != nil {
		t.Fatal(err)
	}
	if err := api.db.QueryRow(`SELECT like_count FROM posts WHERE id = ?`, postID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if reactions != 0 || count != 0 {
		t.Fatalf("failed transaction leaked state: reactions=%d count=%d", reactions, count)
	}
}

func TestPrivateMessagingMembershipIdempotencyAndReconnectCursor(t *testing.T) {
	api := newTestAPI(t)
	demoAccess, _ := loginAs(t, api, "demo", "demo1234")
	rainAccess, _ := loginAs(t, api, "rain", "demo1234")
	forestAccess, _ := loginAs(t, api, "forest", "demo1234")

	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", "", map[string]any{
		"member_ids": []string{"usr_rain"},
	}, nil), http.StatusUnauthorized)
	created := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", demoAccess, map[string]any{
		"member_ids": []string{"usr_rain"}, "title": "本地私信",
	}, nil), http.StatusCreated)
	conversationID := created["data"].(map[string]any)["id"].(string)
	reversed := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", rainAccess, map[string]any{
		"member_ids": []string{"usr_demo"},
	}, nil), http.StatusOK)
	if reversed["data"].(map[string]any)["id"] != conversationID {
		t.Fatalf("reversed member set created a duplicate conversation: %#v", reversed)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", demoAccess, map[string]any{
		"member_ids": []string{"usr_demo"},
	}, nil), http.StatusUnprocessableEntity)

	for _, path := range []string{
		"/api/v1/conversations/" + conversationID,
		"/api/v1/conversations/" + conversationID + "/messages",
	} {
		expectStatus(t, api.request(http.MethodGet, path, forestAccess, nil, nil), http.StatusNotFound)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversationID+"/messages", forestAccess, map[string]any{
		"client_message_id": "outsider-1", "body": "不能发送",
	}, nil), http.StatusNotFound)

	messageIDs := []string{}
	first := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversationID+"/messages", demoAccess, map[string]any{
		"client_message_id": "device-a-1", "body": "第一条消息",
	}, nil), http.StatusCreated)
	firstID := first["data"].(map[string]any)["id"].(string)
	messageIDs = append(messageIDs, firstID)
	replayResponse := api.request(http.MethodPost, "/api/v1/conversations/"+conversationID+"/messages", demoAccess, map[string]any{
		"client_message_id": "device-a-1", "body": "第一条消息",
	}, nil)
	replay := expectStatus(t, replayResponse, http.StatusOK)
	if replayResponse.Header().Get("Idempotency-Replayed") != "true" || replay["data"].(map[string]any)["id"] != firstID {
		t.Fatalf("message replay did not return the exact original: %#v", replay)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversationID+"/messages", demoAccess, map[string]any{
		"client_message_id": "device-a-1", "body": "不同负载",
	}, nil), http.StatusConflict)

	otherConversation := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", demoAccess, map[string]any{
		"member_ids": []string{"usr_forest"},
	}, nil), http.StatusCreated)["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+otherConversation+"/messages", demoAccess, map[string]any{
		"client_message_id": "device-a-1", "body": "第一条消息",
	}, nil), http.StatusConflict)
	if _, err := api.db.Exec(`CREATE TRIGGER fail_message_notification BEFORE INSERT ON notifications WHEN NEW.type = 'message' BEGIN SELECT RAISE(ABORT, 'forced message notification failure'); END`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+otherConversation+"/messages", demoAccess, map[string]any{
		"client_message_id": "rollback-1", "body": "必须与通知一起回滚",
	}, nil), http.StatusInternalServerError)
	var rolledBackMessages int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, otherConversation).Scan(&rolledBackMessages); err != nil {
		t.Fatal(err)
	}
	if rolledBackMessages != 0 {
		t.Fatalf("notification failure left a partial message: %d", rolledBackMessages)
	}
	if _, err := api.db.Exec(`DROP TRIGGER fail_message_notification`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+otherConversation+"/messages", demoAccess, map[string]any{
		"client_message_id": "rollback-1", "body": "必须与通知一起回滚",
	}, nil), http.StatusCreated)

	for index := 2; index <= 3; index++ {
		payload := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversationID+"/messages", demoAccess, map[string]any{
			"client_message_id": fmt.Sprintf("device-a-%d", index), "body": fmt.Sprintf("第%d条消息", index),
		}, nil), http.StatusCreated)
		messageIDs = append(messageIDs, payload["data"].(map[string]any)["id"].(string))
	}
	pageOne := expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations/"+conversationID+"/messages?limit=2", demoAccess, nil, nil), http.StatusOK)
	pageOneData := pageOne["data"].([]any)
	if len(pageOneData) != 2 {
		t.Fatalf("first message page mismatch: %#v", pageOne)
	}
	cursor := pageOne["page"].(map[string]any)["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("first message page did not provide a cursor")
	}
	pageTwo := expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations/"+conversationID+"/messages?limit=2&cursor="+url.QueryEscape(cursor), demoAccess, nil, nil), http.StatusOK)
	if len(pageTwo["data"].([]any)) != 1 {
		t.Fatalf("second message page mismatch: %#v", pageTwo)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations/"+conversationID+"/messages?cursor="+url.QueryEscape(cursor), rainAccess, nil, nil), http.StatusUnprocessableEntity)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations/"+conversationID+"/messages?limit=2junk", demoAccess, nil, nil), http.StatusUnprocessableEntity)

	var messageCount, notificationCount int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, conversationID).Scan(&messageCount); err != nil {
		t.Fatal(err)
	}
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = 'usr_rain' AND type = 'message'`).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if messageCount != 3 || notificationCount != 3 {
		t.Fatalf("replay duplicated durable facts: messages=%d notifications=%d", messageCount, notificationCount)
	}

	eventsOne := expectStatus(t, api.request(http.MethodGet, "/api/v1/events?limit=2", rainAccess, nil, nil), http.StatusOK)
	if len(eventsOne["data"].([]any)) != 2 || eventsOne["page"].(map[string]any)["has_more"] != true {
		t.Fatalf("first reconnect event page mismatch: %#v", eventsOne)
	}
	eventCursor := eventsOne["page"].(map[string]any)["next_cursor"].(string)
	eventsTwo := expectStatus(t, api.request(http.MethodGet, "/api/v1/events?limit=2&cursor="+url.QueryEscape(eventCursor), rainAccess, nil, nil), http.StatusOK)
	if len(eventsTwo["data"].([]any)) != 1 {
		t.Fatalf("second reconnect event page mismatch: %#v", eventsTwo)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/events?cursor="+url.QueryEscape(eventCursor), demoAccess, nil, nil), http.StatusUnprocessableEntity)
	senderEvents := expectStatus(t, api.request(http.MethodGet, "/api/v1/events", demoAccess, nil, nil), http.StatusOK)
	if len(senderEvents["data"].([]any)) != 0 {
		t.Fatalf("sender received its own message event: %#v", senderEvents)
	}
}

func TestAdminUserContentAndTaxonomyOperations(t *testing.T) {
	api := newTestAPI(t)
	if _, err := api.db.Exec(`UPDATE users SET role = 'admin' WHERE id = 'usr_demo'; UPDATE users SET role = 'moderator' WHERE id = 'usr_rain'`); err != nil {
		t.Fatal(err)
	}
	adminAccess, _ := loginAs(t, api, "demo", "demo1234")
	moderatorAccess, _ := loginAs(t, api, "rain", "demo1234")
	memberAccess, _ := loginAs(t, api, "forest", "demo1234")

	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/users", "", nil, nil), http.StatusUnauthorized)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/users", memberAccess, nil, nil), http.StatusForbidden)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/users", moderatorAccess, nil, nil), http.StatusForbidden)
	users := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/users?status=active&role=member", adminAccess, nil, nil), http.StatusOK)
	if len(users["data"].([]any)) != 1 || strings.Contains(users["data"].([]any)[0].(map[string]any)["email"].(string), "password_hash") {
		t.Fatalf("admin user filter mismatch: %#v", users)
	}
	literalWildcard := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/users?q=%25", adminAccess, nil, nil), http.StatusOK)
	if len(literalWildcard["data"].([]any)) != 0 {
		t.Fatalf("admin user search did not escape wildcard: %#v", literalWildcard)
	}

	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/content?type=post&status=published&author_id=usr_rain", memberAccess, nil, nil), http.StatusForbidden)
	moderatedContent := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/content?type=post&status=published&author_id=usr_rain", moderatorAccess, nil, nil), http.StatusOK)
	if len(moderatedContent["data"].([]any)) == 0 {
		t.Fatalf("moderator content filter returned no seeded post: %#v", moderatedContent)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/content?report_status=open", moderatorAccess, nil, nil), http.StatusOK)

	if _, err := api.db.Exec(`CREATE TRIGGER fail_user_status_audit BEFORE INSERT ON audit_events WHEN NEW.action = 'user_status_update' BEGIN SELECT RAISE(ABORT, 'forced audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/users/usr_forest/status", adminAccess, map[string]any{
		"status": "suspended", "reason": "验证审计失败回滚",
	}, nil), http.StatusInternalServerError)
	var status string
	if err := api.db.QueryRow(`SELECT status FROM users WHERE id = 'usr_forest'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Fatalf("audit failure did not roll back user status: %s", status)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", memberAccess, nil, nil), http.StatusOK)
	if _, err := api.db.Exec(`DROP TRIGGER fail_user_status_audit`); err != nil {
		t.Fatal(err)
	}

	statusChange := expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/users/usr_forest/status", adminAccess, map[string]any{
		"status": "suspended", "reason": "本地管理测试停用",
	}, nil), http.StatusOK)
	if statusChange["data"].(map[string]any)["changed"] != true {
		t.Fatalf("user status did not change: %#v", statusChange)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", memberAccess, nil, nil), http.StatusUnauthorized)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/users/usr_forest/status", adminAccess, map[string]any{
		"status": "active", "reason": "本地管理测试恢复",
	}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", memberAccess, nil, nil), http.StatusUnauthorized)
	memberAccess, _ = loginAs(t, api, "forest", "demo1234")
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/users/usr_demo/status", adminAccess, map[string]any{
		"status": "suspended", "reason": "不允许自停用",
	}, nil), http.StatusConflict)

	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/categories", memberAccess, map[string]any{"slug": "local-news", "name": "本地新闻"}, nil), http.StatusForbidden)
	category := expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/categories", adminAccess, map[string]any{
		"slug": "local-news", "name": "本地新闻", "description": "社区周边与线下活动资讯",
		"image_url": "/api/v1/media/category-news", "background_url": "https://static.example.test/categories/news.webp",
	}, nil), http.StatusCreated)
	categoryID := category["data"].(map[string]any)["id"].(string)
	if category["data"].(map[string]any)["description"] != "社区周边与线下活动资讯" || category["data"].(map[string]any)["image_url"] != "/api/v1/media/category-news" {
		t.Fatalf("rich category fields were not created: %#v", category)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/categories", adminAccess, map[string]any{
		"slug": "unsafe-art", "name": "不安全图片", "image_url": "javascript:alert(1)",
	}, nil), http.StatusUnprocessableEntity)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/categories", adminAccess, map[string]any{"slug": "LOCAL-NEWS", "name": "另一个名字"}, nil), http.StatusConflict)
	updatedCategory := expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/categories/"+categoryID, adminAccess, map[string]any{
		"name": "本地资讯", "description": "更新后的社区资讯", "background_url": "/media/local-news-cover.webp",
	}, nil), http.StatusOK)
	if updatedCategory["data"].(map[string]any)["name"] != "本地资讯" || updatedCategory["data"].(map[string]any)["description"] != "更新后的社区资讯" {
		t.Fatalf("category update mismatch: %#v", updatedCategory)
	}
	publicCategories := expectStatus(t, api.request(http.MethodGet, "/api/v1/categories", "", nil, nil), http.StatusOK)
	var publicCategory map[string]any
	for _, item := range publicCategories["data"].([]any) {
		if candidate := item.(map[string]any); candidate["id"] == categoryID {
			publicCategory = candidate
		}
	}
	if publicCategory == nil || publicCategory["description"] != "更新后的社区资讯" || publicCategory["background_url"] != "/media/local-news-cover.webp" {
		t.Fatalf("public category presentation mismatch: %#v", publicCategories)
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/admin/categories/cat_tech", adminAccess, map[string]any{"reason": "仍在使用的分类"}, nil), http.StatusConflict)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/admin/categories/"+categoryID, adminAccess, map[string]any{"reason": "清理测试分类"}, nil), http.StatusOK)

	tag := expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/tags", adminAccess, map[string]any{"slug": "chat", "name": "聊天"}, nil), http.StatusCreated)
	tagID := tag["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/tags/"+tagID, adminAccess, map[string]any{"name": "私信"}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/admin/tags/"+tagID, adminAccess, map[string]any{"reason": "清理测试标签"}, nil), http.StatusOK)

	audits := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/audit-events?limit=20", adminAccess, nil, nil), http.StatusOK)
	if len(audits["data"].([]any)) < 6 {
		t.Fatalf("admin mutations were not audited: %#v", audits)
	}
}

func TestPublicProfilesBlocksCategoryFollowsFiltersAndCover(t *testing.T) {
	api := newTestAPI(t)
	demoAccess, _ := loginAs(t, api, "demo", "demo1234")
	rainAccess, _ := loginAs(t, api, "rain", "demo1234")
	forestAccess, _ := loginAs(t, api, "forest", "demo1234")

	profile := expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_rain", "", nil, nil), http.StatusOK)
	if profile["data"].(map[string]any)["handle"] != "rain" {
		t.Fatalf("public profile mismatch: %#v", profile)
	}
	followers := expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_rain/followers", demoAccess, nil, nil), http.StatusOK)
	if len(followers["data"].([]any)) != 1 {
		t.Fatalf("seeded follower list mismatch: %#v", followers)
	}

	firstBlock := expectStatus(t, api.request(http.MethodPut, "/api/v1/users/usr_rain/block", demoAccess, nil, nil), http.StatusOK)
	secondBlock := expectStatus(t, api.request(http.MethodPut, "/api/v1/users/usr_rain/block", demoAccess, nil, nil), http.StatusOK)
	if firstBlock["data"].(map[string]any)["changed"] != true || secondBlock["data"].(map[string]any)["changed"] != false {
		t.Fatal("block PUT is not idempotent")
	}
	blocks := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/blocks", demoAccess, nil, nil), http.StatusOK)
	if len(blocks["data"].([]any)) != 1 || blocks["data"].([]any)[0].(map[string]any)["id"] != "usr_rain" {
		t.Fatalf("blocked user list mismatch: %#v", blocks)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_rain", demoAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/post_morning", demoAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/users/usr_rain/follow", demoAccess, nil, nil), http.StatusConflict)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_demo", rainAccess, nil, nil), http.StatusNotFound)
	following := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts?feed=following", demoAccess, nil, nil), http.StatusOK)
	if len(following["data"].([]any)) != 0 {
		t.Fatalf("block did not remove follows/feed visibility: %#v", following)
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/users/usr_rain/block", demoAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/users/usr_rain/follow", demoAccess, nil, nil), http.StatusOK)

	firstCategoryFollow := expectStatus(t, api.request(http.MethodPut, "/api/v1/categories/cat_tech/follow", demoAccess, nil, nil), http.StatusOK)
	secondCategoryFollow := expectStatus(t, api.request(http.MethodPut, "/api/v1/categories/cat_tech/follow", demoAccess, nil, nil), http.StatusOK)
	if firstCategoryFollow["data"].(map[string]any)["changed"] != true || secondCategoryFollow["data"].(map[string]any)["changed"] != false {
		t.Fatal("category follow PUT is not idempotent")
	}
	categoryFollows := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/category-follows", demoAccess, nil, nil), http.StatusOK)
	if len(categoryFollows["data"].([]any)) != 1 {
		t.Fatalf("category follow list mismatch: %#v", categoryFollows)
	}
	categoryFeed := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts?feed=latest&category_id=cat_tech", "", nil, nil), http.StatusOK)
	tagFeed := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts?feed=latest&tag_id=tag_go", "", nil, nil), http.StatusOK)
	if len(categoryFeed["data"].([]any)) != 1 || categoryFeed["data"].([]any)[0].(map[string]any)["id"] != "post_go" || len(tagFeed["data"].([]any)) != 1 {
		t.Fatalf("taxonomy feed filter mismatch: category=%#v tag=%#v", categoryFeed, tagFeed)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts?category_id=missing", "", nil, nil), http.StatusUnprocessableEntity)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/categories/cat_tech/follow", demoAccess, nil, nil), http.StatusOK)

	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	cover := expectStatus(t, api.multipart(http.MethodPut, "/api/v1/me/cover", demoAccess, "cover.png", png, map[string]string{"alt_text": "主页封面"}), http.StatusCreated)
	coverID := cover["data"].(map[string]any)["id"].(string)
	demoProfile := expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_demo", forestAccess, nil, nil), http.StatusOK)
	if demoProfile["data"].(map[string]any)["cover_url"] != "/api/v1/media/"+coverID {
		t.Fatalf("cover reference missing from profile: %#v", demoProfile)
	}
	coverMedia := api.request(http.MethodGet, "/api/v1/media/"+coverID, "", nil, nil)
	if coverMedia.Code != http.StatusOK || !strings.HasPrefix(coverMedia.Header().Get("Cache-Control"), "public") {
		t.Fatalf("public cover cache policy mismatch: status=%d cache=%q", coverMedia.Code, coverMedia.Header().Get("Cache-Control"))
	}
}

func TestRestrictedMediaUsesPrivateCacheAndTracksAccess(t *testing.T) {
	api := newTestAPI(t)
	rainAccess, _ := loginAs(t, api, "rain", "demo1234")
	demoAccess, _ := loginAs(t, api, "demo", "demo1234")
	forestAccess, _ := loginAs(t, api, "forest", "demo1234")
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	upload := expectStatus(t, api.multipart(http.MethodPost, "/api/v1/uploads", rainAccess, "followers.png", png, nil), http.StatusCreated)
	assetID := upload["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", rainAccess, map[string]any{
		"content": "仅关注者媒体缓存边界", "visibility": "followers", "media_ids": []string{assetID},
	}, nil), http.StatusCreated)
	for name, token := range map[string]string{"owner": rainAccess, "follower": demoAccess} {
		response := api.request(http.MethodGet, "/api/v1/media/"+assetID, token, nil, nil)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s restricted media cache mismatch: status=%d cache=%q", name, response.Code, response.Header().Get("Cache-Control"))
		}
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/media/"+assetID, forestAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/users/usr_rain/block", demoAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/media/"+assetID, demoAccess, nil, nil), http.StatusNotFound)
}

func TestDeviceSessionListAndOwnerScopedRevocation(t *testing.T) {
	api := newTestAPI(t)
	firstAccess, _ := loginAs(t, api, "demo", "demo1234")
	secondAccess, _ := loginAs(t, api, "demo", "demo1234")
	rainAccess, _ := loginAs(t, api, "rain", "demo1234")

	sessions := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/sessions", secondAccess, nil, nil), http.StatusOK)
	var oldSessionID string
	currentCount := 0
	for _, raw := range sessions["data"].([]any) {
		item := raw.(map[string]any)
		if item["current"].(bool) {
			currentCount++
		} else if oldSessionID == "" {
			oldSessionID = item["id"].(string)
		}
	}
	if currentCount != 1 || oldSessionID == "" {
		t.Fatalf("session list did not identify current/other sessions: %#v", sessions)
	}
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/me/sessions/"+oldSessionID, rainAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/me/sessions/"+oldSessionID, secondAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", firstAccess, nil, nil), http.StatusUnauthorized)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", secondAccess, nil, nil), http.StatusOK)
}

func TestConversationReadReceiptAndSafeLeaveLifecycle(t *testing.T) {
	api := newTestAPI(t)
	demoAccess, _ := loginAs(t, api, "demo", "demo1234")
	rainAccess, _ := loginAs(t, api, "rain", "demo1234")
	conversation := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", demoAccess, map[string]any{
		"member_ids": []string{"usr_rain"},
	}, nil), http.StatusCreated)
	conversationID := conversation["data"].(map[string]any)["id"].(string)
	messageIDs := []string{}
	for index := 1; index <= 2; index++ {
		message := expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversationID+"/messages", demoAccess, map[string]any{
			"client_message_id": fmt.Sprintf("receipt-%d", index), "body": fmt.Sprintf("回执消息%d", index),
		}, nil), http.StatusCreated)
		messageIDs = append(messageIDs, message["data"].(map[string]any)["id"].(string))
	}
	latest := expectStatus(t, api.request(http.MethodPut, "/api/v1/conversations/"+conversationID+"/read", rainAccess, map[string]any{
		"message_id": messageIDs[1],
	}, nil), http.StatusOK)
	if latest["data"].(map[string]any)["changed"] != true {
		t.Fatalf("latest receipt did not advance: %#v", latest)
	}
	older := expectStatus(t, api.request(http.MethodPut, "/api/v1/conversations/"+conversationID+"/read", rainAccess, map[string]any{
		"message_id": messageIDs[0],
	}, nil), http.StatusOK)
	if older["data"].(map[string]any)["changed"] != false || older["data"].(map[string]any)["last_read_message_id"] != messageIDs[1] {
		t.Fatalf("receipt regressed: %#v", older)
	}
	detail := expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations/"+conversationID, rainAccess, nil, nil), http.StatusOK)
	if detail["data"].(map[string]any)["last_read_message_id"] != messageIDs[1] {
		t.Fatalf("conversation did not expose viewer receipt: %#v", detail)
	}

	expectStatus(t, api.request(http.MethodDelete, "/api/v1/conversations/"+conversationID+"/membership", rainAccess, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations/"+conversationID, rainAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/conversations/"+conversationID+"/messages", rainAccess, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations/"+conversationID+"/messages", demoAccess, map[string]any{
		"client_message_id": "after-leave", "body": "不能发给已退出成员",
	}, nil), http.StatusConflict)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/conversations", demoAccess, map[string]any{
		"member_ids": []string{"usr_rain"},
	}, nil), http.StatusConflict)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/conversations/"+conversationID+"/membership", demoAccess, nil, nil), http.StatusOK)
	var status string
	if err := api.db.QueryRow(`SELECT status FROM conversations WHERE id = ?`, conversationID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "closed" {
		t.Fatalf("empty conversation was not closed: %s", status)
	}
}

func TestVersionedHomepageOperationsConfigRBACAndAudit(t *testing.T) {
	api := newTestAPI(t)
	if _, err := api.db.Exec(`UPDATE users SET role = 'admin' WHERE id = 'usr_demo'`); err != nil {
		t.Fatal(err)
	}
	adminAccess, _ := loginAs(t, api, "demo", "demo1234")
	memberAccess, _ := loginAs(t, api, "forest", "demo1234")
	initial := expectStatus(t, api.request(http.MethodGet, "/api/v1/homepage", "", nil, nil), http.StatusOK)
	if initial["data"].(map[string]any)["version"] != float64(1) {
		t.Fatalf("default homepage version mismatch: %#v", initial)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/homepage", memberAccess, nil, nil), http.StatusForbidden)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/homepage", adminAccess, map[string]any{}, nil), http.StatusPreconditionRequired)
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	carouselUpload := expectStatus(t, api.multipart(http.MethodPost, "/api/v1/uploads", adminAccess, "carousel.png", png, nil), http.StatusCreated)
	carouselMediaID := carouselUpload["data"].(map[string]any)["id"].(string)
	updated := expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/homepage", adminAccess, map[string]any{
		"status":        "published",
		"carousel":      []map[string]any{{"title": "社区新鲜事", "summary": "本地运营配置", "link_url": "/posts", "media_id": carouselMediaID, "enabled": true}},
		"announcements": []map[string]any{{"text": "欢迎来到社区", "link_url": "/", "enabled": true}},
	}, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	if updated["data"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("homepage version did not increment: %#v", updated)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/homepage", adminAccess, map[string]any{
		"status": "published", "carousel": []any{}, "announcements": []any{},
	}, map[string]string{"If-Match": `"1"`}), http.StatusConflict)
	public := expectStatus(t, api.request(http.MethodGet, "/api/v1/homepage", "", nil, nil), http.StatusOK)
	if len(public["data"].(map[string]any)["payload"].(map[string]any)["carousel"].([]any)) != 1 {
		t.Fatalf("published homepage was not readable: %#v", public)
	}
	carouselMedia := api.request(http.MethodGet, "/api/v1/media/"+carouselMediaID, "", nil, nil)
	if carouselMedia.Code != http.StatusOK || !strings.HasPrefix(carouselMedia.Header().Get("Cache-Control"), "public") {
		t.Fatalf("published carousel media was not publicly cacheable: status=%d cache=%q", carouselMedia.Code, carouselMedia.Header().Get("Cache-Control"))
	}

	if _, err := api.db.Exec(`CREATE TRIGGER fail_homepage_audit BEFORE INSERT ON audit_events WHEN NEW.action = 'homepage_config_update' BEGIN SELECT RAISE(ABORT, 'forced homepage audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/homepage", adminAccess, map[string]any{
		"status": "published", "carousel": []any{}, "announcements": []any{},
	}, map[string]string{"If-Match": `"2"`}), http.StatusInternalServerError)
	var version int
	if err := api.db.QueryRow(`SELECT version FROM operations_configs WHERE id = 'homepage'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("audit failure did not roll back homepage version: %d", version)
	}
	if _, err := api.db.Exec(`DROP TRIGGER fail_homepage_audit`); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'homepage_config_update'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("homepage audit count mismatch: %d", audits)
	}
}

func TestRecoveryCodesAreOneTimeAndRevokeSessions(t *testing.T) {
	api := newTestAPI(t)
	registered := expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"handle": "recoverable", "email": "recoverable@example.test", "password": "before-recovery-1",
	}, nil), http.StatusCreated)
	data := registered["data"].(map[string]any)
	access := data["access_token"].(string)
	codes := data["recovery_codes"].([]any)
	if len(codes) != 8 {
		t.Fatalf("registration did not issue eight recovery codes: %#v", data)
	}
	code := codes[0].(string)
	var stored string
	if err := api.db.QueryRow(`SELECT code_hash FROM recovery_codes WHERE user_id = 'usr_missing' OR user_id = (SELECT id FROM users WHERE handle = 'recoverable') LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == code || strings.Contains(stored, strings.ReplaceAll(code, "-", "")) {
		t.Fatal("plaintext recovery code was persisted")
	}
	recovered := expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/recover", "", map[string]any{
		"identity": "recoverable", "recovery_code": code, "new_password": "after-recovery-2",
	}, nil), http.StatusOK)
	if len(recovered["data"].(map[string]any)["recovery_codes"].([]any)) != 8 {
		t.Fatalf("recovery did not rotate codes: %#v", recovered)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", access, nil, nil), http.StatusUnauthorized)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"identity": "recoverable", "password": "before-recovery-1",
	}, nil), http.StatusUnauthorized)
	newAccess, _ := loginAs(t, api, "recoverable", "after-recovery-2")
	expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/recover", "", map[string]any{
		"identity": "recoverable", "recovery_code": code, "new_password": "must-not-work-3",
	}, nil), http.StatusUnauthorized)
	rotated := expectStatus(t, api.request(http.MethodPost, "/api/v1/me/recovery-codes/rotate", newAccess, map[string]any{
		"current_password": "after-recovery-2",
	}, nil), http.StatusOK)
	if len(rotated["data"].(map[string]any)["recovery_codes"].([]any)) != 8 {
		t.Fatalf("authenticated rotation did not return eight codes: %#v", rotated)
	}
}

func TestPostRevisionOwnerActivityAndModerationWorkflow(t *testing.T) {
	api := newTestAPI(t)
	if _, err := api.db.Exec(`UPDATE users SET role = 'moderator' WHERE id = 'usr_demo'`); err != nil {
		t.Fatal(err)
	}
	moderatorAccess, _ := loginAs(t, api, "demo", "demo1234")
	memberAccess, _ := loginAs(t, api, "forest", "demo1234")

	created := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", memberAccess, map[string]any{
		"content": "第一版正文用于修订历史", "tag_ids": []string{"tag_go"},
	}, nil), http.StatusCreated)
	postID := created["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+postID, memberAccess, map[string]any{
		"content": "第二版正文用于修订历史",
	}, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	revisions := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID+"/revisions", memberAccess, nil, nil), http.StatusOK)
	revision := revisions["data"].([]any)[0].(map[string]any)
	if revision["version"] != float64(1) || revision["body"] != "第一版正文用于修订历史" {
		t.Fatalf("revision snapshot mismatch: %#v", revision)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID+"/revisions", moderatorAccess, nil, nil), http.StatusNotFound)
	activityResponse := api.request(http.MethodGet, "/api/v1/me/activity", memberAccess, nil, nil)
	expectStatus(t, activityResponse, http.StatusOK)
	if strings.Contains(activityResponse.Body.String(), "第二版正文") || strings.Contains(activityResponse.Body.String(), "forest@") {
		t.Fatalf("owner activity leaked content or contact fields: %s", activityResponse.Body.String())
	}

	pending := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", memberAccess, map[string]any{
		"content": "需要审核后才能出现在公开信息流", "status": "pending",
	}, nil), http.StatusCreated)
	pendingID := pending["data"].(map[string]any)["id"].(string)
	if pending["data"].(map[string]any)["status"] != "pending" {
		t.Fatalf("pending post state mismatch: %#v", pending)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+pendingID, "", nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/posts/"+pendingID+"/moderation", memberAccess, map[string]any{
		"decision": "published", "reason": "成员不能审核自己",
	}, nil), http.StatusForbidden)
	queue := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/content?type=post&status=pending", moderatorAccess, nil, nil), http.StatusOK)
	if len(queue["data"].([]any)) != 1 {
		t.Fatalf("pending moderation queue mismatch: %#v", queue)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/posts/"+pendingID+"/moderation", moderatorAccess, map[string]any{
		"decision": "published", "reason": "内容符合社区规则",
	}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/posts/"+pendingID+"/controls", moderatorAccess, map[string]any{
		"pinned": true, "recommended": true, "reason": "本周社区精选",
	}, nil), http.StatusOK)
	publicPost := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+pendingID, "", nil, nil), http.StatusOK)
	if publicPost["data"].(map[string]any)["pinned"] != true || publicPost["data"].(map[string]any)["recommended"] != true {
		t.Fatalf("feed controls were not reflected on post: %#v", publicPost)
	}
	feed := expectStatus(t, api.request(http.MethodGet, "/api/v1/feeds/recommend?limit=1", "", nil, nil), http.StatusOK)
	if feed["data"].([]any)[0].(map[string]any)["id"] != pendingID {
		t.Fatalf("pinned post was not safely prioritized: %#v", feed)
	}
	var audits int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE target_id = ? AND action IN ('post_moderation_decision','post_feed_controls_update')`, pendingID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 2 {
		t.Fatalf("moderation audit count mismatch: %d", audits)
	}

	rejected := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", memberAccess, map[string]any{
		"content": "用于验证拒绝路径的待审内容", "status": "pending",
	}, nil), http.StatusCreated)
	rejectedID := rejected["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/posts/"+rejectedID+"/moderation", moderatorAccess, map[string]any{
		"decision": "rejected", "reason": "测试拒绝状态转换",
	}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+rejectedID, "", nil, nil), http.StatusNotFound)
}

func TestSecurityHeadersMetricsAndLoginRateLimit(t *testing.T) {
	api := newTestAPI(t)
	health := api.request(http.MethodGet, "/healthz", "", nil, nil)
	expectStatus(t, health, http.StatusOK)
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
	} {
		if health.Header().Get(name) != want {
			t.Fatalf("security header %s=%q want=%q", name, health.Header().Get(name), want)
		}
	}
	metrics := expectStatus(t, api.request(http.MethodGet, "/metrics", "", nil, nil), http.StatusOK)
	if len(metrics["data"].(map[string]any)["requests"].([]any)) == 0 {
		t.Fatalf("metrics did not include completed request: %#v", metrics)
	}
	for attempt := 0; attempt < 10; attempt++ {
		expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/login", "", map[string]any{
			"identity": "missing-rate-limit-user", "password": "not-a-password",
		}, nil), http.StatusUnauthorized)
	}
	limitedResponse := api.request(http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"identity": "missing-rate-limit-user", "password": "not-a-password",
	}, nil)
	limited := expectStatus(t, limitedResponse, http.StatusTooManyRequests)
	if limited["error"].(map[string]any)["code"] != "rate_limit_exceeded" || limitedResponse.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit response mismatch: %#v headers=%v", limited, limitedResponse.Header())
	}
}
