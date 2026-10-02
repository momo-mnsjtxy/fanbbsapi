package community

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

type CarouselItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Summary  string `json:"summary,omitempty"`
	LinkURL  string `json:"link_url,omitempty"`
	MediaID  string `json:"media_id,omitempty"`
	MediaURL string `json:"media_url,omitempty"`
	Position int    `json:"position"`
	Enabled  bool   `json:"enabled"`
}

type Announcement struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	LinkURL string `json:"link_url,omitempty"`
	Enabled bool   `json:"enabled"`
}

type HomepagePayload struct {
	Carousel      []CarouselItem `json:"carousel"`
	Announcements []Announcement `json:"announcements"`
}

type HomepageConfig struct {
	ID        string          `json:"id"`
	Version   int             `json:"version"`
	Status    string          `json:"status"`
	Payload   HomepagePayload `json:"payload"`
	UpdatedAt string          `json:"updated_at"`
}

type UpdateHomepageInput struct {
	Status        string         `json:"status"`
	Carousel      []CarouselItem `json:"carousel"`
	Announcements []Announcement `json:"announcements"`
}

func safeOperationsLink(raw string) bool {
	if raw == "" {
		return true
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return true
	}
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func (s *Service) homepageConfig(ctx context.Context, admin bool) (HomepageConfig, error) {
	var item HomepageConfig
	var payload string
	query := `SELECT id, version, status, payload, updated_at FROM operations_configs WHERE id = 'homepage'`
	if !admin {
		query += ` AND status = 'published'`
	}
	if err := s.db.QueryRowContext(ctx, query).Scan(&item.ID, &item.Version, &item.Status, &payload, &item.UpdatedAt); err == sql.ErrNoRows {
		return HomepageConfig{}, platform.Problem(http.StatusNotFound, "homepage_config_not_found", "首页配置不存在")
	} else if err != nil {
		return HomepageConfig{}, fmt.Errorf("load homepage config: %w", err)
	}
	if err := json.Unmarshal([]byte(payload), &item.Payload); err != nil {
		return HomepageConfig{}, fmt.Errorf("decode homepage config: %w", err)
	}
	if item.Payload.Carousel == nil {
		item.Payload.Carousel = []CarouselItem{}
	}
	if item.Payload.Announcements == nil {
		item.Payload.Announcements = []Announcement{}
	}
	return item, nil
}

func (s *Service) UpdateHomepage(ctx context.Context, actor identity.User, requestID string, expectedVersion int, input UpdateHomepageInput) (HomepageConfig, error) {
	input.Status = strings.TrimSpace(input.Status)
	fields := map[string][]string{}
	if input.Status != "draft" && input.Status != "published" {
		fields["status"] = []string{"status 必须是 draft 或 published"}
	}
	if len(input.Carousel) > 20 {
		fields["carousel"] = []string{"轮播项最多 20 条"}
	}
	if len(input.Announcements) > 20 {
		fields["announcements"] = []string{"公告最多 20 条"}
	}
	seenIDs := map[string]bool{}
	mediaIDs := map[string]bool{}
	for index := range input.Carousel {
		item := &input.Carousel[index]
		item.ID = strings.TrimSpace(item.ID)
		item.Title = strings.TrimSpace(item.Title)
		item.Summary = strings.TrimSpace(item.Summary)
		item.LinkURL = strings.TrimSpace(item.LinkURL)
		item.MediaID = strings.TrimSpace(item.MediaID)
		if item.ID == "" {
			generated, err := platform.NewID("slide")
			if err != nil {
				return HomepageConfig{}, err
			}
			item.ID = generated
		}
		if seenIDs[item.ID] {
			fields["carousel"] = []string{"轮播项 id 不能重复"}
		}
		seenIDs[item.ID] = true
		if len([]rune(item.Title)) < 1 || len([]rune(item.Title)) > 100 || len([]rune(item.Summary)) > 300 {
			fields["carousel"] = []string{"轮播标题长度为 1 到 100，摘要不超过 300 个字符"}
		}
		if len(item.LinkURL) > 500 || !safeOperationsLink(item.LinkURL) {
			fields["carousel"] = []string{"轮播链接必须是站内路径或 http/https URL"}
		}
		item.Position = index
		if item.MediaID != "" {
			mediaIDs[item.MediaID] = true
			item.MediaURL = "/api/v1/media/" + item.MediaID
		} else {
			item.MediaURL = ""
		}
	}
	seenIDs = map[string]bool{}
	for index := range input.Announcements {
		item := &input.Announcements[index]
		item.ID = strings.TrimSpace(item.ID)
		item.Text = strings.TrimSpace(item.Text)
		item.LinkURL = strings.TrimSpace(item.LinkURL)
		if item.ID == "" {
			generated, err := platform.NewID("notice")
			if err != nil {
				return HomepageConfig{}, err
			}
			item.ID = generated
		}
		if seenIDs[item.ID] {
			fields["announcements"] = []string{"公告 id 不能重复"}
		}
		seenIDs[item.ID] = true
		if len([]rune(item.Text)) < 1 || len([]rune(item.Text)) > 300 || len(item.LinkURL) > 500 || !safeOperationsLink(item.LinkURL) {
			fields["announcements"] = []string{"公告正文长度为 1 到 300，链接必须是站内路径或 http/https URL"}
		}
	}
	if len(fields) > 0 {
		return HomepageConfig{}, platform.Validation(fields)
	}
	payload := HomepagePayload{Carousel: input.Carousel, Announcements: input.Announcements}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return HomepageConfig{}, fmt.Errorf("encode homepage config: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HomepageConfig{}, fmt.Errorf("begin homepage update: %w", err)
	}
	defer tx.Rollback()
	var currentVersion int
	var currentStatus, currentPayload string
	if err := tx.QueryRowContext(ctx, `SELECT version, status, payload FROM operations_configs WHERE id = 'homepage'`).Scan(&currentVersion, &currentStatus, &currentPayload); err != nil {
		return HomepageConfig{}, fmt.Errorf("load homepage update: %w", err)
	}
	if currentVersion != expectedVersion {
		return HomepageConfig{}, platform.Problem(http.StatusConflict, "version_conflict", "首页配置已经被其他管理员更新")
	}
	ids := make([]string, 0, len(mediaIDs))
	for id := range mediaIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, mediaID := range ids {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM media_assets WHERE id = ? AND owner_id = ? AND status = 'ready' AND mime_type LIKE 'image/%'`, mediaID, actor.ID).Scan(&exists)
		if err == sql.ErrNoRows {
			return HomepageConfig{}, platform.Validation(map[string][]string{"carousel": {"轮播媒体不存在、不是图片或不属于当前管理员"}})
		}
		if err != nil {
			return HomepageConfig{}, fmt.Errorf("check homepage media: %w", err)
		}
	}
	now := s.now().UTC()
	result, err := tx.ExecContext(ctx, `
		UPDATE operations_configs SET version = version + 1, status = ?, payload = ?, updated_by = ?, updated_at = ?
		WHERE id = 'homepage' AND version = ?`, input.Status, string(encoded), actor.ID, now.Format(time.RFC3339Nano), expectedVersion)
	if err != nil {
		return HomepageConfig{}, fmt.Errorf("update homepage config: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return HomepageConfig{}, platform.Problem(http.StatusConflict, "version_conflict", "首页配置已经被其他管理员更新")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM operations_config_media WHERE config_id = 'homepage'`); err != nil {
		return HomepageConfig{}, fmt.Errorf("clear homepage media: %w", err)
	}
	for _, mediaID := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO operations_config_media(config_id, asset_id) VALUES ('homepage', ?)`, mediaID); err != nil {
			return HomepageConfig{}, fmt.Errorf("attach homepage media: %w", err)
		}
	}
	if err := insertAudit(ctx, tx, actor.ID, "homepage_config_update", "operations_config", "homepage",
		map[string]any{"version": currentVersion, "status": currentStatus, "payload": json.RawMessage(currentPayload)},
		map[string]any{"version": currentVersion + 1, "status": input.Status, "payload": json.RawMessage(encoded)},
		"更新首页运营配置", requestID, now); err != nil {
		return HomepageConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return HomepageConfig{}, fmt.Errorf("commit homepage config: %w", err)
	}
	return s.homepageConfig(ctx, true)
}

func (s *Service) homepageHTTP(w http.ResponseWriter, r *http.Request) {
	item, err := s.homepageConfig(r.Context(), false)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) adminHomepageHTTP(w http.ResponseWriter, r *http.Request) {
	item, err := s.homepageConfig(r.Context(), true)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	platform.WriteData(w, r, http.StatusOK, item)
}

func (s *Service) updateHomepageHTTP(w http.ResponseWriter, r *http.Request) {
	version, err := ifMatchVersion(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	var input UpdateHomepageInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	actor, _ := identity.UserFromContext(r.Context())
	item, err := s.UpdateHomepage(r.Context(), actor, platform.RequestID(r.Context()), version, input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	platform.WriteData(w, r, http.StatusOK, item)
}
