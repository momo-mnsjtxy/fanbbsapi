// Package legacyimport imports only declared synthetic JSON fixtures. It has no
// network or MySQL capability, making mapping and quarantine behavior safe to rehearse.
package legacyimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Document struct {
	Source         string           `json:"source"`
	MappingVersion int              `json:"mapping_version"`
	Users          []LegacyUser     `json:"users"`
	Taxonomy       []LegacyTaxonomy `json:"taxonomy"`
	Posts          []LegacyPost     `json:"posts"`
	Comments       []LegacyComment  `json:"comments"`
}

type LegacyUser struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type LegacyTaxonomy struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type LegacyPost struct {
	ID         string `json:"id"`
	AuthorID   string `json:"author_id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	CategoryID string `json:"category_id"`
	Status     string `json:"status"`
	Paid       bool   `json:"paid"`
	CreatedAt  string `json:"created_at"`
}

type LegacyComment struct {
	ID        string `json:"id"`
	PostID    string `json:"post_id"`
	AuthorID  string `json:"author_id"`
	ParentID  string `json:"parent_id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type Counts struct {
	Read        int `json:"read"`
	Imported    int `json:"imported"`
	Quarantined int `json:"quarantined"`
}

type Report struct {
	RunID      string         `json:"run_id"`
	SourceHash string         `json:"source_hash"`
	Replayed   bool           `json:"replayed"`
	Users      Counts         `json:"users"`
	Taxonomy   Counts         `json:"taxonomy"`
	Posts      Counts         `json:"posts"`
	Comments   Counts         `json:"comments"`
	Reasons    map[string]int `json:"quarantine_reasons"`
}

type Importer struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB) *Importer { return &Importer{db: db, now: time.Now} }

