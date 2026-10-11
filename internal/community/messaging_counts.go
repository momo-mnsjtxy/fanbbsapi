package community

import (
	"context"
	"fmt"
	"net/http"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

type ConversationUnreadCounts struct {
	Conversations int64 `json:"conversations"`
	Messages      int64 `json:"messages"`
}

// ConversationCounts includes only unread messages by other senders in active
// memberships. Inbox muting never changes these authoritative chat counts.
func (s *Service) ConversationCounts(ctx context.Context, userID string) (ConversationUnreadCounts, error) {
	var result ConversationUnreadCounts
	err := s.db.QueryRowContext(ctx, `
  SELECT COUNT(*), COALESCE(SUM(unread_count), 0) FROM (
   SELECT COUNT(*) AS unread_count FROM messages m
    JOIN conversations c ON c.id = m.conversation_id
    JOIN conversation_members cm ON cm.conversation_id = c.id
   WHERE cm.user_id = ? AND cm.left_at IS NULL AND c.status = 'active'
    AND m.sequence > cm.read_sequence AND m.sender_id <> cm.user_id
   GROUP BY c.id
  ) unread`, userID).Scan(&result.Conversations, &result.Messages)
	if err != nil {
		return result, fmt.Errorf("count unread conversations: %w", err)
	}
	return result, nil
}

func (s *Service) conversationUnreadCountsHTTP(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.UserFromContext(r.Context())
	counts, err := s.ConversationCounts(r.Context(), user.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, counts)
}
