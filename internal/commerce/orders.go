package commerce

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func (s *Service) Cart(ctx context.Context, userID string) ([]CartItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.type_id,t.name,p.sku,p.name,p.description,p.inventory,p.status,p.version,p.created_at,p.updated_at,c.quantity,c.updated_at FROM cart_items c JOIN products p ON p.id=c.product_id JOIN product_types t ON t.id=p.type_id WHERE c.user_id=? ORDER BY c.updated_at DESC,p.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list cart: %w", err)
	}
	defer rows.Close()
	items := []CartItem{}
	for rows.Next() {
		var item CartItem
		if err := rows.Scan(&item.Product.ID, &item.Product.TypeID, &item.Product.TypeName, &item.Product.SKU, &item.Product.Name, &item.Product.Description, &item.Product.Inventory, &item.Product.Status, &item.Product.Version, &item.Product.CreatedAt, &item.Product.UpdatedAt, &item.Quantity, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) SetCartItem(ctx context.Context, userID, productID string, quantity int) (CartItem, error) {
	if quantity < 1 || quantity > 100 {
		return CartItem{}, platform.Validation(map[string][]string{"quantity": {"必须在 1 到 100 之间"}})
	}
	product, err := s.Product(ctx, productID, false)
	if err != nil {
		return CartItem{}, err
	}
	if product.Inventory < quantity {
		return CartItem{}, platform.Problem(http.StatusConflict, "insufficient_inventory", "库存不足")
	}
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `INSERT INTO cart_items(user_id,product_id,quantity,updated_at) VALUES(?,?,?,?) ON CONFLICT(user_id,product_id) DO UPDATE SET quantity=excluded.quantity,updated_at=excluded.updated_at`, userID, productID, quantity, stamp)
	if err != nil {
		return CartItem{}, fmt.Errorf("set cart item: %w", err)
	}
	return CartItem{Product: product, Quantity: quantity, UpdatedAt: stamp}, nil
}

