// Community routes present one resource-style API for feeds and interactions.
package community

import (
	"net/http"
	"strconv"
	"strings"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func (s *Service) Routes(auth *identity.Service) chi.Router {
	router := chi.NewRouter()
	router.Get("/posts", s.listPosts)
	router.Get("/feeds/{feed}", s.listFeed)
	router.Get("/categories", s.categoriesHTTP)
	router.Get("/tags", s.tagsHTTP)
	router.Get("/search", s.searchHTTP)
	router.Get("/homepage", s.homepageHTTP)
	router.Get("/users/{userID}", s.publicProfileHTTP)
	router.Get("/users/{userID}/followers", s.socialUsersHTTP("followers"))
	router.Get("/users/{userID}/following", s.socialUsersHTTP("following"))
	router.Get("/media/{mediaID}", s.mediaHTTP)
	router.Get("/posts/{postID}", s.getPost)
	router.Get("/posts/{postID}/comments", s.listComments)

	router.Group(func(protected chi.Router) {
		protected.Use(auth.RequireAuth)
		protected.Post("/posts", s.createPost)
		protected.Patch("/posts/{postID}", s.updatePostHTTP)
		protected.Delete("/posts/{postID}", s.deletePostHTTP)
		protected.Post("/posts/{postID}/comments", s.addComment)
		protected.Post("/posts/{postID}/comments/{commentID}/replies", s.reply)
		protected.Patch("/posts/{postID}/comments/{commentID}", s.updateCommentHTTP)
		protected.Delete("/posts/{postID}/comments/{commentID}", s.deleteCommentHTTP)
		protected.Put("/posts/{postID}/comments/{commentID}/reactions/like", s.likeCommentHTTP)
		protected.Delete("/posts/{postID}/comments/{commentID}/reactions/like", s.unlikeCommentHTTP)
		protected.Put("/posts/{postID}/reactions/like", s.like)
		protected.Delete("/posts/{postID}/reactions/like", s.unlike)
		protected.Put("/posts/{postID}/reposts", s.repost)
		protected.Delete("/posts/{postID}/reposts", s.undoRepost)
		protected.Put("/posts/{postID}/repost", s.repost)
		protected.Delete("/posts/{postID}/repost", s.undoRepost)
		protected.Put("/posts/{postID}/bookmark", s.bookmarkHTTP)
		protected.Delete("/posts/{postID}/bookmark", s.unbookmarkHTTP)
		protected.Get("/me/bookmarks", s.bookmarksHTTP)
		protected.Put("/users/{userID}/follow", s.followHTTP)
		protected.Delete("/users/{userID}/follow", s.unfollowHTTP)
		protected.Put("/users/{userID}/block", s.blockHTTP)
		protected.Delete("/users/{userID}/block", s.unblockHTTP)
		protected.Get("/me/blocks", s.blocksHTTP)
		protected.Put("/categories/{categoryID}/follow", s.followCategoryHTTP)
		protected.Delete("/categories/{categoryID}/follow", s.unfollowCategoryHTTP)
		protected.Get("/me/category-follows", s.followedCategoriesHTTP)
		protected.Get("/notifications", s.notificationsHTTP)
		protected.Put("/notifications/read", s.markNotificationsReadHTTP)
		protected.Post("/reports", s.submitReportHTTP)
		protected.Post("/uploads", s.uploadHTTP)
		protected.Put("/me/avatar", s.avatarHTTP)
		protected.Put("/me/cover", s.coverHTTP)
		protected.Post("/conversations", s.createConversationHTTP)
		protected.Get("/conversations", s.conversationsHTTP)
		protected.Get("/conversations/{conversationID}", s.conversationHTTP)
		protected.Get("/conversations/{conversationID}/messages", s.messagesHTTP)
		protected.Post("/conversations/{conversationID}/messages", s.sendMessageHTTP)
		protected.Put("/conversations/{conversationID}/read", s.markConversationReadHTTP)
		protected.Delete("/conversations/{conversationID}/membership", s.leaveConversationHTTP)
		protected.Get("/events", s.eventsHTTP)
		protected.With(auth.RequireRole("moderator", "admin")).Get("/admin/reports", s.reportsHTTP)
		protected.With(auth.RequireRole("moderator", "admin")).Post("/admin/reports/{reportID}/decision", s.decideReportHTTP)
		protected.With(auth.RequireRole("moderator", "admin")).Get("/admin/content", s.adminContentHTTP)
		protected.With(auth.RequireRole("admin")).Get("/admin/users", s.adminUsersHTTP)
		protected.With(auth.RequireRole("admin")).Patch("/admin/users/{userID}/status", s.updateUserStatusHTTP)
		protected.With(auth.RequireRole("admin")).Post("/admin/categories", s.createTaxonomyHTTP)
		protected.With(auth.RequireRole("admin")).Patch("/admin/categories/{categoryID}", s.updateTaxonomyHTTP)
		protected.With(auth.RequireRole("admin")).Delete("/admin/categories/{categoryID}", s.deleteTaxonomyHTTP)
		protected.With(auth.RequireRole("admin")).Post("/admin/tags", s.createTaxonomyHTTP)
		protected.With(auth.RequireRole("admin")).Patch("/admin/tags/{tagID}", s.updateTaxonomyHTTP)
		protected.With(auth.RequireRole("admin")).Delete("/admin/tags/{tagID}", s.deleteTaxonomyHTTP)
		protected.With(auth.RequireRole("admin")).Get("/admin/homepage", s.adminHomepageHTTP)
		protected.With(auth.RequireRole("admin")).Patch("/admin/homepage", s.updateHomepageHTTP)
		protected.With(auth.RequireRole("admin")).Get("/admin/audit-events", s.auditEventsHTTP)
	})
	return router
}

func postID(r *http.Request) string    { return chi.URLParam(r, "postID") }
func userID(r *http.Request) string    { return chi.URLParam(r, "userID") }
func mediaID(r *http.Request) string   { return chi.URLParam(r, "mediaID") }
func commentID(r *http.Request) string { return chi.URLParam(r, "commentID") }

func (s *Service) listPosts(w http.ResponseWriter, r *http.Request) {
	s.writeFeed(w, r, r.URL.Query().Get("feed"))
}

func (s *Service) listFeed(w http.ResponseWriter, r *http.Request) {
	s.writeFeed(w, r, chi.URLParam(r, "feed"))
}

func (s *Service) writeFeed(w http.ResponseWriter, r *http.Request, feed string) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	user, _ := identity.UserFromContext(r.Context())
	posts, next, serviceErr := s.ListPostsFiltered(r.Context(), feed, user.ID, r.URL.Query().Get("category_id"), r.URL.Query().Get("tag_id"), offset, limit)
	if serviceErr != nil {
		platform.WriteError(w, r, serviceErr)
		return
	}
	platform.WriteList(w, r, posts, next)
}

