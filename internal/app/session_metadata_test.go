package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDeviceSessionNamesOwnershipRotationAndValidation(t *testing.T) {
	api := newTestAPI(t)
	access, refresh := login(t, api)
	other, _ := loginAs(t, api, "rain", "demo1234")
	list := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/sessions", access, nil, nil), http.StatusOK)["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("unexpected sessions: %#v", list)
	}
	session := list[0].(map[string]any)
	id := session["id"].(string)
	if session["name"] != "" || session["last_active_at"] == "" {
		t.Fatalf("missing metadata defaults: %#v", session)
	}
	if _, err := time.Parse(time.RFC3339, session["last_active_at"].(string)); err != nil {
		t.Fatal(err)
	}
	renamed := expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/sessions/"+id, access, map[string]any{"name": "  我的笔记本  "}, nil), http.StatusOK)
	if renamed["data"].(map[string]any)["name"] != "我的笔记本" {
		t.Fatal("label was not normalized")
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/sessions/"+id, other, map[string]any{"name": "foreign"}, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/sessions/"+id, "", map[string]any{"name": "anonymous"}, nil), http.StatusUnauthorized)
	for _, body := range []map[string]any{{}, {"name": nil}, {"name": strings.Repeat("界", 81)}, {"name": "bad\nname"}} {
		expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/sessions/"+id, access, body, nil), http.StatusUnprocessableEntity)
	}
	rotated := expectStatus(t, api.request(http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": refresh}, nil), http.StatusOK)["data"].(map[string]any)
	nextAccess := rotated["access_token"].(string)
	nextID := rotated["session_id"].(string)
	list = expectStatus(t, api.request(http.MethodGet, "/api/v1/me/sessions", nextAccess, nil, nil), http.StatusOK)["data"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != nextID || list[0].(map[string]any)["name"] != "我的笔记本" {
		t.Fatalf("rotation lost label or retained old session: %#v", list)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/sessions/"+id, nextAccess, map[string]any{"name": "old"}, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/sessions/"+nextID, nextAccess, map[string]any{"name": ""}, nil), http.StatusOK)
	list = expectStatus(t, api.request(http.MethodGet, "/api/v1/me/sessions", nextAccess, nil, nil), http.StatusOK)["data"].([]any)
	if list[0].(map[string]any)["name"] != "" {
		t.Fatal("could not clear label")
	}
}

func TestDeviceSessionActivityIsOwnerScopedThrottledAndMonotonic(t *testing.T) {
	api := newTestAPI(t)
	access, _ := login(t, api)
	other, _ := loginAs(t, api, "rain", "demo1234")
	_ = other
	var id string
	if err := api.db.QueryRow(`SELECT id FROM sessions WHERE user_id = 'usr_demo'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour).Unix()
	if _, err := api.db.Exec(`UPDATE sessions SET last_active_unix = ?`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Exec(`CREATE TABLE activity_writes (id TEXT); CREATE TRIGGER count_activity_writes AFTER UPDATE OF last_active_unix ON sessions BEGIN INSERT INTO activity_writes(id) VALUES (NEW.id); END`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		expectStatus(t, api.request(http.MethodGet, "/api/v1/me", access, nil, nil), http.StatusOK)
	}
	var count int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM activity_writes`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("activity caused %d writes, expected one", count)
	}
	var last, foreign int64
	if err := api.db.QueryRow(`SELECT last_active_unix FROM sessions WHERE id = ?`, id).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if err := api.db.QueryRow(`SELECT last_active_unix FROM sessions WHERE user_id = 'usr_rain'`).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	if last <= old || foreign != old {
		t.Fatalf("activity scope failed: own=%d foreign=%d old=%d", last, foreign, old)
	}
	future := time.Now().Add(time.Hour).Unix()
	if _, err := api.db.Exec(`UPDATE sessions SET last_active_unix = ? WHERE id = ?`, future, id); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me", access, nil, nil), http.StatusOK)
	if err := api.db.QueryRow(`SELECT last_active_unix FROM sessions WHERE id = ?`, id).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if last != future {
		t.Fatal("clock movement regressed recorded activity")
	}
}
