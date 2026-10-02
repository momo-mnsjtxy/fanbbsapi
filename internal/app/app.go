// Package app assembles the API once. Domains register their own routes and keep
// HTTP parsing separate from transactional business work.
package app

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"

	"fanbbs.local/backend/internal/blob"
	"fanbbs.local/backend/internal/capabilities"
	"fanbbs.local/backend/internal/commerce"
	"fanbbs.local/backend/internal/community"
	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

type App struct {
	DB        *sql.DB
	Identity  *identity.Service
	Community *community.Service
	Commerce  *commerce.Service
	Metrics   *platform.Metrics
	Handler   http.Handler
}

func New(db *sql.DB) *App {
	local, err := blob.NewLocal(filepath.Join(os.TempDir(), "fanbbs-local-blobs"))
	if err != nil {
		panic(err)
	}
	return NewWithBlob(db, local)
}

func NewWithBlob(db *sql.DB, blobs blob.Store) *App {
	identityService := identity.NewService(db)
	communityService := community.NewService(db, blobs)
	commerceService := commerce.NewService(db)
	metrics := platform.NewMetrics()
	router := chi.NewRouter()
	router.Use(platform.RequestIDMiddleware)
	router.Use(platform.SecurityHeaders)
	router.Use(metrics.Middleware)
	router.Use(platform.RecoverMiddleware)
	metricsHandler := func(w http.ResponseWriter, r *http.Request) {
		platform.WriteData(w, r, http.StatusOK, metrics.Snapshot())
	}

	router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteData(w, r, http.StatusOK, map[string]string{"status": "ok"})
	})
	router.Get("/metrics", metricsHandler)
	router.Route("/api/v1", func(api chi.Router) {
		api.Get("/metrics", metricsHandler)
		api.Mount("/auth", identityService.Routes())
		api.With(identityService.RequireAuth).Post("/auth/logout", identityService.LogoutHandler)
		api.Group(func(accounts chi.Router) {
			accounts.Use(identityService.RequireAuth)
			identityService.RegisterAccountRoutes(accounts)
		})
		api.Get("/capabilities", func(w http.ResponseWriter, r *http.Request) {
			platform.WriteData(w, r, http.StatusOK, capabilities.Status())
		})
		api.Group(func(communityRoutes chi.Router) {
			communityRoutes.Use(identityService.OptionalAuth)
			communityRoutes.Mount("/", communityService.Routes(identityService))
		})
		api.Group(func(commerceRoutes chi.Router) {
			commerceRoutes.Use(identityService.OptionalAuth)
			commerceService.RegisterRoutes(commerceRoutes, identityService)
		})
	})

	return &App{DB: db, Identity: identityService, Community: communityService, Commerce: commerceService, Metrics: metrics, Handler: router}
}
