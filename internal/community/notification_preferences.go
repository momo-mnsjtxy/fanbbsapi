package community

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

var notificationKinds = [...]string{"comment", "like", "repost", "follow", "moderation", "message"}

func validNotificationKind(kind string) bool {
	for _, candidate := range notificationKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}

type NotificationCounts struct {
	Total  int64            `json:"total"`
	ByType map[string]int64 `json:"by_type"`
}

// NotificationUnreadCounts derives all badges in a single query snapshot. It
// includes historical rows even if the recipient later mutes that event type.
func (s *Service) NotificationUnreadCounts(ctx context.Context, userID string) (NotificationCounts, error) {
	result := NotificationCounts{ByType: make(map[string]int64, len(notificationKinds))}
	for _, kind := range notificationKinds {
		result.ByType[kind] = 0
	}
	rows, err := s.db.QueryContext(ctx, `SELECT type, COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL AND muted = 0 GROUP BY type`, userID)
	if err != nil {
		return result, fmt.Errorf("count unread notifications: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var count int64
		if err := rows.Scan(&kind, &count); err != nil {
			return result, fmt.Errorf("scan notification count: %w", err)
		}
		result.ByType[kind] = count
		result.Total += count
	}
	return result, rows.Err()
}

// NotificationPreferences always returns a complete map. Missing stored rows
// default to enabled; mandatory moderation notices cannot be muted.
func (s *Service) NotificationPreferences(ctx context.Context, userID string) (map[string]bool, error) {
	result := make(map[string]bool, len(notificationKinds))
	for _, kind := range notificationKinds {
		result[kind] = true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT type, enabled FROM notification_preferences WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("list notification preferences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var enabled int
		if err := rows.Scan(&kind, &enabled); err != nil {
			return nil, fmt.Errorf("scan notification preference: %w", err)
		}
		result[kind] = enabled == 1
	}
	return result, rows.Err()
}

// UpdateNotificationPreferences patches only explicit booleans and commits the
// batch atomically. Null/missing values cannot silently disable notifications.
func (s *Service) UpdateNotificationPreferences(ctx context.Context, userID string, changes map[string]*bool) error {
	if len(changes) == 0 {
		return platform.Validation(map[string][]string{"preferences": {"请提供至少一项通知偏好"}})
	}
	kinds := make([]string, 0, len(changes))
	errors := make(map[string][]string)
	for kind, value := range changes {
		switch {
		case !validNotificationKind(kind):
			errors["preferences."+kind] = []string{"通知类型无效"}
		case kind == "moderation":
			errors["preferences."+kind] = []string{"审核通知不能修改"}
		case value == nil:
			errors["preferences."+kind] = []string{"必须是 true 或 false"}
		default:
			kinds = append(kinds, kind)
		}
	}
	if len(errors) > 0 {
		return platform.Validation(errors)
	}
	sort.Strings(kinds)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin notification preferences: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Format(time.RFC3339Nano)
	for _, kind := range kinds {
		enabled := 0
		if *changes[kind] {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_preferences(user_id, type, enabled, updated_at)
   VALUES (?, ?, ?, ?) ON CONFLICT(user_id, type) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
			userID, kind, enabled, now); err != nil {
			return fmt.Errorf("write notification preference: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notification preferences: %w", err)
	}
	return nil
}

func (s *Service) notificationCountsHTTP(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.UserFromContext(r.Context())
	counts, err := s.NotificationUnreadCounts(r.Context(), user.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, counts)
}

func (s *Service) notificationPreferencesHTTP(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.UserFromContext(r.Context())
	preferences, err := s.NotificationPreferences(r.Context(), user.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"preferences": preferences})
}

func (s *Service) updateNotificationPreferencesHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Preferences map[string]*bool `json:"preferences"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	user, _ := identity.UserFromContext(r.Context())
	if err := s.UpdateNotificationPreferences(r.Context(), user.ID, input.Preferences); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	s.notificationPreferencesHTTP(w, r)
}
