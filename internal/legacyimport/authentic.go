package legacyimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AuthenticSchema identifies the row layout created by the original Java
// InstallController. AuthenticSnapshot deliberately has no database or network
// reader: callers must provide a declared synthetic, offline JSON fixture.
const AuthenticSchema = "fanbbs-java-installcontroller-v1"

type AuthenticSnapshot struct {
	Source         string                  `json:"source"`
	Schema         string                  `json:"schema"`
	MappingVersion int                     `json:"mapping_version"`
	Users          []AuthenticUser         `json:"users"`
	Contents       []AuthenticContent      `json:"contents"`
	Comments       []AuthenticComment      `json:"comments"`
	Metas          []AuthenticMeta         `json:"metas"`
	Relationships  []AuthenticRelationship `json:"relationships"`
	Fans           []AuthenticFan          `json:"fan"`
}

// These fields intentionally retain the original MySQL column spelling. The
// password column is accepted only so a schema-shaped fixture can be decoded;
// AdaptAuthenticSnapshot never copies it into the normalized import document.
type AuthenticUser struct {
	UID        int64  `json:"uid"`
	Name       string `json:"name"`
	ScreenName string `json:"screenName"`
	Password   string `json:"password"`
	Mail       string `json:"mail"`
	Introduce  string `json:"introduce"`
	Avatar     string `json:"avatar"`
	Status     int    `json:"status"`
	Group      string `json:"group"`
	Created    int64  `json:"created"`
}

type AuthenticContent struct {
	CID      int64  `json:"cid"`
	MID      int64  `json:"mid"`
	Title    string `json:"title"`
	Text     string `json:"text"`
	AuthorID int64  `json:"authorId"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Price    int64  `json:"price"`
	Images   string `json:"images"`
	Videos   string `json:"videos"`
	Created  int64  `json:"created"`
}

type AuthenticComment struct {
	ID      int64  `json:"id"`
	CID     int64  `json:"cid"`
	UID     int64  `json:"uid"`
	Text    string `json:"text"`
	Images  string `json:"images"`
	Parent  int64  `json:"parent"`
	All     int64  `json:"all"`
	Type    int    `json:"type"`
	Created int64  `json:"created"`
}

type AuthenticMeta struct {
	MID    int64  `json:"mid"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Type   string `json:"type"`
	Avatar string `json:"avatar"`
	ImgURL string `json:"imgurl"`
	Parent int64  `json:"parent"`
	IsVIP  int    `json:"isvip"`
}

type AuthenticRelationship struct {
	CID int64 `json:"cid"`
	MID int64 `json:"mid"`
}

type AuthenticFan struct {
	ID      int64 `json:"id"`
	UID     int64 `json:"uid"`
	ToUID   int64 `json:"touid"`
	Created int64 `json:"created"`
}

