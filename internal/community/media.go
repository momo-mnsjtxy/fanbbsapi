// Media metadata lives in SQLite while bytes flow through the blob interface.
// The local adapter is bounded and compensates filesystem writes if DB work fails.
package community

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fanbbs.local/backend/internal/blob"
	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
)

func (s *Service) Upload(ctx context.Context, ownerID, purpose, originalName, altText string, source io.Reader) (MediaAsset, error) {
	purpose = strings.TrimSpace(purpose)
	altText = strings.TrimSpace(altText)
	originalName = filepath.Base(strings.TrimSpace(originalName))
	if purpose != "post" && purpose != "avatar" {
		return MediaAsset{}, platform.Validation(map[string][]string{"purpose": {"purpose 必须是 post 或 avatar"}})
	}
	if originalName == "" || len([]rune(originalName)) > 255 {
		return MediaAsset{}, platform.Validation(map[string][]string{"file": {"文件名不能为空且不能超过 255 个字符"}})
	}
	if len([]rune(altText)) > 500 {
		return MediaAsset{}, platform.Validation(map[string][]string{"alt_text": {"替代文本不能超过 500 个字符"}})
	}
	assetID, err := platform.NewID("media")
	if err != nil {
		return MediaAsset{}, err
	}
	object, err := s.blobs.Put(ctx, assetID, source)
	if errors.Is(err, blob.ErrTooLarge) {
		return MediaAsset{}, platform.Problem(http.StatusRequestEntityTooLarge, "media_too_large", "媒体文件不能超过 10 MiB")
	}
	if errors.Is(err, blob.ErrUnsupportedType) {
		return MediaAsset{}, platform.Validation(map[string][]string{"file": {"仅支持 JPEG、PNG、GIF、WebP 和 MP4"}})
	}
	if err != nil {
		return MediaAsset{}, fmt.Errorf("store media: %w", err)
	}
	if purpose == "avatar" && !strings.HasPrefix(object.MIMEType, "image/") {
		_ = s.blobs.Delete(ctx, object.Key)
		return MediaAsset{}, platform.Validation(map[string][]string{"file": {"头像必须是图片"}})
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_ = s.blobs.Delete(ctx, object.Key)
		return MediaAsset{}, fmt.Errorf("begin media metadata: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO media_assets(id, owner_id, purpose, object_key, mime_type, size_bytes, checksum, original_name, alt_text, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, assetID, ownerID, purpose, object.Key, object.MIMEType, object.Size, object.Checksum, originalName, altText, now)
	if err == nil && purpose == "avatar" {
		_, err = tx.ExecContext(ctx, `UPDATE users SET avatar_media_id = ?, avatar_url = ?, updated_at = ? WHERE id = ? AND status = 'active'`, assetID, "/api/v1/media/"+assetID, now, ownerID)
	}
	if err != nil {
		_ = s.blobs.Delete(ctx, object.Key)
		return MediaAsset{}, fmt.Errorf("save media metadata: %w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = s.blobs.Delete(ctx, object.Key)
		return MediaAsset{}, fmt.Errorf("commit media metadata: %w", err)
	}
	return MediaAsset{ID: assetID, Purpose: purpose, MIMEType: object.MIMEType, SizeBytes: object.Size, Checksum: object.Checksum, OriginalName: originalName, AltText: altText, URL: "/api/v1/media/" + assetID, CreatedAt: now}, nil
}

func (s *Service) media(ctx context.Context, assetID, viewerID string) (MediaAsset, string, io.ReadCloser, bool, error) {
	var asset MediaAsset
	var ownerID, objectKey, status string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, owner_id, purpose, object_key, mime_type, size_bytes, checksum, original_name, alt_text, status, created_at
		FROM media_assets WHERE id = ?`, assetID).Scan(&asset.ID, &ownerID, &asset.Purpose, &objectKey, &asset.MIMEType,
		&asset.SizeBytes, &asset.Checksum, &asset.OriginalName, &asset.AltText, &status, &asset.CreatedAt)
	if err == sql.ErrNoRows || status != "ready" {
		return MediaAsset{}, "", nil, false, platform.Problem(http.StatusNotFound, "media_not_found", "媒体不存在")
	}
	if err != nil {
		return MediaAsset{}, "", nil, false, fmt.Errorf("load media: %w", err)
	}
	allowed := ownerID == viewerID && viewerID != ""
	var publicReference int
	publicErr := s.db.QueryRowContext(ctx, `
		SELECT 1 WHERE
			EXISTS(SELECT 1 FROM users WHERE avatar_media_id = ? AND status = 'active')
			OR EXISTS(SELECT 1 FROM user_covers cover JOIN users u ON u.id = cover.user_id WHERE cover.asset_id = ? AND u.status = 'active')
			OR EXISTS(SELECT 1 FROM operations_config_media link JOIN operations_configs config ON config.id = link.config_id WHERE link.asset_id = ? AND config.status = 'published')
			OR EXISTS(SELECT 1 FROM post_media pm JOIN posts p ON p.id = pm.post_id WHERE pm.asset_id = ? AND p.status = 'published' AND p.visibility = 'public')`,
		assetID, assetID, assetID, assetID).Scan(&publicReference)
	if publicErr != nil && publicErr != sql.ErrNoRows {
		return MediaAsset{}, "", nil, false, fmt.Errorf("check public media references: %w", publicErr)
	}
	publicCache := publicErr == nil
	allowed = allowed || publicCache
	if !allowed {
		var exists int
		allowed = s.db.QueryRowContext(ctx, `
			SELECT 1 FROM post_media pm JOIN posts p ON p.id = pm.post_id
			WHERE pm.asset_id = ? AND `+visiblePostPredicate, append([]any{assetID}, visiblePostArgs(viewerID)...)...).Scan(&exists) == nil
	}
	if !allowed {
		return MediaAsset{}, "", nil, false, platform.Problem(http.StatusNotFound, "media_not_found", "媒体不存在")
	}
	content, err := s.blobs.Open(ctx, objectKey)
	if err != nil {
		return MediaAsset{}, "", nil, false, fmt.Errorf("open media content: %w", err)
	}
	asset.URL = "/api/v1/media/" + asset.ID
	return asset, objectKey, content, publicCache, nil
}

func (s *Service) uploadHTTP(w http.ResponseWriter, r *http.Request) {
	s.uploadPurposeHTTP(w, r, "post")
}

func (s *Service) avatarHTTP(w http.ResponseWriter, r *http.Request) {
	s.uploadPurposeHTTP(w, r, "avatar")
}

func (s *Service) coverHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	if err := platform.CheckRateLimit(s.uploadLimiter, current.ID); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, blob.MaxSize+(1<<20))
	if err := r.ParseMultipartForm(blob.MaxSize + (1 << 20)); err != nil {
		platform.WriteError(w, r, platform.Problem(http.StatusBadRequest, "invalid_multipart", "上传请求格式不正确或文件过大"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		platform.WriteError(w, r, platform.Validation(map[string][]string{"file": {"请选择要上传的文件"}}))
		return
	}
	defer file.Close()
	asset, err := s.Upload(r.Context(), current.ID, "post", header.Filename, r.FormValue("alt_text"), file)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if !strings.HasPrefix(asset.MIMEType, "image/") {
		var objectKey string
		if err := s.db.QueryRowContext(r.Context(), `SELECT object_key FROM media_assets WHERE id = ? AND owner_id = ?`, asset.ID, current.ID).Scan(&objectKey); err == nil {
			_, _ = s.db.ExecContext(r.Context(), `UPDATE media_assets SET status = 'deleted' WHERE id = ? AND owner_id = ?`, asset.ID, current.ID)
			_ = s.blobs.Delete(r.Context(), objectKey)
		}
		platform.WriteError(w, r, platform.Validation(map[string][]string{"file": {"封面必须是图片"}}))
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO user_covers(user_id, asset_id, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET asset_id = excluded.asset_id, updated_at = excluded.updated_at`,
		current.ID, asset.ID, s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		platform.WriteError(w, r, fmt.Errorf("save profile cover: %w", err))
		return
	}
	platform.WriteData(w, r, http.StatusCreated, asset)
}

func (s *Service) uploadPurposeHTTP(w http.ResponseWriter, r *http.Request, purpose string) {
	current, _ := identity.UserFromContext(r.Context())
	if err := platform.CheckRateLimit(s.uploadLimiter, current.ID); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, blob.MaxSize+(1<<20))
	if err := r.ParseMultipartForm(blob.MaxSize + (1 << 20)); err != nil {
		platform.WriteError(w, r, platform.Problem(http.StatusBadRequest, "invalid_multipart", "上传请求格式不正确或文件过大"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		platform.WriteError(w, r, platform.Validation(map[string][]string{"file": {"请选择要上传的文件"}}))
		return
	}
	defer file.Close()
	asset, err := s.Upload(r.Context(), current.ID, purpose, header.Filename, r.FormValue("alt_text"), file)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusCreated, asset)
}

func (s *Service) mediaHTTP(w http.ResponseWriter, r *http.Request) {
	current, _ := identity.UserFromContext(r.Context())
	asset, _, content, publicCache, err := s.media(r.Context(), mediaID(r), current.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	defer content.Close()
	w.Header().Set("Content-Type", asset.MIMEType)
	w.Header().Set("Content-Length", strconv.FormatInt(asset.SizeBytes, 10))
	w.Header().Set("ETag", `"`+asset.Checksum+`"`)
	if publicCache {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, content)
}