func (s *Service) getPost(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.UserFromContext(r.Context())
	post, err := s.Post(r.Context(), chi.URLParam(r, "postID"), user.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(post.Version)))
	platform.WriteData(w, r, http.StatusOK, post)
}

func (s *Service) createPost(w http.ResponseWriter, r *http.Request) {
	var input CreatePostInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	user, _ := identity.UserFromContext(r.Context())
	post, replayed, err := s.CreatePost(r.Context(), user.ID, strings.TrimSpace(r.Header.Get("Idempotency-Key")), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	platform.WriteData(w, r, status, post)
}

func (s *Service) listComments(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	user, _ := identity.UserFromContext(r.Context())
	comments, next, serviceErr := s.ListComments(r.Context(), chi.URLParam(r, "postID"), user.ID, offset, limit)
	if serviceErr != nil {
		platform.WriteError(w, r, serviceErr)
		return
	}
	platform.WriteList(w, r, comments, next)
}

func (s *Service) addComment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Body     string `json:"body"`
		Content  string `json:"content"`
		ParentID string `json:"parent_id"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	user, _ := identity.UserFromContext(r.Context())
	if input.Body == "" {
		input.Body = input.Content
	}
	comment, replayed, err := s.AddComment(r.Context(), chi.URLParam(r, "postID"), user.ID, strings.TrimSpace(input.ParentID), strings.TrimSpace(r.Header.Get("Idempotency-Key")), input.Body)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	platform.WriteData(w, r, status, comment)
}

func (s *Service) reply(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Body    string `json:"body"`
		Content string `json:"content"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	user, _ := identity.UserFromContext(r.Context())
	if input.Body == "" {
		input.Body = input.Content
	}
	comment, replayed, err := s.AddComment(r.Context(), chi.URLParam(r, "postID"), user.ID, chi.URLParam(r, "commentID"), strings.TrimSpace(r.Header.Get("Idempotency-Key")), input.Body)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	platform.WriteData(w, r, status, comment)
}

func (s *Service) like(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.UserFromContext(r.Context())
	changed, count, err := s.Like(r.Context(), chi.URLParam(r, "postID"), user.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"liked": true, "changed": changed, "like_count": count})
}

func (s *Service) unlike(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.UserFromContext(r.Context())
	changed, count, err := s.Unlike(r.Context(), chi.URLParam(r, "postID"), user.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"liked": false, "changed": changed, "like_count": count})
}

func (s *Service) repost(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.UserFromContext(r.Context())
	post, replayed, err := s.Repost(r.Context(), chi.URLParam(r, "postID"), user.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	platform.WriteData(w, r, http.StatusOK, post)
}

func (s *Service) undoRepost(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.UserFromContext(r.Context())
	changed, err := s.UndoRepost(r.Context(), chi.URLParam(r, "postID"), user.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"reposted": false, "changed": changed})
}

func pagination(r *http.Request) (int, int, error) {
	offset, err := platform.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return 0, 0, platform.Validation(map[string][]string{"cursor": {"游标无效"}})
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			return 0, 0, platform.Validation(map[string][]string{"limit": {"limit 必须是 1 到 50 的整数"}})
		}
	}
	return offset, limit, nil
}

func ifMatchVersion(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, platform.Problem(http.StatusPreconditionRequired, "if_match_required", "请提供 If-Match 版本")
	}
	raw = strings.TrimPrefix(raw, "W/")
	raw = strings.Trim(raw, `"`)
	version, err := strconv.Atoi(raw)
	if err != nil || version < 1 {
		return 0, platform.Validation(map[string][]string{"if_match": {"If-Match 必须是有效的正整数版本"}})
	}
	return version, nil
}
