package app_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestDeletedCommentTombstonesPreserveThreadPresentation(t *testing.T) {
	api := newTestAPI(t)
	demo, _ := login(t, api)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	forest, _ := loginAs(t, api, "forest", "demo1234")
	post := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", demo, map[string]any{"content": "用于验证评论线程墓碑的文章"}, nil), http.StatusCreated)
	postID := post["data"].(map[string]any)["id"].(string)
	root := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/"+postID+"/comments", demo, map[string]any{"content": "稍后删除的根评论"}, nil), http.StatusCreated)
	rootID := root["data"].(map[string]any)["id"].(string)
	reply := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/"+postID+"/comments/"+rootID+"/replies", rain, map[string]any{"content": "仍然可见的回复"}, nil), http.StatusCreated)
	replyID := reply["data"].(map[string]any)["id"].(string)
	grandchild := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts/"+postID+"/comments/"+replyID+"/replies", forest, map[string]any{"content": "仍然可见的第三层回复"}, nil), http.StatusCreated)
	grandchildID := grandchild["data"].(map[string]any)["id"].(string)

	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/"+postID+"/comments/"+rootID, demo, nil, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	expectStatus(t, api.request(http.MethodDelete, "/api/v1/posts/"+postID+"/comments/"+replyID, rain, nil, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	page := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID+"/comments?limit=1", "", nil, nil), http.StatusOK)
	items := page["data"].([]any)
	if len(items) != 3 {
		t.Fatalf("one thread page must include its complete reply chain: %#v", page)
	}
	tombstone := items[0].(map[string]any)
	replyTombstone := items[1].(map[string]any)
	visibleReply := items[2].(map[string]any)
	if tombstone["id"] != rootID || tombstone["deleted"] != true || tombstone["body"] != "" || tombstone["content"] != "" {
		t.Fatalf("deleted root leaked content or lost its tombstone: %#v", tombstone)
	}
	if tombstone["thread_id"] != rootID || tombstone["reply_count"] != float64(2) {
		t.Fatalf("root thread metadata mismatch: %#v", tombstone)
	}
	if replyTombstone["id"] != replyID || replyTombstone["deleted"] != true || replyTombstone["parent_id"] != rootID {
		t.Fatalf("deleted intermediate reply lost its structural tombstone: %#v", replyTombstone)
	}
	if visibleReply["id"] != grandchildID || visibleReply["parent_id"] != replyID || visibleReply["thread_id"] != rootID || visibleReply["deleted"] != false {
		t.Fatalf("reply was detached from tombstoned root: %#v", visibleReply)
	}
	if author := tombstone["author"].(map[string]any); author["id"] != "" || author["display_name"] != "已删除" {
		t.Fatalf("tombstone leaked original author identity: %#v", author)
	}
	detail := expectStatus(t, api.request(http.MethodGet, "/api/v1/posts/"+postID, demo, nil, nil), http.StatusOK)["data"].(map[string]any)
	if detail["comment_count"] != float64(1) {
		t.Fatalf("published comment counter should exclude only the deleted row: %#v", detail)
	}
}

