package app_test

import (
	"net/http"
	"testing"
)

func promoteDemoAdmin(t *testing.T, api *testAPI) string {
	t.Helper()
	if _, err := api.db.Exec(`UPDATE users SET role='admin' WHERE id='usr_demo'`); err != nil {
		t.Fatal(err)
	}
	access, _ := login(t, api)
	return access
}

func TestLocalCommerceInventoryOrderLifecycleAndRBAC(t *testing.T) {
	api := newTestAPI(t)
	admin := promoteDemoAdmin(t, api)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	forest, _ := loginAs(t, api, "forest", "demo1234")

	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/product-types", rain, map[string]any{"slug": "local-goods", "name": "本地商品"}, nil), http.StatusForbidden)
	typePayload := expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/product-types", admin, map[string]any{"slug": "local-goods", "name": "本地商品", "reason": "测试目录"}, nil), http.StatusCreated)
	typeID := typePayload["data"].(map[string]any)["id"].(string)
	productPayload := expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/products", admin, map[string]any{
		"type_id": typeID, "sku": "poster-001", "name": "社区海报", "description": "仅本地履约", "inventory": 2, "status": "active", "reason": "测试库存",
	}, nil), http.StatusCreated)
	productID := productPayload["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/products/"+productID, "", nil, nil), http.StatusOK)

	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/cart/"+productID, rain, map[string]any{"quantity": 2}, nil), http.StatusOK)
	created := expectStatus(t, api.request(http.MethodPost, "/api/v1/orders", rain, nil, map[string]string{"Idempotency-Key": "checkout-one"}), http.StatusCreated)
	orderID := created["data"].(map[string]any)["id"].(string)
	replay := api.request(http.MethodPost, "/api/v1/orders", rain, nil, map[string]string{"Idempotency-Key": "checkout-one"})
	expectStatus(t, replay, http.StatusOK)
	if replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("checkout replay was not identified")
	}
	if got := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/products/"+productID, admin, nil, nil), http.StatusOK)["data"].(map[string]any)["inventory"]; got != float64(0) {
		t.Fatalf("inventory not reserved exactly once: %v", got)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/orders/"+orderID, forest, nil, nil), http.StatusNotFound)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/cart/"+productID, forest, map[string]any{"quantity": 1}, nil), http.StatusConflict)

	expectStatus(t, api.request(http.MethodPost, "/api/v1/orders/"+orderID+"/cancel", rain, map[string]any{"reason": "不再需要"}, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodPost, "/api/v1/orders/"+orderID+"/cancel", rain, map[string]any{"reason": "重复请求"}, nil), http.StatusOK)
	if got := expectStatus(t, api.request(http.MethodGet, "/api/v1/admin/products/"+productID, admin, nil, nil), http.StatusOK)["data"].(map[string]any)["inventory"]; got != float64(2) {
		t.Fatalf("cancel did not restore inventory exactly once: %v", got)
	}

	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/cart/"+productID, forest, map[string]any{"quantity": 1}, nil), http.StatusOK)
	second := expectStatus(t, api.request(http.MethodPost, "/api/v1/orders", forest, nil, map[string]string{"Idempotency-Key": "checkout-two"}), http.StatusCreated)
	secondID := second["data"].(map[string]any)["id"].(string)
	fulfilled := expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/orders/"+secondID, admin, map[string]any{"status": "fulfilled", "fulfillment_carrier": "local", "tracking_code": "LOCAL-1", "reason": "手工本地履约"}, nil), http.StatusOK)
	if fulfilled["data"].(map[string]any)["tracking_code"] != "LOCAL-1" {
		t.Fatalf("tracking metadata missing: %#v", fulfilled)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/orders/"+secondID+"/cancel", forest, map[string]any{"reason": "太晚"}, nil), http.StatusConflict)

	// Audit and state mutation share a transaction. A forced audit failure must
	// leave the order in created state with no tracking metadata.
	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/cart/"+productID, rain, map[string]any{"quantity": 1}, nil), http.StatusOK)
	third := expectStatus(t, api.request(http.MethodPost, "/api/v1/orders", rain, nil, map[string]string{"Idempotency-Key": "checkout-three"}), http.StatusCreated)
	thirdID := third["data"].(map[string]any)["id"].(string)
	if _, err := api.db.Exec(`CREATE TRIGGER fail_commerce_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT, 'forced commerce audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/orders/"+thirdID, admin, map[string]any{"status": "fulfilled", "fulfillment_carrier": "local", "tracking_code": "SHOULD-ROLL-BACK", "reason": "force rollback"}, nil), http.StatusInternalServerError)
	if _, err := api.db.Exec(`DROP TRIGGER fail_commerce_audit`); err != nil {
		t.Fatal(err)
	}
	rolledBack := expectStatus(t, api.request(http.MethodGet, "/api/v1/orders/"+thirdID, rain, nil, nil), http.StatusOK)["data"].(map[string]any)
	if rolledBack["status"] != "created" || rolledBack["tracking_code"] != "" {
		t.Fatalf("failed audit did not roll back fulfillment: %#v", rolledBack)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/orders/"+thirdID+"/cancel", rain, map[string]any{"reason": "cleanup"}, nil), http.StatusOK)

	var audits int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action IN ('product_type_create','product_create','order_cancel','order_fulfill')`).Scan(&audits); err != nil || audits != 5 {
		t.Fatalf("commerce audit count=%d err=%v", audits, err)
	}
	capabilities := expectStatus(t, api.request(http.MethodGet, "/api/v1/capabilities", "", nil, nil), http.StatusOK)["data"].(map[string]any)
	for _, name := range []string{"payments", "cash_wallet", "withdrawals", "lottery", "raffle_gambling", "vip_purchase", "paid_content", "external_fulfillment"} {
		if capabilities[name].(map[string]any)["status"] != "disabled" {
			t.Fatalf("capability %s unexpectedly enabled", name)
		}
	}
}

