// Private messaging is a database-backed domain. HTTP is the source of truth;
// clients may poll the event cursor after reconnecting, without WebSockets or
// an external push provider.
package community

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

type ConversationMember struct {
	User     Author `json:"user"`
	Role     string `json:"role"`
	JoinedAt string `json:"joined_at"`
	ReadAt   string `json:"read_at,omitempty"`
	LeftAt   string `json:"left_at,omitempty"`
}

type Message struct {
	ID              string `json:"id"`
	ConversationID  string `json:"conversation_id"`
	ClientMessageID string `json:"client_message_id"`
	Body            string `json:"body"`
	CreatedAt       string `json:"created_at"`
	Sender          Author `json:"sender"`
	sequence        int64
}

type Conversation struct {
	ID                string               `json:"id"`
	Title             string               `json:"title"`
	Status            string               `json:"status"`
	CreatedAt         string               `json:"created_at"`
	UpdatedAt         string               `json:"updated_at"`
	Members           []ConversationMember `json:"members"`
	LastMessage       *Message             `json:"last_message,omitempty"`
	LastReadMessageID string               `json:"last_read_message_id,omitempty"`
	activitySequence  int64
}

type CreateConversationInput struct {
	MemberIDs []string `json:"member_ids"`
	Title     string   `json:"title"`
}

type ConversationReceipt struct {
	ConversationID    string `json:"conversation_id"`
	LastReadMessageID string `json:"last_read_message_id,omitempty"`
	ReadAt            string `json:"read_at,omitempty"`
	Changed           bool   `json:"changed"`
}

type realtimeEvent struct {
	Cursor       string       `json:"cursor"`
	Type         string       `json:"type"`
	Notification Notification `json:"notification"`
	sequence     int64
}

type seekCursor struct {
	Version   int    `json:"v"`
	Scope     string `json:"s"`
	Timestamp string `json:"t,omitempty"`
	ID        string `json:"i,omitempty"`
	Sequence  int64  `json:"q,omitempty"`
}