func (s *Service) DeleteCartItem(ctx context.Context, userID, productID string) (bool, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM cart_items WHERE user_id=? AND product_id=?`, userID, productID)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

func scanOrder(row interface{ Scan(...any) error }) (Order, error) {
	var item Order
	err := row.Scan(&item.ID, &item.UserID, &item.Status, &item.FulfillmentCarrier, &item.TrackingCode, &item.CancelledAt, &item.FulfilledAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

const orderSelect = `SELECT id,user_id,status,fulfillment_carrier,tracking_code,COALESCE(cancelled_at,''),COALESCE(fulfilled_at,''),created_at,updated_at FROM commerce_orders`

func loadOrderItems(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, orderID string) ([]OrderItem, error) {
	rows, err := q.QueryContext(ctx, `SELECT product_id,sku_snapshot,name_snapshot,quantity FROM commerce_order_items WHERE order_id=? ORDER BY rowid`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []OrderItem{}
	for rows.Next() {
		var item OrderItem
		if err := rows.Scan(&item.ProductID, &item.SKU, &item.Name, &item.Quantity); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) Order(ctx context.Context, orderID, userID string, admin bool) (Order, error) {
	query := orderSelect + ` WHERE id=?`
	args := []any{orderID}
	if !admin {
		query += ` AND user_id=?`
		args = append(args, userID)
	}
	item, err := scanOrder(s.db.QueryRowContext(ctx, query, args...))
	if err == sql.ErrNoRows {
		return Order{}, platform.Problem(http.StatusNotFound, "order_not_found", "订单不存在")
	}
	if err != nil {
		return Order{}, err
	}
	item.Items, err = loadOrderItems(ctx, s.db, item.ID)
	return item, err
}

func (s *Service) ListOrders(ctx context.Context, userID, status string, admin bool, offset, limit int) ([]Order, string, error) {
	offset, limit = page(offset, limit)
	where := []string{"1=1"}
	args := []any{}
	if !admin {
		where = append(where, "user_id=?")
		args = append(args, userID)
	}
	status = strings.TrimSpace(status)
	if status != "" {
		if status != "created" && status != "cancelled" && status != "fulfilled" {
			return nil, "", platform.Validation(map[string][]string{"status": {"必须是 created、cancelled 或 fulfilled"}})
		}
		where = append(where, "status=?")
		args = append(args, status)
	}
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, orderSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []Order{}
	for rows.Next() {
		item, err := scanOrder(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, "", err
	}
	for i := range items {
		items[i].Items, err = loadOrderItems(ctx, s.db, items[i].ID)
		if err != nil {
			return nil, "", err
		}
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return items, next, rows.Err()
}

func (s *Service) CreateOrder(ctx context.Context, userID, idempotencyKey string) (Order, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		return Order{}, false, platform.Validation(map[string][]string{"idempotency_key": {"Idempotency-Key 必填且不能超过 128 个字符"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, false, err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT resource_id FROM idempotency_keys WHERE user_id=? AND scope='create_order' AND key=?`, userID, idempotencyKey).Scan(&existing)
	if err == nil {
		tx.Rollback()
		item, loadErr := s.Order(ctx, existing, userID, false)
		return item, true, loadErr
	}
	if err != sql.ErrNoRows {
		return Order{}, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.sku,p.name,p.inventory,p.status,t.status,c.quantity FROM cart_items c JOIN products p ON p.id=c.product_id JOIN product_types t ON t.id=p.type_id WHERE c.user_id=? ORDER BY p.id`, userID)
	if err != nil {
		return Order{}, false, err
	}
	items := []OrderItem{}
	for rows.Next() {
		var item OrderItem
		var inventory int
		var status, typeStatus string
		if err := rows.Scan(&item.ProductID, &item.SKU, &item.Name, &inventory, &status, &typeStatus, &item.Quantity); err != nil {
			rows.Close()
			return Order{}, false, err
		}
		if status != "active" || typeStatus != "active" {
			rows.Close()
			return Order{}, false, platform.Problem(http.StatusConflict, "product_unavailable", "购物车含不可用商品")
		}
		if inventory < item.Quantity {
			rows.Close()
			return Order{}, false, platform.Problem(http.StatusConflict, "insufficient_inventory", "库存不足")
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return Order{}, false, err
	}
	if len(items) == 0 {
		return Order{}, false, platform.Problem(http.StatusConflict, "empty_cart", "购物车为空")
	}
	id, err := platform.NewID("ord")
	if err != nil {
		return Order{}, false, err
	}
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO commerce_orders(id,user_id,status,created_at,updated_at) VALUES(?,?,'created',?,?)`, id, userID, stamp, stamp); err != nil {
		return Order{}, false, err
	}
	for _, item := range items {
		result, err := tx.ExecContext(ctx, `UPDATE products SET inventory=inventory-?,version=version+1,updated_at=? WHERE id=? AND status='active' AND inventory>=?`, item.Quantity, stamp, item.ProductID, item.Quantity)
		if err != nil {
			return Order{}, false, err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return Order{}, false, platform.Problem(http.StatusConflict, "insufficient_inventory", "库存不足")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO commerce_order_items(order_id,product_id,sku_snapshot,name_snapshot,quantity) VALUES(?,?,?,?,?)`, id, item.ProductID, item.SKU, item.Name, item.Quantity); err != nil {
			return Order{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cart_items WHERE user_id=?`, userID); err != nil {
		return Order{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys(user_id,scope,key,resource_id,created_at) VALUES(?,'create_order',?,?,?)`, userID, idempotencyKey, id, stamp); err != nil {
		return Order{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Order{}, false, err
	}
	return Order{ID: id, UserID: userID, Status: "created", CreatedAt: stamp, UpdatedAt: stamp, Items: items}, false, nil
}

type OrderTransitionInput struct {
	Status             string `json:"status"`
	FulfillmentCarrier string `json:"fulfillment_carrier"`
	TrackingCode       string `json:"tracking_code"`
	Reason             string `json:"reason"`
}

func (s *Service) TransitionOrder(ctx context.Context, actor identity.User, requestID, orderID string, input OrderTransitionInput, admin bool) (Order, bool, error) {
	input.Status = strings.TrimSpace(input.Status)
	input.FulfillmentCarrier = strings.TrimSpace(input.FulfillmentCarrier)
	input.TrackingCode = strings.TrimSpace(input.TrackingCode)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Status != "cancelled" && input.Status != "fulfilled" {
		return Order{}, false, platform.Validation(map[string][]string{"status": {"必须是 cancelled 或 fulfilled"}})
	}
	if !admin && input.Status != "cancelled" {
		return Order{}, false, platform.Problem(http.StatusForbidden, "forbidden", "只有管理员可以履约订单")
	}
	if len(input.FulfillmentCarrier) > 100 || len(input.TrackingCode) > 200 {
		return Order{}, false, platform.Validation(map[string][]string{"fulfillment": {"承运方不能超过 100 个字符，追踪号不能超过 200 个字符"}})
	}
	if input.Status == "fulfilled" && (input.FulfillmentCarrier == "" || input.TrackingCode == "") {
		return Order{}, false, platform.Validation(map[string][]string{"tracking_code": {"履约时必须提供承运方和追踪号"}})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, false, err
	}
	defer tx.Rollback()
	query := orderSelect + ` WHERE id=?`
	args := []any{orderID}
	if !admin {
		query += ` AND user_id=?`
		args = append(args, actor.ID)
	}
	before, err := scanOrder(tx.QueryRowContext(ctx, query, args...))
	if err == sql.ErrNoRows {
		return Order{}, false, platform.Problem(http.StatusNotFound, "order_not_found", "订单不存在")
	}
	if err != nil {
		return Order{}, false, err
	}
	before.Items, err = loadOrderItems(ctx, tx, orderID)
	if err != nil {
		return Order{}, false, err
	}
	if before.Status == input.Status {
		return before, false, nil
	}
	if before.Status != "created" {
		return Order{}, false, platform.Problem(http.StatusConflict, "invalid_order_transition", "订单已结束，不能再次变更")
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	after := before
	after.Status = input.Status
	after.UpdatedAt = stamp
	if input.Status == "cancelled" {
		after.CancelledAt = stamp
		after.FulfillmentCarrier = ""
		after.TrackingCode = ""
		for _, item := range before.Items {
			result, err := tx.ExecContext(ctx, `UPDATE products SET inventory=inventory+?,version=version+1,updated_at=? WHERE id=? AND inventory<=?`, item.Quantity, stamp, item.ProductID, 1000000-item.Quantity)
			if err != nil {
				return Order{}, false, err
			}
			changed, _ := result.RowsAffected()
			if changed != 1 {
				return Order{}, false, platform.Problem(http.StatusConflict, "inventory_bound_conflict", "取消会使库存超过本地上限，请先调整库存")
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE commerce_orders SET status='cancelled',cancelled_at=?,updated_at=? WHERE id=? AND status='created'`, stamp, stamp, orderID)
	} else {
		after.FulfilledAt = stamp
		after.FulfillmentCarrier = input.FulfillmentCarrier
		after.TrackingCode = input.TrackingCode
		_, err = tx.ExecContext(ctx, `UPDATE commerce_orders SET status='fulfilled',fulfillment_carrier=?,tracking_code=?,fulfilled_at=?,updated_at=? WHERE id=? AND status='created'`, input.FulfillmentCarrier, input.TrackingCode, stamp, stamp, orderID)
	}
	if err != nil {
		return Order{}, false, err
	}
	action := "order_cancel"
	if input.Status == "fulfilled" {
		action = "order_fulfill"
	}
	if err := insertAudit(ctx, tx, actor.ID, action, "order", orderID, before, after, input.Reason, requestID, now); err != nil {
		return Order{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Order{}, false, err
	}
	return after, true, nil
}