var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,24}$`)

func (importer *Importer) Import(ctx context.Context, document Document) (Report, error) {
	if document.Source != "synthetic" || document.MappingVersion != 1 {
		return Report{}, fmt.Errorf("refusing import: source must be synthetic and mapping_version must be 1")
	}
	canonical := canonicalDocument(document)
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return Report{}, fmt.Errorf("encode synthetic source: %w", err)
	}
	sum := sha256.Sum256(encoded)
	sourceHash := hex.EncodeToString(sum[:])
	runID := "legacy_run_" + sourceHash[:20]
	var previous string
	err = importer.db.QueryRowContext(ctx, `SELECT report_json FROM legacy_import_runs WHERE source_hash = ?`, sourceHash).Scan(&previous)
	if err == nil {
		var report Report
		if err := json.Unmarshal([]byte(previous), &report); err != nil {
			return Report{}, fmt.Errorf("decode prior import report: %w", err)
		}
		report.Replayed = true
		return report, nil
	}
	if err != sql.ErrNoRows {
		return Report{}, fmt.Errorf("check prior synthetic import: %w", err)
	}

	report := Report{
		RunID: runID, SourceHash: sourceHash, Reasons: map[string]int{},
		Users: Counts{Read: len(canonical.Users)}, Taxonomy: Counts{Read: len(canonical.Taxonomy)},
		Posts: Counts{Read: len(canonical.Posts)}, Comments: Counts{Read: len(canonical.Comments)},
	}
	tx, err := importer.db.BeginTx(ctx, nil)
	if err != nil {
		return Report{}, fmt.Errorf("begin synthetic import: %w", err)
	}
	defer tx.Rollback()
	now := importer.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_import_runs(id, source_hash, report_json, created_at) VALUES (?, ?, '{}', ?)`, runID, sourceHash, now); err != nil {
		return Report{}, fmt.Errorf("create synthetic import run: %w", err)
	}

	userIDs := map[string]string{}
	handles := map[string]bool{}
	emails := map[string]bool{}
	for _, user := range canonical.Users {
		reason := ""
		role := mapRole(user.Role)
		status := mapStatus(user.Status)
		if user.ID == "" || !handlePattern.MatchString(user.Handle) || !strings.Contains(user.Email, "@") {
			reason = "invalid_user"
		} else if handles[strings.ToLower(user.Handle)] || emails[strings.ToLower(user.Email)] {
			reason = "duplicate_identity"
		} else if role == "" || status == "" {
			reason = "unsupported_user_state"
		}
		if reason != "" {
			if err := importer.quarantine(ctx, tx, report.RunID, "user", user.ID, reason, user, &report, now); err != nil {
				return Report{}, err
			}
			report.Users.Quarantined++
			continue
		}
		newID := deterministicID("usr", user.ID)
		created := normalizedTime(user.CreatedAt)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO users(id, handle, email, password_hash, display_name, role, status, created_at, updated_at)
			VALUES (?, ?, ?, 'legacy-login-disabled', ?, ?, ?, ?, ?)`, newID, user.Handle, user.Email, fallback(user.DisplayName, user.Handle), role, status, created, created)
		if err != nil {
			if err := importer.quarantine(ctx, tx, report.RunID, "user", user.ID, "target_conflict", user, &report, now); err != nil {
				return Report{}, err
			}
			report.Users.Quarantined++
			continue
		}
		userIDs[user.ID] = newID
		handles[strings.ToLower(user.Handle)] = true
		emails[strings.ToLower(user.Email)] = true
		report.Users.Imported++
	}

	categoryIDs := map[string]string{}
	tagIDs := map[string]string{}
	slugs := map[string]bool{}
	for _, taxonomy := range canonical.Taxonomy {
		if taxonomy.ID == "" || taxonomy.Name == "" || taxonomy.Slug == "" || (taxonomy.Kind != "category" && taxonomy.Kind != "tag") {
			if err := importer.quarantine(ctx, tx, runID, "taxonomy", taxonomy.ID, "invalid_taxonomy", taxonomy, &report, now); err != nil {
				return Report{}, err
			}
			report.Taxonomy.Quarantined++
			continue
		}
		key := taxonomy.Kind + ":" + strings.ToLower(taxonomy.Slug)
		if slugs[key] {
			if err := importer.quarantine(ctx, tx, runID, "taxonomy", taxonomy.ID, "duplicate_slug", taxonomy, &report, now); err != nil {
				return Report{}, err
			}
			report.Taxonomy.Quarantined++
			continue
		}
		prefix := "cat"
		table := "categories"
		if taxonomy.Kind == "tag" {
			prefix, table = "tag", "tags"
		}
		newID := deterministicID(prefix, taxonomy.ID)
		query := `INSERT INTO ` + table + `(id, slug, name`
		values := ` VALUES (?, ?, ?`
		args := []any{newID, taxonomy.Slug, taxonomy.Name}
		if table == "tags" {
			query += `, created_at`
			values += `, ?`
			args = append(args, now)
		}
		_, err := tx.ExecContext(ctx, query+`)`+values+`)`, args...)
		if err != nil {
			if err := importer.quarantine(ctx, tx, runID, "taxonomy", taxonomy.ID, "target_conflict", taxonomy, &report, now); err != nil {
				return Report{}, err
			}
			report.Taxonomy.Quarantined++
			continue
		}
		if taxonomy.Kind == "category" {
			categoryIDs[taxonomy.ID] = newID
		} else {
			tagIDs[taxonomy.ID] = newID
		}
		slugs[key] = true
		report.Taxonomy.Imported++
	}
	_ = tagIDs // Tag relationships are intentionally absent from the minimal synthetic v1 shape.

	postIDs := map[string]string{}
	for _, post := range canonical.Posts {
		reason := ""
		authorID := userIDs[post.AuthorID]
		if authorID == "" {
			reason = "orphan_post_author"
		} else if post.Paid {
			reason = "paid_content_disabled"
		} else if post.Status != "publish" {
			reason = "unsupported_post_status"
		} else if post.Kind != "article" && post.Kind != "image" && post.Kind != "video" {
			reason = "unsupported_post_kind"
		} else if strings.TrimSpace(post.Title) == "" || strings.TrimSpace(post.Body) == "" {
			reason = "invalid_post"
		}
		categoryID := ""
		if post.CategoryID != "" {
			categoryID = categoryIDs[post.CategoryID]
			if categoryID == "" && reason == "" {
				reason = "orphan_post_category"
			}
		}
		if reason != "" {
			if err := importer.quarantine(ctx, tx, runID, "post", post.ID, reason, post, &report, now); err != nil {
				return Report{}, err
			}
			report.Posts.Quarantined++
			continue
		}
		newID := deterministicID("post", post.ID)
		var category any
		if categoryID != "" {
			category = categoryID
		}
		created := normalizedTime(post.CreatedAt)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO posts(id, author_id, kind, title, body, category_id, status, visibility, created_at, published_at)
			VALUES (?, ?, ?, ?, ?, ?, 'published', 'public', ?, ?)`, newID, authorID, post.Kind, post.Title, post.Body, category, created, created)
		if err != nil {
			return Report{}, fmt.Errorf("insert synthetic post %s: %w", post.ID, err)
		}
		postIDs[post.ID] = newID
		report.Posts.Imported++
	}

	commentIDs := map[string]string{}
	commentDepth := map[string]int{}
	commentRoot := map[string]string{}
	pending := append([]LegacyComment(nil), canonical.Comments...)
	for len(pending) > 0 {
		progress := false
		next := make([]LegacyComment, 0, len(pending))
		for _, comment := range pending {
			postID, authorID := postIDs[comment.PostID], userIDs[comment.AuthorID]
			if postID == "" || authorID == "" || strings.TrimSpace(comment.Body) == "" {
				reason := "invalid_comment"
				if postID == "" {
					reason = "orphan_comment_post"
				} else if authorID == "" {
					reason = "orphan_comment_author"
				}
				if err := importer.quarantine(ctx, tx, runID, "comment", comment.ID, reason, comment, &report, now); err != nil {
					return Report{}, err
				}
				report.Comments.Quarantined++
				progress = true
				continue
			}
			parentID := ""
			rootID := ""
			depth := 0
			if comment.ParentID != "" {
				parentID = commentIDs[comment.ParentID]
				if parentID == "" {
					next = append(next, comment)
					continue
				}
				depth = commentDepth[comment.ParentID] + 1
				if depth > 2 {
					if err := importer.quarantine(ctx, tx, runID, "comment", comment.ID, "comment_too_deep", comment, &report, now); err != nil {
						return Report{}, err
					}
					report.Comments.Quarantined++
					progress = true
					continue
				}
				rootID = commentRoot[comment.ParentID]
				if rootID == "" {
					rootID = parentID
				}
			}
			newID := deterministicID("cmt", comment.ID)
			var parent, root any
			if parentID != "" {
				parent = parentID
			}
			if rootID != "" {
				root = rootID
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO comments(id, post_id, author_id, parent_id, root_id, depth, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				newID, postID, authorID, parent, root, depth, comment.Body, normalizedTime(comment.CreatedAt))
			if err != nil {
				return Report{}, fmt.Errorf("insert synthetic comment %s: %w", comment.ID, err)
			}
			commentIDs[comment.ID] = newID
			commentDepth[comment.ID] = depth
			commentRoot[comment.ID] = rootID
			report.Comments.Imported++
			progress = true
		}
		if !progress {
			for _, comment := range next {
				if err := importer.quarantine(ctx, tx, runID, "comment", comment.ID, "orphan_or_cyclic_parent", comment, &report, now); err != nil {
					return Report{}, err
				}
				report.Comments.Quarantined++
			}
			break
		}
		pending = next
	}
	for _, postID := range postIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE posts SET comment_count = (SELECT COUNT(*) FROM comments WHERE post_id = ? AND status = 'published') WHERE id = ?`, postID, postID); err != nil {
			return Report{}, fmt.Errorf("recount synthetic post comments: %w", err)
		}
	}
	stored := report
	stored.Replayed = false
	reportJSON, err := json.Marshal(stored)
	if err != nil {
		return Report{}, fmt.Errorf("encode import report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE legacy_import_runs SET report_json = ? WHERE id = ?`, string(reportJSON), runID); err != nil {
		return Report{}, fmt.Errorf("save import report: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Report{}, fmt.Errorf("commit synthetic import: %w", err)
	}
	return report, nil
}

