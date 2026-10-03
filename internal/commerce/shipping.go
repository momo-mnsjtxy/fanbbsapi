package commerce

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

type ShippingAddressInput struct {
	Label         string `json:"label"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Region        string `json:"region"`
	AddressLine   string `json:"address_line"`
	PostalCode    string `json:"postal_code"`
	Default       bool   `json:"is_default"`
}

func validateShippingAddress(input *ShippingAddressInput) error {
	input.Label = strings.TrimSpace(input.Label)
	input.RecipientName = strings.TrimSpace(input.RecipientName)
	input.Phone = strings.TrimSpace(input.Phone)
	input.Region = strings.TrimSpace(input.Region)
	input.AddressLine = strings.TrimSpace(input.AddressLine)
	input.PostalCode = strings.TrimSpace(input.PostalCode)
	fields := map[string][]string{}
	if len([]rune(input.Label)) > 40 {
		fields["label"] = []string{"标签不能超过 40 个字符"}
	}
	if n := len([]rune(input.RecipientName)); n < 1 || n > 80 {
		fields["recipient_name"] = []string{"收件人长度必须为 1 到 80 个字符"}
	}
	if n := len([]rune(input.Phone)); n < 3 || n > 40 {
		fields["phone"] = []string{"联系电话长度必须为 3 到 40 个字符"}
	}
	if n := len([]rune(input.Region)); n < 1 || n > 120 {
		fields["region"] = []string{"地区长度必须为 1 到 120 个字符"}
	}
	if n := len([]rune(input.AddressLine)); n < 1 || n > 300 {
		fields["address_line"] = []string{"详细地址长度必须为 1 到 300 个字符"}
	}
	if len([]rune(input.PostalCode)) > 20 {
		fields["postal_code"] = []string{"邮政编码不能超过 20 个字符"}
	}
	if len(fields) > 0 {
		return platform.Validation(fields)
	}
	return nil
}

const shippingAddressSelect = `SELECT id,label,recipient_name,phone,region,address_line,postal_code,is_default,version,created_at,updated_at FROM shipping_addresses`

func scanShippingAddress(row interface{ Scan(...any) error }) (ShippingAddress, error) {
	var item ShippingAddress
	var defaultValue int
	err := row.Scan(&item.ID, &item.Label, &item.RecipientName, &item.Phone, &item.Region,
		&item.AddressLine, &item.PostalCode, &defaultValue, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	item.Default = defaultValue != 0
	return item, err
}

func (s *Service) ShippingAddresses(ctx context.Context, userID string) ([]ShippingAddress, error) {
	rows, err := s.db.QueryContext(ctx, shippingAddressSelect+` WHERE user_id=? ORDER BY is_default DESC,updated_at DESC,id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list shipping addresses: %w", err)
	}
	defer rows.Close()
	items := []ShippingAddress{}
	for rows.Next() {
		item, err := scanShippingAddress(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateShippingAddress(ctx context.Context, userID string, input ShippingAddressInput) (ShippingAddress, error) {
	if err := validateShippingAddress(&input); err != nil {
		return ShippingAddress{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ShippingAddress{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shipping_addresses WHERE user_id=?`, userID).Scan(&count); err != nil {
		return ShippingAddress{}, err
	}
	makeDefault := input.Default || count == 0
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	if makeDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE shipping_addresses SET is_default=0,version=version+1,updated_at=? WHERE user_id=? AND is_default=1`, stamp, userID); err != nil {
			return ShippingAddress{}, err
		}
	}
	id, err := platform.NewID("addr")
	if err != nil {
		return ShippingAddress{}, err
	}
	defaultValue := 0
	if makeDefault {
		defaultValue = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO shipping_addresses(id,user_id,label,recipient_name,phone,region,address_line,postal_code,is_default,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, userID, input.Label, input.RecipientName, input.Phone, input.Region, input.AddressLine, input.PostalCode, defaultValue, stamp, stamp); err != nil {
		return ShippingAddress{}, fmt.Errorf("create shipping address: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ShippingAddress{}, err
	}
	return scanShippingAddress(s.db.QueryRowContext(ctx, shippingAddressSelect+` WHERE id=? AND user_id=?`, id, userID))
}

func (s *Service) UpdateShippingAddress(ctx context.Context, addressID, userID string, expectedVersion int, input ShippingAddressInput) (ShippingAddress, error) {
	if err := validateShippingAddress(&input); err != nil {
		return ShippingAddress{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ShippingAddress{}, err
	}
	defer tx.Rollback()
	var currentVersion, currentDefault int
	if err := tx.QueryRowContext(ctx, `SELECT version,is_default FROM shipping_addresses WHERE id=? AND user_id=?`, addressID, userID).Scan(&currentVersion, &currentDefault); err == sql.ErrNoRows {
		return ShippingAddress{}, platform.Problem(http.StatusNotFound, "shipping_address_not_found", "收货地址不存在")
	} else if err != nil {
		return ShippingAddress{}, err
	}
	if currentVersion != expectedVersion {
		return ShippingAddress{}, platform.Problem(http.StatusConflict, "version_conflict", "收货地址已被其他操作更新，请刷新后重试")
	}
	if currentDefault == 1 && !input.Default {
		return ShippingAddress{}, platform.Validation(map[string][]string{"is_default": {"请先将另一个地址设为默认地址"}})
	}
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	if input.Default {
		if _, err := tx.ExecContext(ctx, `UPDATE shipping_addresses SET is_default=0,version=version+1,updated_at=? WHERE user_id=? AND id<>? AND is_default=1`, stamp, userID, addressID); err != nil {
			return ShippingAddress{}, err
		}
	}
	defaultValue := 0
	if input.Default {
		defaultValue = 1
	}
	result, err := tx.ExecContext(ctx, `UPDATE shipping_addresses SET label=?,recipient_name=?,phone=?,region=?,address_line=?,postal_code=?,is_default=?,version=version+1,updated_at=? WHERE id=? AND user_id=? AND version=?`,
		input.Label, input.RecipientName, input.Phone, input.Region, input.AddressLine, input.PostalCode, defaultValue, stamp, addressID, userID, expectedVersion)
	if err != nil {
		return ShippingAddress{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ShippingAddress{}, platform.Problem(http.StatusConflict, "version_conflict", "收货地址已被其他操作更新，请刷新后重试")
	}
	if err := tx.Commit(); err != nil {
		return ShippingAddress{}, err
	}
	return scanShippingAddress(s.db.QueryRowContext(ctx, shippingAddressSelect+` WHERE id=? AND user_id=?`, addressID, userID))
}

func (s *Service) DeleteShippingAddress(ctx context.Context, addressID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var wasDefault int
	if err := tx.QueryRowContext(ctx, `SELECT is_default FROM shipping_addresses WHERE id=? AND user_id=?`, addressID, userID).Scan(&wasDefault); err == sql.ErrNoRows {
		return platform.Problem(http.StatusNotFound, "shipping_address_not_found", "收货地址不存在")
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM shipping_addresses WHERE id=? AND user_id=?`, addressID, userID); err != nil {
		return err
	}
	if wasDefault == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE shipping_addresses SET is_default=1,version=version+1,updated_at=? WHERE id=(SELECT id FROM shipping_addresses WHERE user_id=? ORDER BY updated_at DESC,id DESC LIMIT 1)`, s.now().UTC().Format(time.RFC3339Nano), userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type TrackingEventInput struct {
	Status      string `json:"status"`
	Description string `json:"description"`
	Location    string `json:"location"`
	OccurredAt  string `json:"occurred_at"`
	Reason      string `json:"reason"`
}

func (s *Service) AddTrackingEvent(ctx context.Context, actor identity.User, requestID, orderID string, input TrackingEventInput) (TrackingEvent, error) {
	input.Status = strings.TrimSpace(input.Status)
	input.Description = strings.TrimSpace(input.Description)
	input.Location = strings.TrimSpace(input.Location)
	input.OccurredAt = strings.TrimSpace(input.OccurredAt)
	input.Reason = strings.TrimSpace(input.Reason)
	allowed := map[string]bool{"label_created": true, "in_transit": true, "out_for_delivery": true, "delivered": true, "exception": true}
	fields := map[string][]string{}
	if !allowed[input.Status] {
		fields["status"] = []string{"物流状态无效"}
	}
	if len([]rune(input.Description)) > 500 {
		fields["description"] = []string{"说明不能超过 500 个字符"}
	}
	if len([]rune(input.Location)) > 200 {
		fields["location"] = []string{"地点不能超过 200 个字符"}
	}
	if len([]rune(input.Reason)) > 500 {
		fields["reason"] = []string{"审计原因不能超过 500 个字符"}
	}
	occurred := s.now().UTC()
	if input.OccurredAt != "" {
		parsed, err := time.Parse(time.RFC3339, input.OccurredAt)
		if err != nil {
			fields["occurred_at"] = []string{"必须是 RFC3339 时间"}
		} else {
			occurred = parsed.UTC()
		}
	}
	if len(fields) > 0 {
		return TrackingEvent{}, platform.Validation(fields)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TrackingEvent{}, err
	}
	defer tx.Rollback()
	var orderStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM commerce_orders WHERE id=?`, orderID).Scan(&orderStatus); err == sql.ErrNoRows {
		return TrackingEvent{}, platform.Problem(http.StatusNotFound, "order_not_found", "订单不存在")
	} else if err != nil {
		return TrackingEvent{}, err
	}
	if orderStatus != "fulfilled" {
		return TrackingEvent{}, platform.Problem(http.StatusConflict, "tracking_unavailable", "只有已手工履约的订单可以记录物流事件")
	}
	id, err := platform.NewID("trk")
	if err != nil {
		return TrackingEvent{}, err
	}
	now := s.now().UTC()
	item := TrackingEvent{ID: id, Status: input.Status, Description: input.Description, Location: input.Location, Source: "manual", OccurredAt: occurred.Format(time.RFC3339Nano), CreatedAt: now.Format(time.RFC3339Nano)}
	if _, err := tx.ExecContext(ctx, `INSERT INTO order_tracking_events(id,order_id,status,description,location,occurred_at,created_by,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		id, orderID, item.Status, item.Description, item.Location, item.OccurredAt, actor.ID, item.CreatedAt); err != nil {
		return TrackingEvent{}, err
	}
	if err := insertAudit(ctx, tx, actor.ID, "order_tracking_append", "order", orderID, map[string]any{}, item, input.Reason, requestID, now); err != nil {
		return TrackingEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return TrackingEvent{}, err
	}
	return item, nil
}

func parseIfMatch(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, platform.Problem(http.StatusPreconditionRequired, "if_match_required", "需要 If-Match 版本")
	}
	raw = strings.Trim(raw, `"`)
	version, err := strconv.Atoi(raw)
	if err != nil || version < 1 {
		return 0, platform.Validation(map[string][]string{"if_match": {"必须是带引号的正整数版本"}})
	}
	return version, nil
}

func (s *Service) addressesHTTP(w http.ResponseWriter, r *http.Request) {
	items, err := s.ShippingAddresses(r.Context(), currentUser(r).ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, items)
}
func (s *Service) createAddressHTTP(w http.ResponseWriter, r *http.Request) {
	var input ShippingAddressInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.CreateShippingAddress(r.Context(), currentUser(r).ID, input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(item.Version)))
	platform.WriteData(w, r, http.StatusCreated, item)
}
func (s *Service) updateAddressHTTP(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var input ShippingAddressInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.UpdateShippingAddress(r.Context(), chi.URLParam(r, "addressID"), currentUser(r).ID, version, input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(item.Version)))
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) deleteAddressHTTP(w http.ResponseWriter, r *http.Request) {
	if err := s.DeleteShippingAddress(r.Context(), chi.URLParam(r, "addressID"), currentUser(r).ID); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"deleted": true})
}
func (s *Service) trackingHTTP(w http.ResponseWriter, r *http.Request) {
	item, err := s.Order(r.Context(), chi.URLParam(r, "orderID"), currentUser(r).ID, false)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item.TrackingEvents)
}
func (s *Service) addTrackingHTTP(w http.ResponseWriter, r *http.Request) {
	var input TrackingEventInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.AddTrackingEvent(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "orderID"), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusCreated, item)
}
