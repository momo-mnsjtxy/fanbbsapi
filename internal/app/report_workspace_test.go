package app_test

import (
	"net/http"
	"testing"
)

func TestReportWorkspaceLifecycleSmoke(t *testing.T) {
	api := newTestAPI(t)
	author, _ := loginAs(t, api, "rain", "demo1234")
	if _, err := api.db.Exec(`UPDATE users SET role='moderator' WHERE id='usr_demo'`); err != nil {
		t.Fatal(err)
	}
	admin, _ := login(t, api)
	post := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", author, map[string]any{"content": "Context for reported comment"}, nil), http.StatusCreated)["data"].(map[string]any)
	postID := post["id"].(string)
	comment := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/"+postID+"/comments", author, map[string]any{"content": "Comment requiring review"}, nil), http.StatusCreated)["data"].(map[string]any)
	id := comment["id"].(string)
	report := expectStatus(t, api.request(http.MethodPost, "/api/v1/reports", admin, map[string]any{"target_type": "comment", "target_id": id, "reason": "Please inspect this comment"}, nil), http.StatusCreated)["data"].(map[string]any)
	reportID := report["id"].(string)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/reports/"+reportID, author, nil, nil), http.StatusForbidden)
	context := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/reports/"+reportID, admin, nil, nil), http.StatusOK)["data"].(map[string]any)["target"].(map[string]any)
	if context["body"] != "Comment requiring review" || context["post"].(map[string]any)["id"] != postID {
		t.Fatalf("missing comment context: %#v", context)
	}
	others := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/reports", author, nil, nil), http.StatusOK)["data"].([]any)
	if len(others) != 0 {
		t.Fatal("report leaked to another account")
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/posts/"+postID+"/comments/"+id, author, map[string]any{"body": "Edited comment requiring review"}, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	decision := map[string]any{"decision": "remove_content", "reason": "Reviewed complete comment context"}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/reports/"+reportID+"/decision", admin, decision, map[string]string{"If-Match": `"1"`}), http.StatusConflict)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/reports/"+reportID+"/decision", admin, decision, map[string]string{"If-Match": `"2"`}), http.StatusOK)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/reports/"+reportID+"/decision", admin, decision, map[string]string{"If-Match": `"2"`}), http.StatusConflict)
	mine := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/reports?status=actioned", admin, nil, nil), http.StatusOK)["data"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["decision_reason"] != decision["reason"] {
		t.Fatalf("missing reporter outcome: %#v", mine)
	}
	var count int
	if err := api.db.QueryRow(`SELECT comment_count FROM posts WHERE id=?`, postID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("comment count=%d err=%v", count, err)
	}
}