func TestCollectionsHaveExplicitPrivacyAndBookmarksStayPrivate(t *testing.T) {
	api := newTestAPI(t)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	public := expectStatus(t, api.request(http.MethodPost, "/api/v1/me/collections", rain, map[string]any{
		"name": "公开精选", "description": "任何可查看作者的人都能发现", "visibility": "public",
	}, nil), http.StatusCreated)["data"].(map[string]any)
	private := expectStatus(t, api.request(http.MethodPost, "/api/v1/me/collections", rain, map[string]any{
		"name": "私人整理", "visibility": "private",
	}, nil), http.StatusCreated)["data"].(map[string]any)
	publicID := public["id"].(string)
	privateID := private["id"].(string)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/collections/"+publicID+"/posts/post_morning", rain, nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/collections/"+privateID+"/posts/post_morning", rain, nil, nil), http.StatusOK)

	publicList := expectStatus(t, api.request(http.MethodGet, "/api/v1/users/usr_rain/collections", "", nil, nil), http.StatusOK)
	if items := publicList["data"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != publicID {
		t.Fatalf("public collection discovery leaked private metadata: %#v", publicList)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/collections/"+publicID, "", nil, nil), http.StatusOK)
	expectStatus(t, api.request(http.MethodGet, "/api/v1/collections/"+privateID, "", nil, nil), http.StatusNotFound)
	ownerList := expectStatus(t, api.request(http.MethodGet, "/api/v1/me/collections", rain, nil, nil), http.StatusOK)
	if len(ownerList["data"].([]any)) != 2 {
		t.Fatalf("owner cannot see both collection privacy states: %#v", ownerList)
	}

	// Item totals are collection totals, not the length of whichever page was
	// requested. Exercise multiple cursors so the value cannot regress to a
	// page-local count.
	for i := 0; i < 21; i++ {
		created := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", rain, map[string]any{
			"content": fmt.Sprintf("收藏集分页帖子 %02d", i), "visibility": "public",
		}, map[string]string{"Idempotency-Key": fmt.Sprintf("collection-page-%02d", i)}), http.StatusCreated)["data"].(map[string]any)
		expectStatus(t, api.request(http.MethodPut, "/api/v1/me/collections/"+publicID+"/posts/"+created["id"].(string), rain, nil, nil), http.StatusOK)
	}
	restricted := expectStatus(t, api.request(http.MethodPost, "/api/v1/posts", rain, map[string]any{
		"content": "仅关注者可见的收藏集成员", "visibility": "followers",
	}, map[string]string{"Idempotency-Key": "collection-hidden-member"}), http.StatusCreated)["data"].(map[string]any)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/collections/"+publicID+"/posts/"+restricted["id"].(string), rain, nil, nil), http.StatusOK)

	firstPage := expectStatus(t, api.request(http.MethodGet, "/api/v1/collections/"+publicID+"?limit=5", rain, nil, nil), http.StatusOK)
	firstData := firstPage["data"].(map[string]any)
	if firstData["collection"].(map[string]any)["item_count"] != float64(23) || len(firstData["posts"].([]any)) != 5 {
		t.Fatalf("owner collection total was page-local: %#v", firstPage)
	}
	next := firstData["next_cursor"].(string)
	secondPage := expectStatus(t, api.request(http.MethodGet, "/api/v1/collections/"+publicID+"?limit=5&cursor="+url.QueryEscape(next), rain, nil, nil), http.StatusOK)
	if secondPage["data"].(map[string]any)["collection"].(map[string]any)["item_count"] != float64(23) {
		t.Fatalf("owner collection total changed across cursors: %#v", secondPage)
	}
	publicFirstPage := expectStatus(t, api.request(http.MethodGet, "/api/v1/collections/"+publicID+"?limit=5", "", nil, nil), http.StatusOK)
	publicFirstData := publicFirstPage["data"].(map[string]any)
	if publicFirstData["collection"].(map[string]any)["item_count"] != float64(22) || len(publicFirstData["posts"].([]any)) != 5 {
		t.Fatalf("public count did not exclude the restricted member: %#v", publicFirstPage)
	}
	publicNext := publicFirstData["next_cursor"].(string)
	publicSecondPage := expectStatus(t, api.request(http.MethodGet, "/api/v1/collections/"+publicID+"?limit=5&cursor="+url.QueryEscape(publicNext), "", nil, nil), http.StatusOK)
	if publicSecondPage["data"].(map[string]any)["collection"].(map[string]any)["item_count"] != float64(22) {
		t.Fatalf("public viewer-safe total changed across cursors: %#v", publicSecondPage)
	}

	// There is deliberately no public bookmark projection; bookmarks remain
	// accessible only through the authenticated owner's /me route.
	expectStatus(t, api.request(http.MethodGet, "/api/v1/me/bookmarks", "", nil, nil), http.StatusUnauthorized)
	if response := api.request(http.MethodGet, "/api/v1/users/usr_rain/bookmarks", "", nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("a public bookmark route unexpectedly exists: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestShippingAddressSnapshotsAndManualTrackingStayInternal(t *testing.T) {
	api := newTestAPI(t)
	admin := promoteDemoAdmin(t, api)
	rain, _ := loginAs(t, api, "rain", "demo1234")
	forest, _ := loginAs(t, api, "forest", "demo1234")

	first := expectStatus(t, api.request(http.MethodPost, "/api/v1/me/shipping-addresses", rain, map[string]any{
		"label": "家", "recipient_name": "雨", "phone": "+86 10000", "region": "浙江省杭州市", "address_line": "第一街道 1 号", "postal_code": "310000",
	}, nil), http.StatusCreated)["data"].(map[string]any)
	if first["is_default"] != true {
		t.Fatalf("first address was not made the safe default: %#v", first)
	}
	second := expectStatus(t, api.request(http.MethodPost, "/api/v1/me/shipping-addresses", rain, map[string]any{
		"label": "工作", "recipient_name": "雨", "phone": "+86 20000", "region": "上海市", "address_line": "第二街道 2 号", "is_default": true,
	}, nil), http.StatusCreated)["data"].(map[string]any)
	addressID := second["id"].(string)
	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/shipping-addresses/"+addressID, forest, map[string]any{
		"recipient_name": "越权", "phone": "123", "region": "未知", "address_line": "未知", "is_default": true,
	}, map[string]string{"If-Match": `"1"`}), http.StatusNotFound)

	typePayload := expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/product-types", admin, map[string]any{"slug": "shipping-test", "name": "物流测试"}, nil), http.StatusCreated)
	typeID := typePayload["data"].(map[string]any)["id"].(string)
	productPayload := expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/products", admin, map[string]any{
		"type_id": typeID, "sku": "ship-001", "name": "需寄送商品", "inventory": 1, "status": "active",
	}, nil), http.StatusCreated)
	productID := productPayload["data"].(map[string]any)["id"].(string)
	expectStatus(t, api.request(http.MethodPut, "/api/v1/me/cart/"+productID, rain, map[string]any{"quantity": 1}, nil), http.StatusOK)
	created := expectStatus(t, api.request(http.MethodPost, "/api/v1/orders", rain, map[string]any{"address_id": addressID}, map[string]string{"Idempotency-Key": "shipping-snapshot"}), http.StatusCreated)["data"].(map[string]any)
	orderID := created["id"].(string)
	if created["shipping_address"].(map[string]any)["address_line"] != "第二街道 2 号" {
		t.Fatalf("order did not snapshot selected address: %#v", created)
	}

	expectStatus(t, api.request(http.MethodPatch, "/api/v1/me/shipping-addresses/"+addressID, rain, map[string]any{
		"label": "工作", "recipient_name": "雨", "phone": "+86 20000", "region": "上海市", "address_line": "已经修改的地址", "is_default": true,
	}, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	order := expectStatus(t, api.request(http.MethodGet, "/api/v1/orders/"+orderID, rain, nil, nil), http.StatusOK)["data"].(map[string]any)
	if order["shipping_address"].(map[string]any)["address_line"] != "第二街道 2 号" {
		t.Fatalf("mutable address changed immutable order snapshot: %#v", order)
	}

	fulfilled := expectStatus(t, api.request(http.MethodPatch, "/api/v1/admin/orders/"+orderID, admin, map[string]any{
		"status": "fulfilled", "fulfillment_carrier": "manual-local", "tracking_code": "MANUAL-1", "reason": "手工履约测试",
	}, nil), http.StatusOK)["data"].(map[string]any)
	if events := fulfilled["tracking_events"].([]any); len(events) != 1 || events[0].(map[string]any)["source"] != "manual" {
		t.Fatalf("manual fulfillment did not create the initial tracking event: %#v", fulfilled)
	}
	expectStatus(t, api.request(http.MethodPost, "/api/v1/admin/orders/"+orderID+"/tracking-events", admin, map[string]any{
		"status": "in_transit", "description": "已从本地仓发出", "location": "杭州", "reason": "手工更新",
	}, nil), http.StatusCreated)
	tracking := expectStatus(t, api.request(http.MethodGet, "/api/v1/orders/"+orderID+"/tracking", rain, nil, nil), http.StatusOK)["data"].([]any)
	if len(tracking) != 2 || tracking[1].(map[string]any)["status"] != "in_transit" || tracking[1].(map[string]any)["source"] != "manual" {
		t.Fatalf("manual tracking timeline mismatch: %#v", tracking)
	}
	expectStatus(t, api.request(http.MethodGet, "/api/v1/orders/"+orderID+"/tracking", forest, nil, nil), http.StatusNotFound)
	if _, err := api.db.Exec(`UPDATE order_tracking_events SET description='tampered' WHERE order_id=?`, orderID); err == nil {
		t.Fatal("immutable tracking timeline accepted an update")
	}
}
