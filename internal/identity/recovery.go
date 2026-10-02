package identity

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fanbbs.local/backend/internal/platform"
)

type recoveryRecord struct {
	id   string
	hash string
}

type RecoveryResult struct {
	Changed       bool     `json:"changed"`
	RecoveryCodes []string `json:"recovery_codes"`
}

func normalizeRecoveryCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}

func newRecoveryCodes(count int) ([]string, []recoveryRecord, error) {
	plaintext := make([]string, 0, count)
	records := make([]recoveryRecord, 0, count)
	for index := 0; index < count; index++ {
		random := make([]byte, 12)
		if _, err := rand.Read(random); err != nil {
			return nil, nil, fmt.Errorf("generate recovery code: %w", err)
		}
		compact := strings.TrimRight(base32.StdEncoding.EncodeToString(random), "=")
		parts := []string{}
		for start := 0; start < len(compact); start += 5 {
			end := start + 5
			if end > len(compact) {
				end = len(compact)
			}
			parts = append(parts, compact[start:end])
		}
		code := strings.Join(parts, "-")
		id, err := platform.NewID("rcv")
		if err != nil {
			return nil, nil, err
		}
		plaintext = append(plaintext, code)
		records = append(records, recoveryRecord{id: id, hash: tokenHash(normalizeRecoveryCode(code))})
	}
	return plaintext, records, nil
}

func insertRecoveryCodes(ctx context.Context, runner sqlRunner, userID string, records []recoveryRecord, now time.Time) error {
	for _, record := range records {
		if _, err := runner.ExecContext(ctx, `
			INSERT INTO recovery_codes(id, user_id, code_hash, created_at) VALUES (?, ?, ?, ?)`,
			record.id, userID, record.hash, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("store recovery code: %w", err)
		}
	}
	return nil
}

func (s *Service) rotateRecoveryCodes(ctx context.Context, userID, currentPassword string) ([]string, error) {
	if currentPassword == "" {
		return nil, platform.Validation(map[string][]string{"current_password": {"当前密码不能为空"}})
	}
	var encoded string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ? AND status = 'active'`, userID).Scan(&encoded); err != nil {
		return nil, fmt.Errorf("load recovery password: %w", err)
	}
	if !checkPassword(currentPassword, encoded) {
		return nil, platform.Problem(http.StatusUnauthorized, "invalid_current_password", "当前密码不正确")
	}
	plaintext, records, err := newRecoveryCodes(8)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin recovery rotation: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE recovery_codes SET revoked_at = ? WHERE user_id = ? AND used_at IS NULL AND revoked_at IS NULL`, now.Format(time.RFC3339Nano), userID); err != nil {
		return nil, fmt.Errorf("revoke recovery codes: %w", err)
	}
	if err := insertRecoveryCodes(ctx, tx, userID, records, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit recovery rotation: %w", err)
	}
	return plaintext, nil
}

func (s *Service) Recover(ctx context.Context, account, code, newPassword string) (RecoveryResult, error) {
	account = strings.ToLower(strings.TrimSpace(account))
	code = normalizeRecoveryCode(code)
	fields := map[string][]string{}
	if account == "" {
		fields["account"] = []string{"账号不能为空"}
	}
	if len(code) < 16 || len(code) > 32 {
		fields["recovery_code"] = []string{"恢复码格式不正确"}
	}
	if len(newPassword) < 8 || len(newPassword) > 128 {
		fields["new_password"] = []string{"新密码长度必须为 8 到 128 个字符"}
	}
	if len(fields) > 0 {
		return RecoveryResult{}, platform.Validation(fields)
	}
	newHash, err := hashPassword(newPassword)
	if err != nil {
		return RecoveryResult{}, err
	}
	plaintext, records, err := newRecoveryCodes(8)
	if err != nil {
		return RecoveryResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("begin account recovery: %w", err)
	}
	defer tx.Rollback()
	var userID, recoveryID string
	err = tx.QueryRowContext(ctx, `
		SELECT u.id, r.id FROM users u JOIN recovery_codes r ON r.user_id = u.id
		WHERE (lower(u.email) = ? OR lower(u.handle) = ?) AND u.status = 'active'
		  AND r.code_hash = ? AND r.used_at IS NULL AND r.revoked_at IS NULL`,
		account, account, tokenHash(code)).Scan(&userID, &recoveryID)
	if err == sql.ErrNoRows {
		return RecoveryResult{}, platform.Problem(http.StatusUnauthorized, "invalid_recovery_code", "账号或恢复码不正确")
	}
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("verify recovery code: %w", err)
	}
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE recovery_codes SET used_at = ? WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL`, stamp, recoveryID)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("redeem recovery code: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return RecoveryResult{}, platform.Problem(http.StatusUnauthorized, "invalid_recovery_code", "账号或恢复码不正确")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE recovery_codes SET revoked_at = ? WHERE user_id = ? AND id <> ? AND used_at IS NULL AND revoked_at IS NULL`, stamp, userID, recoveryID); err != nil {
		return RecoveryResult{}, fmt.Errorf("rotate redeemed recovery codes: %w", err)
	}
	if err := insertRecoveryCodes(ctx, tx, userID, records, now); err != nil {
		return RecoveryResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`, newHash, stamp, stamp, userID); err != nil {
		return RecoveryResult{}, fmt.Errorf("reset recovered password: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, stamp, userID); err != nil {
		return RecoveryResult{}, fmt.Errorf("revoke recovered sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RecoveryResult{}, fmt.Errorf("commit account recovery: %w", err)
	}
	return RecoveryResult{Changed: true, RecoveryCodes: plaintext}, nil
}

func (s *Service) recoverHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Account      string `json:"account"`
		Identity     string `json:"identity"`
		RecoveryCode string `json:"recovery_code"`
		NewPassword  string `json:"new_password"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if input.Account == "" {
		input.Account = input.Identity
	}
	if err := platform.CheckRateLimit(s.loginLimiter, platform.RemoteHost(r)+":recover:"+strings.ToLower(strings.TrimSpace(input.Account))); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	result, err := s.Recover(r.Context(), input.Account, input.RecoveryCode, input.NewPassword)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, result)
}

func (s *Service) rotateRecoveryCodesHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"current_password"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := UserFromContext(r.Context())
	codes, err := s.rotateRecoveryCodes(r.Context(), current.ID, input.CurrentPassword)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"recovery_codes": codes})
}