func (importer *Importer) quarantine(ctx context.Context, tx *sql.Tx, runID, entityType, legacyID, reason string, source any, report *Report, now string) error {
	encoded, err := json.Marshal(source)
	if err != nil {
		return fmt.Errorf("encode quarantine source: %w", err)
	}
	if legacyID == "" {
		sum := sha256.Sum256(encoded)
		legacyID = "missing_" + hex.EncodeToString(sum[:6])
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO legacy_quarantine(run_id, entity_type, legacy_id, reason, source_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`, runID, entityType, legacyID, reason, string(encoded), now)
	if err != nil {
		return fmt.Errorf("quarantine %s %s: %w", entityType, legacyID, err)
	}
	report.Reasons[reason]++
	return nil
}

func canonicalDocument(document Document) Document {
	sort.Slice(document.Users, func(i, j int) bool { return document.Users[i].ID < document.Users[j].ID })
	sort.Slice(document.Taxonomy, func(i, j int) bool { return document.Taxonomy[i].ID < document.Taxonomy[j].ID })
	sort.Slice(document.Posts, func(i, j int) bool { return document.Posts[i].ID < document.Posts[j].ID })
	sort.Slice(document.Comments, func(i, j int) bool { return document.Comments[i].ID < document.Comments[j].ID })
	return document
}

func deterministicID(prefix, legacyID string) string {
	sum := sha256.Sum256([]byte(prefix + ":" + legacyID))
	return "legacy_" + prefix + "_" + hex.EncodeToString(sum[:10])
}

func mapRole(role string) string {
	switch role {
	case "", "contributor", "member":
		return "member"
	case "editor", "moderator":
		return "moderator"
	case "administrator", "admin":
		return "admin"
	default:
		return ""
	}
}

func mapStatus(status string) string {
	switch status {
	case "", "active", "1":
		return "active"
	case "disabled", "0", "suspended":
		return "suspended"
	default:
		return ""
	}
}

func normalizedTime(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "2000-01-01T00:00:00Z"
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func fallback(value, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return value
}
