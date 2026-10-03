// Community response models expose only fields needed by the new API contract.
package community

type Author struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

type Category struct {
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ImageURL      string `json:"image_url"`
	BackgroundURL string `json:"background_url"`
	PostCount     int    `json:"post_count,omitempty"`
}

type Tag struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	PostCount int    `json:"post_count,omitempty"`
}

type MediaAsset struct {
	ID           string `json:"id"`
	Purpose      string `json:"purpose"`
	MIMEType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	Checksum     string `json:"checksum,omitempty"`
	OriginalName string `json:"original_name,omitempty"`
	AltText      string `json:"alt_text"`
	URL          string `json:"url"`
	CreatedAt    string `json:"created_at,omitempty"`
}

type Post struct {
	ID           string       `json:"id"`
	Kind         string       `json:"kind"`
	RepostOf     string       `json:"repost_of,omitempty"`
	Title        string       `json:"title"`
	Summary      string       `json:"summary"`
	Body         string       `json:"body,omitempty"`
	Content      string       `json:"content"`
	Status       string       `json:"status"`
	Visibility   string       `json:"visibility"`
	Version      int          `json:"version"`
	Pinned       bool         `json:"pinned"`
	Recommended  bool         `json:"recommended"`
	LikeCount    int          `json:"like_count"`
	CommentCount int          `json:"comment_count"`
	RepostCount  int          `json:"repost_count"`
	Likes        int          `json:"likes"`
	Comments     int          `json:"comments"`
	Reposts      int          `json:"reposts"`
	Views        int          `json:"views"`
	Liked        bool         `json:"liked"`
	Reposted     bool         `json:"reposted"`
	Bookmarked   bool         `json:"bookmarked"`
	Age          string       `json:"age"`
	Avatar       string       `json:"avatar"`
	CreatedAt    string       `json:"created_at"`
	PublishedAt  string       `json:"published_at"`
	Author       Author       `json:"author"`
	Category     *Category    `json:"category,omitempty"`
	Tags         []Tag        `json:"tags"`
	Media        []MediaAsset `json:"media"`
}

type Comment struct {
	ID         string `json:"id"`
	PostID     string `json:"post_id"`
	ParentID   string `json:"parent_id,omitempty"`
	RootID     string `json:"root_id,omitempty"`
	ThreadID   string `json:"thread_id"`
	Depth      int    `json:"depth"`
	Body       string `json:"body"`
	Content    string `json:"content"`
	LikeCount  int    `json:"like_count"`
	Liked      bool   `json:"liked"`
	Deleted    bool   `json:"deleted"`
	ReplyCount int    `json:"reply_count,omitempty"`
	Version    int    `json:"version"`
	CreatedAt  string `json:"created_at"`
	Age        string `json:"age"`
	Author     Author `json:"author"`
}

type PublicUser struct {
	ID            string `json:"id"`
	Handle        string `json:"handle"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	AvatarURL     string `json:"avatar_url"`
	Bio           string `json:"bio"`
	FollowerCount int    `json:"follower_count"`
	Following     bool   `json:"following"`
}

type SearchResults struct {
	Posts []Post       `json:"posts"`
	Users []PublicUser `json:"users"`
	Tags  []Tag        `json:"tags"`
}
