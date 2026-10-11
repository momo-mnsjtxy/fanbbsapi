// Notifications are durable database facts. External push, email, SMS, and
// WebSocket delivery are intentionally outside this local slice.
package community

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

type Notification struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	SubjectType string          `json:"subject_type"`
	SubjectID   string          `json:"subject_id"`
	Payload     json.RawMessage `json:"payload"`
	ReadAt      string          `json:"read_at,omitempty"`
	CreatedAt   string          `json:"created_at"`
	Muted       bool            `json:"muted"`
	Actor       *Author         `json:"actor,omitempty"`
}

func addNotification(ctx context.Context, tx *sql.Tx, recipientID, actorID, kind, subjectType, subjectID string, payload any, now time.Time) error {
	if recipientID == "" || recipientID == actorID {
		return nil
	}
	// Preferences affect future optional inbox visibility only. Durable events
	// still exist for reconnect synchronization, with muted=true to suppress alerts. Moderation notices
	// remain mandatory so decisions and account consequences are never hidden.
	muted := 0
	if kind != "moderation" {
		var enabled int
		err := tx.QueryRowContext(ctx, `SELECT enabled FROM notification_preferences WHERE user_id = ? AND type = ?`, recipientID, kind).Scan(&enabled)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read notification preference: %w", err)
		}
		if err == nil && enabled == 0 {
			muted = 1
		}
	}
	notificationID, err := platform.NewID("ntf")
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode notification payload: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO notifications(id, user_id, type, actor_id, subject_type, subject_id, payload, created_at, muted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, notificationID, recipientID, kind, actorID, subjectType, subjectID, string(encoded), now.UTC().Format(time.RFC3339Nano), muted)
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}
	return nil
}

// Notifications uses a recipient/filter-scoped seek cursor. Reading a page,
// marking its items read, or receiving new events cannot shift the next page.
func (s *Service) Notifications(ctx context.Context, userID string, unreadOnly bool, kind, rawCursor string, limit int) ([]Notification, string, error) {
	if limit < 1 || limit > 50 {
		return nil, "", platform.Validation(map[string][]string{"limit": {"limit 必须是 1 到 50 的整数"}})
	}
	if kind != "" && !validNotificationKind(kind) {
		return nil, "", platform.Validation(map[string][]string{"type": {"通知类型无效"}})
	}
	scope := fmt.Sprintf("notifications:%s:%t:%s", userID, unreadOnly, kind)
	cursor, err := decodeSeekCursor(rawCursor, scope)
	if err != nil {
		return nil, "", err
	}
	if cursor.Timestamp != "" || cursor.ID != "" || (rawCursor != "" && cursor.Sequence == 0) {
		return nil, "", platform.Validation(map[string][]string{"cursor": {"游标无效"}})
	}
	boundary := int64(math.MaxInt64)
	if rawCursor != "" {
		boundary = cursor.Sequence
	}
	where := `n.user_id = ? AND n.sequence < ? AND n.muted = 0`
	args := []any{userID, boundary}
	if unreadOnly {
		where += ` AND n.read_at IS NULL`
	}
	if kind != "" {
		where += ` AND n.type = ?`
		args = append(args, kind)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `
  SELECT n.sequence, n.id, n.type, n.subject_type, n.subject_id, n.payload, COALESCE(n.read_at, ''), n.created_at,
         COALESCE(u.id, ''), COALESCE(u.handle, ''), COALESCE(u.display_name, ''), COALESCE(u.avatar_url, '')
  FROM notifications n LEFT JOIN users u ON u.id = n.actor_id
  WHERE `+where+` ORDER BY n.sequence DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	items := make([]Notification, 0, limit+1)
	sequences := make([]int64, 0, limit+1)
	for rows.Next() {
		var item Notification
		var actor Author
		var payload string
		var sequence int64
		if err := rows.Scan(&sequence, &item.ID, &item.Type, &item.SubjectType, &item.SubjectID, &payload, &item.ReadAt, &item.CreatedAt,
			&actor.ID, &actor.Handle, &actor.DisplayName, &actor.AvatarURL); err != nil {
			return nil, "", fmt.Errorf("scan notification: %w", err)
		}
		item.Payload = json.RawMessage(payload)
		if actor.ID != "" {
			actor.Name = actor.DisplayName
			item.Actor = &actor
		}
		items = append(items, item)
		sequences = append(sequences, sequence)
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = encodeSeekCursor(seekCursor{Scope: scope, Sequence: sequences[limit-1]})
	}
	return items, next, rows.Err()
}

func (s *Service) MarkNotificationsRead(ctx context.Context, userID string, ids []string) (int64, error) {
	now := s.now().UTC().Format(time.RFC3339Nano)
	if len(ids) == 0 {
		result, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at IS NULL`, now, userID)
		if err != nil {
			return 0, fmt.Errorf("mark all notifications read: %w", err)
		}
		changed, _ := result.RowsAffected()
		return changed, nil
	}
	if len(ids) > 100 {
		return 0, platform.Validation(map[string][]string{"ids": {"一次最多标记 100 条通知"}})
	}
	placeholders := make([]string, len(ids))
	args := []any{now, userID}
	for index, id := range ids {
		placeholders[index] = "?"
		args = append(args, strings.TrimSpace(id))
	}
	result, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at IS NULL AND id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("mark notifications read: %w", err)
	}
	changed, _ := result.RowsAffected()
	return changed, nil
}

func (s *Service) notificationsHTTP(w http.ResponseWriter, r *http.Request) {
	limit, err := validateLimit(r.URL.Query().Get("limit"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	unreadValue := r.URL.Query().Get("unread")
	if unreadValue != "" && unreadValue != "true" && unreadValue != "false" {
		platform.WriteError(w, r, platform.Validation(map[string][]string{"unread": {"unread 必须是 true 或 false"}}))
		return
	}
	unread := unreadValue == "true"
	items, next, err := s.Notifications(r.Context(), current.ID, unread, r.URL.Query().Get("type"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) markNotificationsReadHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	changed, err := s.MarkNotificationsRead(r.Context(), current.ID, input.IDs)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"read": true, "changed": changed})
}