func TestLocalGamificationAwardsAreIdempotentAndFramesRequireEntitlement(t *testing.T) {
	api := newTestAPI(t)
	admin := promoteDemoAdmin(t, api)
	rain, _ := loginAs(t, api, "rain", "demo1234")

	first := api.request(http.MethodPost, "/api/v1/me/check-in", rain, nil, nil)
	expectStatus(t, first, http.StatusOK)
	second := api.request(http.MethodPost, "/api/v1/me/check-in", rain, nil, nil)
	expectStatus(t, second, http.StatusOK)
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("daily check-in was not replayed")
	}
	status := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/gamification", rain, nil, nil), http.StatusOK)["data"].(map[string]any)
	if status["points_balance"] != float64(5) || status["lifetime_points"] != float64(5) {
		t.Fatalf("duplicate check-in awarded twice: %#v", status)
	}

	frame := expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/avatar-frames", admin, map[string]any{"slug": "autumn", "name": "秋日", "image_url": "/frames/autumn.png", "status": "active", "reason": "测试奖励"}, nil), http.StatusCreated)
	frameID := frame["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/avatar-frame", rain, map[string]any{"frame_id": frameID}, nil), http.StatusForbidden)
	task := expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/tasks", admin, map[string]any{"code": "verified-helper", "name": "已验证协助", "repeat_policy": "once", "reward_points": 10, "reward_frame_id": frameID, "status": "active", "reason": "测试任务"}, nil), http.StatusCreated)
	taskID := task["data"].(map[string]any)["id"].(string)
	awardPath := "/api/v1/admin/users/usr_rain/tasks/" + taskID + "/award"
	award := api.request(http.MethodPost, awardPath, admin, map[string]any{"claim_key": "verified-event-1", "reason": "管理员已核验"}, nil)
	expectStatus(t, award, http.StatusOK)
	replay := api.request(http.MethodPost, awardPath, admin, map[string]any{"claim_key": "different-ignored-for-once", "reason": "重复"}, nil)
	expectStatus(t, replay, http.StatusOK)
	if replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("one-time task award was not replayed")
	}
	status = expectStatus(t, api.request(http.MethodGet, "/api/v1/me/gamification", rain, nil, nil), http.StatusOK)["data"].(map[string]any)
	if status["points_balance"] != float64(15) {
		t.Fatalf("task reward was not awarded exactly once: %#v", status)
	}
	selected := expectStatus(t, api.request(http.MethodPut, "/api/v1/me/avatar-frame", rain, map[string]any{"frame_id": frameID}, nil), http.StatusOK)
	if selected["data"].(map[string]any)["selected_frame"].(map[string]any)["id"] != frameID {
		t.Fatalf("entitled frame was not selected: %#v", selected)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/ranks", "", nil, nil), http.StatusOK)
	if response := api.request(http.MethodPost, "/api/v1/tasks/"+taskID+"/claim", rain, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("client self-claim route unexpectedly exists: status=%d body=%s", response.Code, response.Body.String())
	}

	var pointEvents, claims, entitlements, auditAwards int
	_ = api.db.QueryRow(`SELECT COUNT(*) FROM local_point_events WHERE user_id='usr_rain'`).Scan(&pointEvents)
	_ = api.db.QueryRow(`SELECT COUNT(*) FROM task_claims WHERE user_id='usr_rain'`).Scan(&claims)
	_ = api.db.QueryRow(`SELECT COUNT(*) FROM user_avatar_frames WHERE user_id='usr_rain'`).Scan(&entitlements)
	_ = api.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='task_reward_award'`).Scan(&auditAwards)
	if pointEvents != 2 || claims != 1 || entitlements != 1 || auditAwards != 1 {
		t.Fatalf("award invariants events=%d claims=%d entitlements=%d audits=%d", pointEvents, claims, entitlements, auditAwards)
	}
	if _, err := api.db.Exec(`UPDATE local_point_events SET amount=999 WHERE user_id='usr_rain'`); err == nil {
		t.Fatal("immutable local point ledger accepted an update")
	}
}