// AdaptAuthenticSnapshot maps the original Java/MySQL row shape into the
// importer model. It is intentionally pure and only accepts fixtures explicitly
// marked synthetic, so schema work can be tested without opening legacy MySQL,
// fetching media, or handling real credentials.
func AdaptAuthenticSnapshot(snapshot AuthenticSnapshot) (Document, error) {
	if snapshot.Source != "synthetic" || snapshot.Schema != AuthenticSchema || snapshot.MappingVersion != 1 {
		return Document{}, fmt.Errorf("refusing authentic-shape mapping: source must be synthetic, schema must be %q, and mapping_version must be 1", AuthenticSchema)
	}

	document := Document{Source: "synthetic", MappingVersion: 1}
	metaKinds := make(map[int64]string, len(snapshot.Metas))
	metaIDs := make(map[int64]string, len(snapshot.Metas))
	for _, meta := range snapshot.Metas {
		id := strconv.FormatInt(meta.MID, 10)
		kind := strings.ToLower(strings.TrimSpace(meta.Type))
		metaKinds[meta.MID] = kind
		metaIDs[meta.MID] = id
		document.Taxonomy = append(document.Taxonomy, LegacyTaxonomy{
			ID: id, Kind: kind, Slug: authenticSlug(meta.Slug, meta.MID), Name: strings.TrimSpace(meta.Name),
		})
		for _, raw := range []struct {
			field string
			value string
		}{{"metas.avatar", meta.Avatar}, {"metas.imgurl", meta.ImgURL}} {
			document.Media = appendMediaReferences(document.Media, "taxonomy", id, "", raw.field, raw.value)
		}
	}

	relations := make(map[int64][]int64)
	for _, relationship := range snapshot.Relationships {
		relations[relationship.CID] = append(relations[relationship.CID], relationship.MID)
	}

	for _, user := range snapshot.Users {
		id := strconv.FormatInt(user.UID, 10)
		handle := authenticHandle(user.Name, user.UID)
		document.Users = append(document.Users, LegacyUser{
			ID: id, Handle: handle, Email: strings.TrimSpace(user.Mail),
			DisplayName: fallback(strings.TrimSpace(user.ScreenName), strings.TrimSpace(user.Name)),
			Bio:         strings.TrimSpace(user.Introduce), Role: strings.TrimSpace(user.Group),
			Status: strconv.Itoa(user.Status), CreatedAt: authenticTime(user.Created),
		})
		// The URL/path is inventory only. The importer quarantines it for a
		// separately approved blob-copy/checksum pass and never fetches it.
		document.Media = appendMediaReferences(document.Media, "user", id, id, "users.avatar", user.Avatar)
		_ = user.Password // Explicitly discarded; legacy PHPass hashes never become live credentials.
	}

	for _, content := range snapshot.Contents {
		id := strconv.FormatInt(content.CID, 10)
		post := LegacyPost{
			ID: id, AuthorID: strconv.FormatInt(content.AuthorID, 10), Kind: authenticPostKind(content.Type),
			Title: content.Title, Body: content.Text, Status: strings.TrimSpace(content.Status),
			Paid: content.Price > 0, CreatedAt: authenticTime(content.Created),
		}
		categoryCandidates := make([]string, 0, 1)
		tagIDs := make([]string, 0)
		mids := append([]int64(nil), relations[content.CID]...)
		if content.MID != 0 {
			mids = append(mids, content.MID)
		}
		sort.Slice(mids, func(i, j int) bool { return mids[i] < mids[j] })
		seen := map[int64]bool{}
		for _, mid := range mids {
			if seen[mid] {
				continue
			}
			seen[mid] = true
			switch metaKinds[mid] {
			case "category":
				categoryCandidates = append(categoryCandidates, metaIDs[mid])
			case "tag":
				tagIDs = append(tagIDs, metaIDs[mid])
			default:
				// Preserve a dangling relation as an unresolved tag reference so
				// Import quarantines the affected post instead of silently losing
				// legacy classification. The sentinel can never resolve to a real
				// taxonomy row because authentic meta IDs are decimal strings.
				tagIDs = append(tagIDs, "missing-meta-"+strconv.FormatInt(mid, 10))
			}
		}
		if len(categoryCandidates) > 0 {
			post.CategoryID = categoryCandidates[0]
		}
		post.TagIDs = tagIDs
		document.Posts = append(document.Posts, post)
		document.Media = appendMediaReferences(document.Media, "post", id, post.AuthorID, "contents.images", content.Images)
		document.Media = appendMediaReferences(document.Media, "post", id, post.AuthorID, "contents.videos", content.Videos)
	}

	for _, comment := range snapshot.Comments {
		id := strconv.FormatInt(comment.ID, 10)
		parentID := ""
		if comment.Parent != 0 {
			parentID = strconv.FormatInt(comment.Parent, 10)
		}
		document.Comments = append(document.Comments, LegacyComment{
			ID: id, PostID: strconv.FormatInt(comment.CID, 10), AuthorID: strconv.FormatInt(comment.UID, 10),
			ParentID: parentID, Body: comment.Text, CreatedAt: authenticTime(comment.Created),
		})
		document.Media = appendMediaReferences(document.Media, "comment", id, strconv.FormatInt(comment.UID, 10), "comments.images", comment.Images)
	}

	for _, fan := range snapshot.Fans {
		document.Follows = append(document.Follows, LegacyFollow{
			ID: strconv.FormatInt(fan.ID, 10), FollowerID: strconv.FormatInt(fan.UID, 10),
			FollowedID: strconv.FormatInt(fan.ToUID, 10), CreatedAt: authenticTime(fan.Created),
		})
	}

	document.Media = uniqueMediaReferences(document.Media)
	return document, nil
}

var authenticHandlePattern = regexp.MustCompile(`^[a-z0-9_]{3,24}$`)
var authenticSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

func authenticHandle(name string, uid int64) string {
	candidate := strings.ToLower(strings.TrimSpace(name))
	if authenticHandlePattern.MatchString(candidate) {
		return candidate
	}
	return "legacy_" + strconv.FormatInt(uid, 10)
}

func authenticSlug(slug string, mid int64) string {
	candidate := strings.ToLower(strings.TrimSpace(slug))
	if authenticSlugPattern.MatchString(candidate) {
		return candidate
	}
	return "legacy-meta-" + strconv.FormatInt(mid, 10)
}

func authenticPostKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "post", "article":
		return "article"
	case "photo", "image":
		return "image"
	case "video":
		return "video"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func authenticTime(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func appendMediaReferences(existing []LegacyMediaReference, entityType, entityID, ownerID, field, raw string) []LegacyMediaReference {
	for index, location := range mediaLocations(raw) {
		sum := sha256.Sum256([]byte(entityType + ":" + entityID + ":" + field + ":" + strconv.Itoa(index) + ":" + location))
		existing = append(existing, LegacyMediaReference{
			ID: "media_" + hex.EncodeToString(sum[:10]), EntityType: entityType, EntityID: entityID,
			OwnerID: ownerID, SourceField: field, Location: location,
		})
	}
	return existing
}

func mediaLocations(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var decoded any
	if json.Unmarshal([]byte(raw), &decoded) != nil {
		return []string{raw}
	}
	values := make([]string, 0)
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				values = append(values, trimmed)
			}
		case []any:
			for _, item := range value {
				visit(item)
			}
		case map[string]any:
			for _, key := range []string{"src", "url", "poster"} {
				if item, ok := value[key]; ok {
					visit(item)
				}
			}
		}
	}
	visit(decoded)
	return values
}

func uniqueMediaReferences(references []LegacyMediaReference) []LegacyMediaReference {
	seen := map[string]bool{}
	result := make([]LegacyMediaReference, 0, len(references))
	for _, reference := range references {
		key := reference.EntityType + "\x00" + reference.EntityID + "\x00" + reference.SourceField + "\x00" + reference.Location
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, reference)
	}
	return result
}
