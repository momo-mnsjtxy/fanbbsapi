package app_test

import (
	"net/http"
	"testing"
)

func TestAdminUserWorkspaceSmoke(t *testing.T) {
	api := newTestAPI(t)
	if _, err := api.db.Exec(`UPDATE users SET role='admin' WHERE id='usr_demo'`); err != nil {
		t.Fatal(err)
	}
	admin, _ := login(t, api)
	member, _ := loginAs(t, api, "rain", "demo1234")
	own := expectStatus(t, api.request(http.MethodGet, "/api/v1/me", member, nil, nil), http.StatusOK)["data"].(map[string]any)
	id := own["id"].(string)
	path := "/api/v1/admin/users/" + id
	expectStatus(t, api.request(http.MethodGet, path, member, nil, nil), http.StatusForbidden)
	detail := expectStatus(t, api.request(http.MethodGet, path, admin, nil, nil), http.StatusOK)["data"].(map[string]any)
	if detail["active_sessions"].(float64) < 1 {
		t.Fatal("active sessions missing")
	}
	expectStatus(t, api.request(http.MethodPost, path+"/sessions/revoke", member, map[string]any{"reason": "This should not be allowed"}, nil), http.StatusForbidden)
	expectStatus(t, api.request(http.MethodPost, path+"/sessions/revoke", admin, map[string]any{"reason": "Account owner requested device reset"}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", member, nil, nil), http.StatusUnauthorized)
	// Session revocation does not suspend the account; a new login succeeds.
	member, _ = loginAs(t, api, "rain", "demo1234")
	expectStatus(t, api.request(http.MethodPatch, path+"/status", admin, map[string]any{"status": "suspended", "expected_status": "suspended", "reason": "Stale account status snapshot"}, nil), http.StatusConflict)
	expectStatus(t, api.request(http.MethodPatch, path+"/status", admin, map[string]any{"status": "suspended", "expected_status": "active", "reason": "Pause account for review"}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", member, nil, nil), http.StatusUnauthorized)
	expectStatus(t, api.request(http.MethodPatch, path+"/status", admin, map[string]any{"status": "active", "expected_status": "suspended", "reason": "Account review complete"}, nil), http.StatusOK)
	history := expectStatus(t, api.request(http.MethodGet, path+"/moderation-history", admin, nil, nil), http.StatusOK)["data"].([]any)
	if len(history) != 3 {
		t.Fatalf("expected three account actions: %#v", history)
	}
	member, _ = loginAs(t, api, "rain", "demo1234")
	report := expectStatus(t, api.request(http.MethodPost, "/api/v1/reports", member, map[string]any{"target_type": "user", "target_id": "usr_demo", "reason": "Administrator account report"}, nil), http.StatusCreated)["data"].(map[string]any)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/reports/"+report["id"].(string)+"/decision", admin, map[string]any{"decision": "suspend_user", "reason": "Self suspension must be rejected"}, nil), http.StatusConflict)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/users/usr_demo/sessions/revoke", admin, map[string]any{"reason": "Do not revoke the current admin session here"}, nil), http.StatusConflict)
}