func encodeSeekCursor(cursor seekCursor) string {
	cursor.Version = 1
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeSeekCursor(raw, scope string) (seekCursor, error) {
	if raw == "" {
		return seekCursor{Version: 1, Scope: scope}, nil
	}
	if len(raw) > 2048 {
		return seekCursor{}, platform.Validation(map[string][]string{"cursor": {"游标无效"}})
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 1024 {
		return seekCursor{}, platform.Validation(map[string][]string{"cursor": {"游标无效"}})
	}
	var cursor seekCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Version != 1 || cursor.Scope != scope || cursor.Sequence < 0 {
		return seekCursor{}, platform.Validation(map[string][]string{"cursor": {"游标无效或不属于当前资源"}})
	}
	return cursor, nil
}

func validateLimit(raw string) (int, error) {
	if raw == "" {
		return 20, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 50 {
		return 0, platform.Validation(map[string][]string{"limit": {"limit 必须是 1 到 50 的整数"}})
	}
	return limit, nil
}

func conversationMemberKey(members map[string]bool) string {
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	digest := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return hex.EncodeToString(digest[:])
}

func (s *Service) CreateConversation(ctx context.Context, creatorID string, input CreateConversationInput) (Conversation, bool, error) {
	input.Title = strings.TrimSpace(input.Title)
	fields := map[string][]string{}
	if len([]rune(input.Title)) > 100 {
		fields["title"] = []string{"会话标题不能超过 100 个字符"}
	}
	if len(input.MemberIDs) < 1 || len(input.MemberIDs) > 19 {
		fields["member_ids"] = []string{"会话需要 1 到 19 位其他成员"}
	}
	members := map[string]bool{creatorID: true}
	for _, raw := range input.MemberIDs {
		memberID := strings.TrimSpace(raw)
		if memberID == "" || memberID == creatorID {
			fields["member_ids"] = []string{"成员不能为空且不能包含自己"}
			continue
		}
		if members[memberID] {
			fields["member_ids"] = []string{"成员不能重复"}
			continue
		}
		members[memberID] = true
	}
	if len(fields) > 0 {
		return Conversation{}, false, platform.Validation(fields)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, false, fmt.Errorf("begin conversation: %w", err)
	}
	defer tx.Rollback()
	for memberID := range members {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM users WHERE id = ?`, memberID).Scan(&status); err == sql.ErrNoRows {
			return Conversation{}, false, platform.Validation(map[string][]string{"member_ids": {"成员不存在"}})
		} else if err != nil {
			return Conversation{}, false, fmt.Errorf("check conversation member: %w", err)
		} else if status != "active" {
			return Conversation{}, false, platform.Validation(map[string][]string{"member_ids": {"成员账号不可用"}})
		}
	}
	memberList := make([]string, 0, len(members))
	for memberID := range members {
		memberList = append(memberList, memberID)
	}
	for left := 0; left < len(memberList); left++ {
		for right := left + 1; right < len(memberList); right++ {
			var blocked bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM blocks WHERE (blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?))`,
				memberList[left], memberList[right], memberList[right], memberList[left]).Scan(&blocked); err != nil {
				return Conversation{}, false, fmt.Errorf("check conversation block: %w", err)
			}
			if blocked {
				return Conversation{}, false, platform.Problem(http.StatusConflict, "relationship_blocked", "屏蔽关系下不能创建会话")
			}
		}
	}
	memberKey := conversationMemberKey(members)
	var existingID, existingStatus string
	var hasLeftMembers bool
	err = tx.QueryRowContext(ctx, `
		SELECT c.id, c.status, EXISTS(SELECT 1 FROM conversation_members cm WHERE cm.conversation_id = c.id AND cm.left_at IS NOT NULL)
		FROM conversations c WHERE c.member_key = ?`, memberKey).Scan(&existingID, &existingStatus, &hasLeftMembers)
	if err == nil {
		if existingStatus != "active" || hasLeftMembers {
			return Conversation{}, false, platform.Problem(http.StatusConflict, "conversation_membership_closed", "该成员组合已有成员退出，不能静默重新加入")
		}
		_ = tx.Rollback()
		item, loadErr := s.Conversation(ctx, existingID, creatorID)
		return item, true, loadErr
	}
	if err != sql.ErrNoRows {
		return Conversation{}, false, fmt.Errorf("check existing conversation: %w", err)
	}
	conversationID, err := platform.NewID("cnv")
	if err != nil {
		return Conversation{}, false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `INSERT INTO conversations(id, created_by, member_key, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, conversationID, creatorID, memberKey, input.Title, now, now)
	if err != nil {
		return Conversation{}, false, fmt.Errorf("create conversation: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Conversation{}, false, fmt.Errorf("check conversation insert: %w", err)
	}
	if inserted == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE member_key = ? AND status = 'active'`, memberKey).Scan(&existingID); err != nil {
			return Conversation{}, false, fmt.Errorf("load concurrent conversation: %w", err)
		}
		_ = tx.Rollback()
		item, loadErr := s.Conversation(ctx, existingID, creatorID)
		return item, true, loadErr
	}
	for memberID := range members {
		role := "member"
		if memberID == creatorID {
			role = "owner"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_members(conversation_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)`, conversationID, memberID, role, now); err != nil {
			return Conversation{}, false, fmt.Errorf("add conversation member: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, false, fmt.Errorf("commit conversation: %w", err)
	}
	item, err := s.Conversation(ctx, conversationID, creatorID)
	return item, false, err
}

func (s *Service) Conversation(ctx context.Context, conversationID, viewerID string) (Conversation, error) {
	var item Conversation
	var lastMessageID string
	var readSequence int64
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.title, c.status, c.created_at, c.updated_at, c.activity_sequence, COALESCE(c.last_message_id, ''), cm.read_sequence
		FROM conversations c JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE c.id = ? AND cm.user_id = ? AND cm.left_at IS NULL AND c.status = 'active'`, conversationID, viewerID).
		Scan(&item.ID, &item.Title, &item.Status, &item.CreatedAt, &item.UpdatedAt, &item.activitySequence, &lastMessageID, &readSequence)
	if err == sql.ErrNoRows {
		return Conversation{}, platform.Problem(http.StatusNotFound, "conversation_not_found", "会话不存在")
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("load conversation: %w", err)
	}
	members, err := s.conversationMembers(ctx, conversationID)
	if err != nil {
		return Conversation{}, err
	}
	item.Members = members
	if lastMessageID != "" {
		message, err := s.message(ctx, conversationID, lastMessageID)
		if err != nil {
			return Conversation{}, err
		}
		item.LastMessage = &message
	}
	if readSequence > 0 {
		_ = s.db.QueryRowContext(ctx, `SELECT id FROM messages WHERE conversation_id = ? AND sequence = ?`, conversationID, readSequence).Scan(&item.LastReadMessageID)
	}
	return item, nil
}

func (s *Service) conversationMembers(ctx context.Context, conversationID string) ([]ConversationMember, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.handle, u.display_name, u.avatar_url, cm.role, cm.joined_at, COALESCE(cm.read_at, ''), COALESCE(cm.left_at, '')
		FROM conversation_members cm JOIN users u ON u.id = cm.user_id
		WHERE cm.conversation_id = ? ORDER BY cm.joined_at, u.id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list conversation members: %w", err)
	}
	defer rows.Close()
	items := []ConversationMember{}
	for rows.Next() {
		var item ConversationMember
		if err := rows.Scan(&item.User.ID, &item.User.Handle, &item.User.DisplayName, &item.User.AvatarURL, &item.Role, &item.JoinedAt, &item.ReadAt, &item.LeftAt); err != nil {
			return nil, fmt.Errorf("scan conversation member: %w", err)
		}
		item.User.Name = item.User.DisplayName
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) Conversations(ctx context.Context, viewerID, rawCursor string, limit int) ([]Conversation, string, error) {
	scope := "conversations:" + viewerID
	cursor, err := decodeSeekCursor(rawCursor, scope)
	if err != nil {
		return nil, "", err
	}
	if cursor.Timestamp != "" || (rawCursor != "" && cursor.ID == "") {
		return nil, "", platform.Validation(map[string][]string{"cursor": {"游标无效"}})
	}
	where := ""
	args := []any{viewerID}
	if rawCursor != "" {
		where = ` AND (c.activity_sequence < ? OR (c.activity_sequence = ? AND c.id < ?))`
		args = append(args, cursor.Sequence, cursor.Sequence, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.title, c.status, c.created_at, c.updated_at, c.activity_sequence, COALESCE(c.last_message_id, '')
		FROM conversations c JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE cm.user_id = ? AND cm.left_at IS NULL AND c.status = 'active'`+where+`
		ORDER BY c.activity_sequence DESC, c.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	type partial struct {
		item          Conversation
		lastMessageID string
	}
	partials := []partial{}
	for rows.Next() {
		var p partial
		if err := rows.Scan(&p.item.ID, &p.item.Title, &p.item.Status, &p.item.CreatedAt, &p.item.UpdatedAt, &p.item.activitySequence, &p.lastMessageID); err != nil {
			return nil, "", fmt.Errorf("scan conversation: %w", err)
		}
		partials = append(partials, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	hasMore := len(partials) > limit
	if hasMore {
		partials = partials[:limit]
	}
	items := make([]Conversation, 0, len(partials))
	for _, p := range partials {
		p.item.Members, err = s.conversationMembers(ctx, p.item.ID)
		if err != nil {
			return nil, "", err
		}
		if p.lastMessageID != "" {
			message, err := s.message(ctx, p.item.ID, p.lastMessageID)
			if err != nil {
				return nil, "", err
			}
			p.item.LastMessage = &message
		}
		items = append(items, p.item)
	}
	next := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		next = encodeSeekCursor(seekCursor{Scope: scope, Sequence: last.activitySequence, ID: last.ID})
	}
	return items, next, nil
}

func (s *Service) SendMessage(ctx context.Context, conversationID, senderID, clientMessageID, body string) (Message, bool, error) {
	clientMessageID = strings.TrimSpace(clientMessageID)
	body = strings.TrimSpace(body)
	fields := map[string][]string{}
	if len(clientMessageID) < 1 || len(clientMessageID) > 128 {
		fields["client_message_id"] = []string{"client_message_id 长度必须为 1 到 128 个字符"}
	}
	if len([]rune(body)) < 1 || len([]rune(body)) > 5000 {
		fields["body"] = []string{"消息长度必须为 1 到 5000 个字符"}
	}
	if len(fields) > 0 {
		return Message{}, false, platform.Validation(fields)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, false, fmt.Errorf("begin message: %w", err)
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM conversations c JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE c.id = ? AND c.status = 'active' AND cm.user_id = ? AND cm.left_at IS NULL`, conversationID, senderID).Scan(&active); err == sql.ErrNoRows {
		return Message{}, false, platform.Problem(http.StatusNotFound, "conversation_not_found", "会话不存在")
	} else if err != nil {
		return Message{}, false, fmt.Errorf("authorize message: %w", err)
	}
	var activeMembers int
	var blocked bool
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversation_members WHERE conversation_id = ? AND left_at IS NULL`, conversationID).Scan(&activeMembers); err != nil {
		return Message{}, false, fmt.Errorf("count active conversation members: %w", err)
	}
	if activeMembers < 2 {
		return Message{}, false, platform.Problem(http.StatusConflict, "conversation_has_no_recipients", "会话没有可接收消息的其他成员")
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM conversation_members other JOIN blocks b
			  ON (b.blocker_id = ? AND b.blocked_id = other.user_id) OR (b.blocker_id = other.user_id AND b.blocked_id = ?)
			WHERE other.conversation_id = ? AND other.user_id <> ? AND other.left_at IS NULL
		)`, senderID, senderID, conversationID, senderID).Scan(&blocked); err != nil {
		return Message{}, false, fmt.Errorf("check message block: %w", err)
	}
	if blocked {
		return Message{}, false, platform.Problem(http.StatusConflict, "relationship_blocked", "屏蔽关系下不能发送消息")
	}
	messageID, err := platform.NewID("msg")
	if err != nil {
		return Message{}, false, err
	}
	now := s.now().UTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO messages(id, conversation_id, sender_id, client_message_id, body, created_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		messageID, conversationID, senderID, clientMessageID, body, now.Format(time.RFC3339Nano))
	if err != nil {
		return Message{}, false, fmt.Errorf("create message: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Message{}, false, fmt.Errorf("check message insert: %w", err)
	}
	if inserted == 0 {
		var existingID, existingConversationID, existingBody string
		if err := tx.QueryRowContext(ctx, `SELECT id, conversation_id, body FROM messages WHERE sender_id = ? AND client_message_id = ?`, senderID, clientMessageID).
			Scan(&existingID, &existingConversationID, &existingBody); err != nil {
			return Message{}, false, fmt.Errorf("load message idempotency record: %w", err)
		}
		if existingConversationID != conversationID || existingBody != body {
			return Message{}, false, platform.Problem(http.StatusConflict, "client_message_id_conflict", "client_message_id 已用于不同消息")
		}
		_ = tx.Rollback()
		message, loadErr := s.message(ctx, conversationID, existingID)
		return message, true, loadErr
	}
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM conversation_members WHERE conversation_id = ? AND user_id <> ? AND left_at IS NULL`, conversationID, senderID)
	if err != nil {
		return Message{}, false, fmt.Errorf("list message recipients: %w", err)
	}
	recipients := []string{}
	for rows.Next() {
		var recipientID string
		if err := rows.Scan(&recipientID); err != nil {
			rows.Close()
			return Message{}, false, fmt.Errorf("scan message recipient: %w", err)
		}
		recipients = append(recipients, recipientID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Message{}, false, fmt.Errorf("iterate message recipients: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Message{}, false, err
	}
	for _, recipientID := range recipients {
		if err := addNotification(ctx, tx, recipientID, senderID, "message", "message", messageID, map[string]string{"conversation_id": conversationID}, now); err != nil {
			return Message{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Message{}, false, fmt.Errorf("commit message: %w", err)
	}
	message, err := s.message(ctx, conversationID, messageID)
	return message, false, err
}

func (s *Service) message(ctx context.Context, conversationID, messageID string) (Message, error) {
	var item Message
	err := s.db.QueryRowContext(ctx, `
		SELECT m.sequence, m.id, m.conversation_id, m.client_message_id, m.body, m.created_at,
		       u.id, u.handle, u.display_name, u.avatar_url
		FROM messages m JOIN users u ON u.id = m.sender_id
		WHERE m.conversation_id = ? AND m.id = ?`, conversationID, messageID).
		Scan(&item.sequence, &item.ID, &item.ConversationID, &item.ClientMessageID, &item.Body, &item.CreatedAt,
			&item.Sender.ID, &item.Sender.Handle, &item.Sender.DisplayName, &item.Sender.AvatarURL)
	if err != nil {
		return Message{}, fmt.Errorf("load message: %w", err)
	}
	item.Sender.Name = item.Sender.DisplayName
	return item, nil
}

func (s *Service) Messages(ctx context.Context, conversationID, viewerID, rawCursor string, limit int) ([]Message, string, error) {
	var member int
	if err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM conversations c JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE c.id = ? AND c.status = 'active' AND cm.user_id = ? AND cm.left_at IS NULL`, conversationID, viewerID).Scan(&member); err == sql.ErrNoRows {
		return nil, "", platform.Problem(http.StatusNotFound, "conversation_not_found", "会话不存在")
	} else if err != nil {
		return nil, "", fmt.Errorf("authorize message list: %w", err)
	}
	scope := "messages:" + viewerID + ":" + conversationID
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.sequence, m.id, m.conversation_id, m.client_message_id, m.body, m.created_at,
		       u.id, u.handle, u.display_name, u.avatar_url
		FROM messages m JOIN users u ON u.id = m.sender_id
		WHERE m.conversation_id = ? AND m.sequence < ?
		ORDER BY m.sequence DESC LIMIT ?`, conversationID, boundary, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	items := []Message{}
	for rows.Next() {
		var item Message
		if err := rows.Scan(&item.sequence, &item.ID, &item.ConversationID, &item.ClientMessageID, &item.Body, &item.CreatedAt,
			&item.Sender.ID, &item.Sender.Handle, &item.Sender.DisplayName, &item.Sender.AvatarURL); err != nil {
			return nil, "", fmt.Errorf("scan message: %w", err)
		}
		item.Sender.Name = item.Sender.DisplayName
		items = append(items, item)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	next := ""
	if hasMore && len(items) > 0 {
		next = encodeSeekCursor(seekCursor{Scope: scope, Sequence: items[len(items)-1].sequence})
	}
	return items, next, rows.Err()
}

func (s *Service) Events(ctx context.Context, userID, rawCursor string, limit int) ([]realtimeEvent, string, bool, error) {
	scope := "events:" + userID
	cursor, err := decodeSeekCursor(rawCursor, scope)
	if err != nil {
		return nil, "", false, err
	}
	if cursor.Timestamp != "" || cursor.ID != "" {
		return nil, "", false, platform.Validation(map[string][]string{"cursor": {"游标无效"}})
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.sequence, n.id, n.type, n.subject_type, n.subject_id, n.payload,
		       COALESCE(n.read_at, ''), n.created_at,
		       COALESCE(u.id, ''), COALESCE(u.handle, ''), COALESCE(u.display_name, ''), COALESCE(u.avatar_url, '')
		FROM notifications n LEFT JOIN users u ON u.id = n.actor_id
		WHERE n.user_id = ? AND n.sequence > ? ORDER BY n.sequence ASC LIMIT ?`, userID, cursor.Sequence, limit+1)
	if err != nil {
		return nil, "", false, fmt.Errorf("list reconnect events: %w", err)
	}
	defer rows.Close()
	items := []realtimeEvent{}
	lastSequence := cursor.Sequence
	for rows.Next() {
		var event realtimeEvent
		var payload string
		var actor Author
		if err := rows.Scan(&event.sequence, &event.Notification.ID, &event.Notification.Type, &event.Notification.SubjectType,
			&event.Notification.SubjectID, &payload, &event.Notification.ReadAt, &event.Notification.CreatedAt,
			&actor.ID, &actor.Handle, &actor.DisplayName, &actor.AvatarURL); err != nil {
			return nil, "", false, fmt.Errorf("scan reconnect event: %w", err)
		}
		event.Notification.Payload = json.RawMessage(payload)
		if actor.ID != "" {
			actor.Name = actor.DisplayName
			event.Notification.Actor = &actor
		}
		event.Type = "notification.created"
		event.Cursor = encodeSeekCursor(seekCursor{Scope: scope, Sequence: event.sequence})
		lastSequence = event.sequence
		items = append(items, event)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
		lastSequence = items[len(items)-1].sequence
	}
	next := encodeSeekCursor(seekCursor{Scope: scope, Sequence: lastSequence})
	return items, next, hasMore, rows.Err()
}

func (s *Service) MarkConversationRead(ctx context.Context, conversationID, userID, messageID string) (ConversationReceipt, error) {
	messageID = strings.TrimSpace(messageID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConversationReceipt{}, fmt.Errorf("begin conversation receipt: %w", err)
	}
	defer tx.Rollback()
	var currentSequence int64
	var currentReadAt string
	err = tx.QueryRowContext(ctx, `
		SELECT cm.read_sequence, COALESCE(cm.read_at, '')
		FROM conversation_members cm JOIN conversations c ON c.id = cm.conversation_id
		WHERE cm.conversation_id = ? AND cm.user_id = ? AND cm.left_at IS NULL AND c.status = 'active'`,
		conversationID, userID).Scan(&currentSequence, &currentReadAt)
	if err == sql.ErrNoRows {
		return ConversationReceipt{}, platform.Problem(http.StatusNotFound, "conversation_not_found", "会话不存在")
	}
	if err != nil {
		return ConversationReceipt{}, fmt.Errorf("authorize conversation receipt: %w", err)
	}
	targetSequence := int64(0)
	targetMessageID := messageID
	if messageID == "" {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM messages WHERE conversation_id = ?`, conversationID).Scan(&targetSequence); err != nil {
			return ConversationReceipt{}, fmt.Errorf("load latest conversation message: %w", err)
		}
		if targetSequence > 0 {
			if err := tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE conversation_id = ? AND sequence = ?`, conversationID, targetSequence).Scan(&targetMessageID); err != nil {
				return ConversationReceipt{}, fmt.Errorf("load latest message id: %w", err)
			}
		}
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT sequence FROM messages WHERE conversation_id = ? AND id = ?`, conversationID, messageID).Scan(&targetSequence); err == sql.ErrNoRows {
			return ConversationReceipt{}, platform.Problem(http.StatusNotFound, "message_not_found", "消息不存在")
		} else if err != nil {
			return ConversationReceipt{}, fmt.Errorf("load receipt message: %w", err)
		}
	}
	result := ConversationReceipt{ConversationID: conversationID, LastReadMessageID: targetMessageID, ReadAt: currentReadAt}
	if targetSequence <= currentSequence {
		if currentSequence > 0 {
			_ = tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE conversation_id = ? AND sequence = ?`, conversationID, currentSequence).Scan(&result.LastReadMessageID)
		}
		return result, nil
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE conversation_members SET read_sequence = ?, read_at = ? WHERE conversation_id = ? AND user_id = ? AND left_at IS NULL`,
		targetSequence, now, conversationID, userID); err != nil {
		return ConversationReceipt{}, fmt.Errorf("update conversation receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ConversationReceipt{}, fmt.Errorf("commit conversation receipt: %w", err)
	}
	result.ReadAt = now
	result.Changed = true
	return result, nil
}

func (s *Service) LeaveConversation(ctx context.Context, conversationID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin leave conversation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE conversation_members SET left_at = ?
		WHERE conversation_id = ? AND user_id = ? AND left_at IS NULL
		  AND EXISTS(SELECT 1 FROM conversations c WHERE c.id = conversation_id AND c.status = 'active')`,
		s.now().UTC().Format(time.RFC3339Nano), conversationID, userID)
	if err != nil {
		return fmt.Errorf("leave conversation: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return platform.Problem(http.StatusNotFound, "conversation_not_found", "会话不存在")
	}
	var activeMembers int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversation_members WHERE conversation_id = ? AND left_at IS NULL`, conversationID).Scan(&activeMembers); err != nil {
		return fmt.Errorf("count remaining conversation members: %w", err)
	}
	if activeMembers == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE conversations SET status = 'closed', updated_at = ? WHERE id = ?`, s.now().UTC().Format(time.RFC3339Nano), conversationID); err != nil {
			return fmt.Errorf("close empty conversation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit leave conversation: %w", err)
	}
	return nil
}

func (s *Service) createConversationHTTP(w http.ResponseWriter, r *http.Request) {
	var input CreateConversationInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	item, replayed, err := s.CreateConversation(r.Context(), current.ID, input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	platform.WriteData(w, r, status, item)
}

func (s *Service) conversationsHTTP(w http.ResponseWriter, r *http.Request) {
	limit, err := validateLimit(r.URL.Query().Get("limit"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	items, next, err := s.Conversations(r.Context(), current.ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) conversationHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	item, err := s.Conversation(r.Context(), chi.URLParam(r, "conversationID"), current.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) messagesHTTP(w http.ResponseWriter, r *http.Request) {
	limit, err := validateLimit(r.URL.Query().Get("limit"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	items, next, err := s.Messages(r.Context(), chi.URLParam(r, "conversationID"), current.ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}

func (s *Service) sendMessageHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ClientMessageID string `json:"client_message_id"`
		Body            string `json:"body"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	if err := platform.CheckRateLimit(s.messageLimiter, current.ID); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, replayed, err := s.SendMessage(r.Context(), chi.URLParam(r, "conversationID"), current.ID, input.ClientMessageID, input.Body)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	platform.WriteData(w, r, status, item)
}

func (s *Service) markConversationReadHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MessageID string `json:"message_id"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	item, err := s.MarkConversationRead(r.Context(), chi.URLParam(r, "conversationID"), current.ID, input.MessageID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) leaveConversationHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	if err := s.LeaveConversation(r.Context(), chi.URLParam(r, "conversationID"), current.ID); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"left": true})
}

func (s *Service) eventsHTTP(w http.ResponseWriter, r *http.Request) {
	limit, err := validateLimit(r.URL.Query().Get("limit"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := identity.UserFromContext(r.Context())
	items, next, hasMore, err := s.Events(r.Context(), current.ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{
		"data":      items,
		"page":      map[string]any{"next_cursor": next, "has_more": hasMore},
		"reconnect": map[string]any{"transport": "poll", "retry_after_ms": 1500},
		"meta":      map[string]string{"request_id": platform.RequestID(r.Context())},
	})
}
