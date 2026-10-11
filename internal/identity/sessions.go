package identity

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

type DeviceSession struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	LastActiveAt     string `json:"last_active_at"`
	Current          bool   `json:"current"`
	AccessExpiresAt  string `json:"access_expires_at"`
	RefreshExpiresAt string `json:"refresh_expires_at"`
	CreatedAt        string `json:"created_at"`
}

func (s *Service) DeviceSessions(ctx context.Context, userID, currentSessionID string) ([]DeviceSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, access_expires_at, refresh_expires_at, created_at, device_name, last_active_unix
		FROM sessions
		WHERE user_id = ? AND revoked_at IS NULL AND refresh_expires_at > ?
		ORDER BY last_active_unix DESC, id DESC`, userID, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("list device sessions: %w", err)
	}
	defer rows.Close()
	items := []DeviceSession{}
	for rows.Next() {
		var item DeviceSession
		var lastActive int64
		if err := rows.Scan(&item.ID, &item.AccessExpiresAt, &item.RefreshExpiresAt, &item.CreatedAt, &item.Name, &lastActive); err != nil {
			return nil, fmt.Errorf("scan device session: %w", err)
		}
		item.LastActiveAt = time.Unix(lastActive, 0).UTC().Format(time.RFC3339)
		item.Current = item.ID == currentSessionID
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) RevokeDeviceSession(ctx context.Context, userID, sessionID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ?
		WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, s.now().UTC().Format(time.RFC3339Nano), sessionID, userID)
	if err != nil {
		return fmt.Errorf("revoke device session: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		var exists int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE id = ? AND user_id = ?`, sessionID, userID).Scan(&exists)
		if err == sql.ErrNoRows {
			return platform.Problem(http.StatusNotFound, "session_not_found", "设备会话不存在")
		}
		if err != nil {
			return fmt.Errorf("check device session: %w", err)
		}
	}
	return nil
}

func (s *Service) sessionsHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := UserFromContext(r.Context())
	currentSessionID, _ := SessionFromContext(r.Context())
	items, err := s.DeviceSessions(r.Context(), current.ID, currentSessionID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, items)
}

func (s *Service) revokeSessionHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := UserFromContext(r.Context())
	sessionID := chi.URLParam(r, "sessionID")
	if err := s.RevokeDeviceSession(r.Context(), current.ID, sessionID); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"revoked": true, "session_id": sessionID})
}

// RenameDeviceSession changes only a live session owned by the caller. It can
// clear a label with an empty string and does not touch credentials or scopes.
func (s *Service) RenameDeviceSession(ctx context.Context, userID, sessionID, name string) (string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", platform.Validation(map[string][]string{"name": {"设备名称最多 80 个字符且不能包含控制字符"}})
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET device_name = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL AND refresh_expires_at > ?`, name, sessionID, userID, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", fmt.Errorf("rename device session: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("check renamed device session: %w", err)
	}
	if changed == 0 {
		return "", platform.Problem(http.StatusNotFound, "session_not_found", "设备会话不存在")
	}
	return name, nil
}

func (s *Service) renameSessionHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name *string `json:"name"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if input.Name == nil {
		platform.WriteError(w, r, platform.Validation(map[string][]string{"name": {"请提供设备名称"}}))
		return
	}
	current, _ := UserFromContext(r.Context())
	sessionID := chi.URLParam(r, "sessionID")
	name, err := s.RenameDeviceSession(r.Context(), current.ID, sessionID, *input.Name)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]string{"session_id": sessionID, "name": name})
}
