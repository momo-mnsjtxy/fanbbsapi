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

type TypeInput struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type ProductInput struct {
	TypeID      string `json:"type_id"`
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Inventory   *int   `json:"inventory"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
}

func validateType(input TypeInput) (TypeInput, error) {
	var err error
	input.Slug, err = validateCode(input.Slug, "slug")
	if err != nil {
		return input, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Status = strings.TrimSpace(input.Status)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Status == "" {
		input.Status = "active"
	}
	fields := map[string][]string{}
	if len([]rune(input.Name)) < 1 || len([]rune(input.Name)) > 80 {
		fields["name"] = []string{"长度必须为 1 到 80 个字符"}
	}
	if input.Status != "active" && input.Status != "archived" {
		fields["status"] = []string{"必须是 active 或 archived"}
	}
	if len(input.Reason) > 500 {
		fields["reason"] = []string{"不能超过 500 个字符"}
	}
	if len(fields) > 0 {
		return input, platform.Validation(fields)
	}
	return input, nil
}

func validateProduct(input ProductInput, creating bool) (ProductInput, error) {
	input.TypeID = strings.TrimSpace(input.TypeID)
	input.SKU = strings.ToLower(strings.TrimSpace(input.SKU))
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Status = strings.TrimSpace(input.Status)
	input.Reason = strings.TrimSpace(input.Reason)
	if creating && input.Status == "" {
		input.Status = "draft"
	}
	fields := map[string][]string{}
	if creating && input.TypeID == "" {
		fields["type_id"] = []string{"不能为空"}
	}
	if (creating || input.SKU != "") && (len(input.SKU) < 2 || len(input.SKU) > 64 || !codePattern.MatchString(input.SKU)) {
		fields["sku"] = []string{"必须是 2 到 64 位小写字母、数字、连字符或下划线"}
	}
	if (creating || input.Name != "") && (len([]rune(input.Name)) < 1 || len([]rune(input.Name)) > 120) {
		fields["name"] = []string{"长度必须为 1 到 120 个字符"}
	}
	if len([]rune(input.Description)) > 2000 {
		fields["description"] = []string{"不能超过 2000 个字符"}
	}
	if input.Inventory != nil && (*input.Inventory < 0 || *input.Inventory > 1000000) {
		fields["inventory"] = []string{"必须在 0 到 1000000 之间"}
	}
	if input.Status != "" && input.Status != "draft" && input.Status != "active" && input.Status != "archived" {
		fields["status"] = []string{"必须是 draft、active 或 archived"}
	}
	if len(input.Reason) > 500 {
		fields["reason"] = []string{"不能超过 500 个字符"}
	}
	if len(fields) > 0 {
		return input, platform.Validation(fields)
	}
	return input, nil
}

func scanType(row interface{ Scan(...any) error }) (ProductType, error) {
	var item ProductType
	err := row.Scan(&item.ID, &item.Slug, &item.Name, &item.Status, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanProduct(row interface{ Scan(...any) error }) (Product, error) {
	var item Product
	err := row.Scan(&item.ID, &item.TypeID, &item.TypeName, &item.SKU, &item.Name, &item.Description, &item.Inventory, &item.Status, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

const productSelect = `SELECT p.id, p.type_id, t.name, p.sku, p.name, p.description, p.inventory, p.status, p.version, p.created_at, p.updated_at FROM products p JOIN product_types t ON t.id = p.type_id`

func (s *Service) ListTypes(ctx context.Context, includeArchived bool) ([]ProductType, error) {
	where := " WHERE status = 'active'"
	if includeArchived {
		where = ""
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, slug, name, status, created_at, updated_at FROM product_types`+where+` ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list product types: %w", err)
	}
	defer rows.Close()
	items := []ProductType{}
	for rows.Next() {
		item, err := scanType(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListProducts(ctx context.Context, typeID string, includeInactive bool, offset, limit int) ([]Product, string, error) {
	offset, limit = page(offset, limit)
	where := []string{"1=1"}
	args := []any{}
	if !includeInactive {
		where = append(where, "p.status = 'active'", "t.status = 'active'")
	}
	if strings.TrimSpace(typeID) != "" {
		where = append(where, "p.type_id = ?")
		args = append(args, strings.TrimSpace(typeID))
	}
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, productSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY p.created_at DESC, p.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list products: %w", err)
	}
	defer rows.Close()
	items := []Product{}
	for rows.Next() {
		item, err := scanProduct(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = platform.EncodeCursor(offset + limit)
	}
	return items, next, rows.Err()
}

func (s *Service) Product(ctx context.Context, productID string, includeInactive bool) (Product, error) {
	query := productSelect + ` WHERE p.id = ?`
	if !includeInactive {
		query += ` AND p.status = 'active' AND t.status = 'active'`
	}
	item, err := scanProduct(s.db.QueryRowContext(ctx, query, productID))
	if err == sql.ErrNoRows {
		return Product{}, platform.Problem(http.StatusNotFound, "product_not_found", "商品不存在")
	}
	if err != nil {
		return Product{}, fmt.Errorf("load product: %w", err)
	}
	return item, nil
}

func (s *Service) CreateType(ctx context.Context, actor identity.User, requestID string, input TypeInput) (ProductType, error) {
	input, err := validateType(input)
	if err != nil {
		return ProductType{}, err
	}
	id, err := platform.NewID("pty")
	if err != nil {
		return ProductType{}, err
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProductType{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO product_types(id, slug, name, status, created_by, updated_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, input.Slug, input.Name, input.Status, actor.ID, actor.ID, stamp, stamp)
	if err != nil {
		return ProductType{}, conflictFromUnique(err, "product_type_in_use", "商品类型名称或 slug 已存在")
	}
	after := ProductType{ID: id, Slug: input.Slug, Name: input.Name, Status: input.Status, CreatedAt: stamp, UpdatedAt: stamp}
	if err := insertAudit(ctx, tx, actor.ID, "product_type_create", "product_type", id, map[string]any{}, after, input.Reason, requestID, now); err != nil {
		return ProductType{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProductType{}, err
	}
	return after, nil
}

func (s *Service) UpdateType(ctx context.Context, actor identity.User, requestID, id string, input TypeInput) (ProductType, error) {
	input, err := validateType(input)
	if err != nil {
		return ProductType{}, err
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProductType{}, err
	}
	defer tx.Rollback()
	before, err := scanType(tx.QueryRowContext(ctx, `SELECT id, slug, name, status, created_at, updated_at FROM product_types WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return ProductType{}, platform.Problem(http.StatusNotFound, "product_type_not_found", "商品类型不存在")
	}
	if err != nil {
		return ProductType{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE product_types SET slug=?, name=?, status=?, updated_by=?, updated_at=? WHERE id=?`, input.Slug, input.Name, input.Status, actor.ID, stamp, id)
	if err != nil {
		return ProductType{}, conflictFromUnique(err, "product_type_in_use", "商品类型名称或 slug 已存在")
	}
	after := ProductType{ID: id, Slug: input.Slug, Name: input.Name, Status: input.Status, CreatedAt: before.CreatedAt, UpdatedAt: stamp}
	if err := insertAudit(ctx, tx, actor.ID, "product_type_update", "product_type", id, before, after, input.Reason, requestID, now); err != nil {
		return ProductType{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProductType{}, err
	}
	return after, nil
}

func (s *Service) DeleteType(ctx context.Context, actor identity.User, requestID, id, reason string) error {
	reason = strings.TrimSpace(reason)
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, err := scanType(tx.QueryRowContext(ctx, `SELECT id, slug, name, status, created_at, updated_at FROM product_types WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return platform.Problem(http.StatusNotFound, "product_type_not_found", "商品类型不存在")
	}
	if err != nil {
		return err
	}
	var used int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE type_id=?`, id).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return platform.Problem(http.StatusConflict, "product_type_in_use", "仍有商品使用该类型")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM product_types WHERE id=?`, id); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, actor.ID, "product_type_delete", "product_type", id, before, map[string]any{}, reason, requestID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) CreateProduct(ctx context.Context, actor identity.User, requestID string, input ProductInput) (Product, error) {
	input, err := validateProduct(input, true)
	if err != nil {
		return Product{}, err
	}
	inventory := 0
	if input.Inventory != nil {
		inventory = *input.Inventory
	}
	id, err := platform.NewID("prd")
	if err != nil {
		return Product{}, err
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Product{}, err
	}
	defer tx.Rollback()
	var typeName, typeStatus string
	if err := tx.QueryRowContext(ctx, `SELECT name,status FROM product_types WHERE id=?`, input.TypeID).Scan(&typeName, &typeStatus); err == sql.ErrNoRows {
		return Product{}, platform.Validation(map[string][]string{"type_id": {"商品类型不存在"}})
	} else if err != nil {
		return Product{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO products(id,type_id,sku,name,description,inventory,status,created_by,updated_by,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, id, input.TypeID, input.SKU, input.Name, input.Description, inventory, input.Status, actor.ID, actor.ID, stamp, stamp)
	if err != nil {
		return Product{}, conflictFromUnique(err, "sku_in_use", "SKU 已存在")
	}
	after := Product{ID: id, TypeID: input.TypeID, TypeName: typeName, SKU: input.SKU, Name: input.Name, Description: input.Description, Inventory: inventory, Status: input.Status, Version: 1, CreatedAt: stamp, UpdatedAt: stamp}
	if err := insertAudit(ctx, tx, actor.ID, "product_create", "product", id, map[string]any{}, after, input.Reason, requestID, now); err != nil {
		return Product{}, err
	}
	if err := tx.Commit(); err != nil {
		return Product{}, err
	}
	return after, nil
}

func (s *Service) UpdateProduct(ctx context.Context, actor identity.User, requestID, id string, input ProductInput) (Product, error) {
	input, err := validateProduct(input, false)
	if err != nil {
		return Product{}, err
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Product{}, err
	}
	defer tx.Rollback()
	before, err := scanProduct(tx.QueryRowContext(ctx, productSelect+` WHERE p.id=?`, id))
	if err == sql.ErrNoRows {
		return Product{}, platform.Problem(http.StatusNotFound, "product_not_found", "商品不存在")
	}
	if err != nil {
		return Product{}, err
	}
	if input.TypeID == "" {
		input.TypeID = before.TypeID
	}
	if input.SKU == "" {
		input.SKU = before.SKU
	}
	if input.Name == "" {
		input.Name = before.Name
	}
	if input.Description == "" {
		input.Description = before.Description
	}
	if input.Inventory == nil {
		v := before.Inventory
		input.Inventory = &v
	}
	if input.Status == "" {
		input.Status = before.Status
	}
	var typeName string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM product_types WHERE id=?`, input.TypeID).Scan(&typeName); err == sql.ErrNoRows {
		return Product{}, platform.Validation(map[string][]string{"type_id": {"商品类型不存在"}})
	} else if err != nil {
		return Product{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE products SET type_id=?,sku=?,name=?,description=?,inventory=?,status=?,version=version+1,updated_by=?,updated_at=? WHERE id=?`, input.TypeID, input.SKU, input.Name, input.Description, *input.Inventory, input.Status, actor.ID, stamp, id)
	if err != nil {
		return Product{}, conflictFromUnique(err, "sku_in_use", "SKU 已存在")
	}
	after := Product{ID: id, TypeID: input.TypeID, TypeName: typeName, SKU: input.SKU, Name: input.Name, Description: input.Description, Inventory: *input.Inventory, Status: input.Status, Version: before.Version + 1, CreatedAt: before.CreatedAt, UpdatedAt: stamp}
	if err := insertAudit(ctx, tx, actor.ID, "product_update", "product", id, before, after, input.Reason, requestID, now); err != nil {
		return Product{}, err
	}
	if err := tx.Commit(); err != nil {
		return Product{}, err
	}
	return after, nil
}

func (s *Service) DeleteProduct(ctx context.Context, actor identity.User, requestID, id, reason string) error {
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, err := scanProduct(tx.QueryRowContext(ctx, productSelect+` WHERE p.id=?`, id))
	if err == sql.ErrNoRows {
		return platform.Problem(http.StatusNotFound, "product_not_found", "商品不存在")
	}
	if err != nil {
		return err
	}
	var ordered int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM commerce_order_items WHERE product_id=?`, id).Scan(&ordered); err != nil {
		return err
	}
	if ordered > 0 {
		return platform.Problem(http.StatusConflict, "product_in_orders", "已进入订单的商品只能归档")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM products WHERE id=?`, id); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, actor.ID, "product_delete", "product", id, before, map[string]any{}, strings.TrimSpace(reason), requestID, now); err != nil {
		return err
	}
	return tx.Commit()
}
