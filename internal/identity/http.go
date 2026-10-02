// Identity HTTP endpoints translate JSON into service calls; no SQL lives here.
package identity

import (
	"net/http"
	"strings"

	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

type currentUserKey struct{}
type currentSessionKey struct{}

func (s *Service) Routes() chi.Router {
	router := chi.NewRouter()
	router.Post("/register", s.register)
	router.Post("/login", s.login)
	router.Post("/refresh", s.refresh)
	return router
}

func (s *Service) RegisterAccountRoutes(router chi.Router) {
	router.Get("/me", s.me)
	router.Patch("/me/profile", s.updateProfile)
	router.Put("/me/password", s.changePassword)
	router.Get("/me/sessions", s.sessionsHTTP)
	router.Delete("/me/sessions/{sessionID}", s.revokeSessionHTTP)
	router.Delete("/me", s.deactivate)
	router.Delete("/me/account", s.deactivate)
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	var input RegisterInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	session, err := s.Register(r.Context(), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusCreated, session)
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Account  string `json:"account"`
		Identity string `json:"identity"`
		Password string `json:"password"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if input.Account == "" {
		input.Account = input.Identity
	}
	session, err := s.Login(r.Context(), input.Account, input.Password)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, session)
}

func (s *Service) refresh(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	session, err := s.Refresh(r.Context(), input.RefreshToken)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, session)
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	current, _ := UserFromContext(r.Context())
	user, err := s.User(r.Context(), current.ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, user)
}

func (s *Service) updateProfile(w http.ResponseWriter, r *http.Request) {
	var input ProfileInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := UserFromContext(r.Context())
	user, err := s.UpdateProfile(r.Context(), current.ID, input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, user)
}

func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	current, _ := UserFromContext(r.Context())
	sessionID, _ := SessionFromContext(r.Context())
	if err := s.ChangePassword(r.Context(), current.ID, sessionID, input.CurrentPassword, input.NewPassword); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"changed": true})
}

func (s *Service) deactivate(w http.ResponseWriter, r *http.Request) {
	current, _ := UserFromContext(r.Context())
	if err := s.Deactivate(r.Context(), current.ID); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"deactivated": true})
}

// RequireAuth supplies the authenticated user and device session to protected routes.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFromContext(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		user, sessionID, err := s.Authenticate(r.Context(), ParseBearer(r.Header.Get("Authorization")))
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		ctx := ContextWithUser(r.Context(), user, sessionID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalAuth enriches public feed/detail responses when a valid token is present.
// A supplied but invalid token is rejected instead of silently becoming a guest.
func (s *Service) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ParseBearer(r.Header.Get("Authorization"))
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		user, sessionID, err := s.Authenticate(r.Context(), token)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(ContextWithUser(r.Context(), user, sessionID)))
	})
}

func (s *Service) RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, role := range roles {
		allowed[role] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok {
				platform.WriteError(w, r, platform.Problem(http.StatusUnauthorized, "authentication_required", "请先登录"))
				return
			}
			if !allowed[user.Role] {
				platform.WriteError(w, r, platform.Problem(http.StatusForbidden, "insufficient_role", "当前账号没有执行此操作的权限"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Service) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	sessionID, _ := SessionFromContext(r.Context())
	if err := s.Logout(r.Context(), user.ID, sessionID); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"revoked": true})
}

func AuthorizationToken(r *http.Request) string {
	return ParseBearer(strings.TrimSpace(r.Header.Get("Authorization")))
}
