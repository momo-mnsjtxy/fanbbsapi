// Package commerce owns local non-payment inventory/orders and non-cash
// gamification. It deliberately has no payment, wallet, withdrawal, raffle,
// paid-content, or external fulfillment dependency.
package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"fanbbs.local/backend/internal/platform"
)

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service { return &Service{db: db, now: time.Now} }

var codePattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

func insertAudit(ctx context.Context, tx *sql.Tx, actorID, action, targetType, targetID string, before, after any, reason, requestID string, now time.Time) error {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("encode audit before: %w", err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("encode audit after: %w", err)
	}
	id, err := platform.NewID("aud")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(id, actor_id, action, target_type, target_id, before_value, after_value, reason, request_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, actorID, action, targetType, targetID, string(beforeJSON), string(afterJSON), reason, requestID, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

func conflictFromUnique(err error, code, message string) error {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return platform.Problem(http.StatusConflict, code, message)
	}
	return err
}

func validateCode(value, field string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 2 || len(value) > 64 || !codePattern.MatchString(value) {
		return "", platform.Validation(map[string][]string{field: {"必须是 2 到 64 位小写字母、数字、连字符或下划线"}})
	}
	return value, nil
}

func page(offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return offset, limit
}
