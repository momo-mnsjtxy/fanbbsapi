package commerce

import (
	"net/http"
	"strconv"
	"strings"

	"fanbbs.local/backend/internal/identity"
	"fanbbs.local/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func (s *Service) RegisterRoutes(r chi.Router, auth *identity.Service) {
	r.Get("/product-types", s.listTypesHTTP(false))
	r.Get("/products", s.listProductsHTTP(false))
	r.Get("/products/{productID}", s.productHTTP(false))
	r.Get("/ranks", s.ranksHTTP)
	r.Get("/avatar-frames", s.framesHTTP(false))
	r.Group(func(p chi.Router) {
		p.Use(auth.RequireAuth)
		p.Get("/me/cart", s.cartHTTP)
		p.Get("/me/shipping-addresses", s.addressesHTTP)
		p.Post("/me/shipping-addresses", s.createAddressHTTP)
		p.Patch("/me/shipping-addresses/{addressID}", s.updateAddressHTTP)
		p.Delete("/me/shipping-addresses/{addressID}", s.deleteAddressHTTP)
		p.Put("/me/cart/{productID}", s.setCartHTTP)
		p.Delete("/me/cart/{productID}", s.deleteCartHTTP)
		p.Post("/orders", s.createOrderHTTP)
		p.Get("/orders", s.ordersHTTP(false))
		p.Get("/orders/{orderID}", s.orderHTTP(false))
		p.Get("/orders/{orderID}/tracking", s.trackingHTTP)
		p.Post("/orders/{orderID}/cancel", s.cancelOrderHTTP)
		p.Post("/me/check-in", s.checkInHTTP)
		p.Get("/me/gamification", s.gamificationHTTP)
		p.Get("/me/points", s.pointsHTTP)
		p.Get("/tasks", s.tasksHTTP(false))
		p.Get("/me/avatar-frames", s.framesHTTP(false))
		p.Put("/me/avatar-frame", s.selectFrameHTTP)
		p.With(auth.RequireRole("admin")).Get("/admin/product-types", s.listTypesHTTP(true))
		p.With(auth.RequireRole("admin")).Post("/admin/product-types", s.createTypeHTTP)
		p.With(auth.RequireRole("admin")).Patch("/admin/product-types/{typeID}", s.updateTypeHTTP)
		p.With(auth.RequireRole("admin")).Delete("/admin/product-types/{typeID}", s.deleteTypeHTTP)
		p.With(auth.RequireRole("admin")).Get("/admin/products", s.listProductsHTTP(true))
		p.With(auth.RequireRole("admin")).Get("/admin/products/{productID}", s.productHTTP(true))
		p.With(auth.RequireRole("admin")).Post("/admin/products", s.createProductHTTP)
		p.With(auth.RequireRole("admin")).Patch("/admin/products/{productID}", s.updateProductHTTP)
		p.With(auth.RequireRole("admin")).Delete("/admin/products/{productID}", s.deleteProductHTTP)
		p.With(auth.RequireRole("admin")).Get("/admin/orders", s.ordersHTTP(true))
		p.With(auth.RequireRole("admin")).Get("/admin/orders/{orderID}", s.orderHTTP(true))
		p.With(auth.RequireRole("admin")).Patch("/admin/orders/{orderID}", s.transitionOrderHTTP)
		p.With(auth.RequireRole("admin")).Post("/admin/orders/{orderID}/tracking-events", s.addTrackingHTTP)
		p.With(auth.RequireRole("admin")).Get("/admin/tasks", s.tasksHTTP(true))
		p.With(auth.RequireRole("admin")).Post("/admin/tasks", s.createTaskHTTP)
		p.With(auth.RequireRole("admin")).Patch("/admin/tasks/{taskID}", s.updateTaskHTTP)
		p.With(auth.RequireRole("admin")).Post("/admin/users/{userID}/tasks/{taskID}/award", s.awardTaskHTTP)
		p.With(auth.RequireRole("admin")).Get("/admin/avatar-frames", s.framesHTTP(true))
		p.With(auth.RequireRole("admin")).Post("/admin/avatar-frames", s.createFrameHTTP)
		p.With(auth.RequireRole("admin")).Patch("/admin/avatar-frames/{frameID}", s.updateFrameHTTP)
		p.With(auth.RequireRole("admin")).Post("/admin/users/{userID}/avatar-frames/{frameID}", s.grantFrameHTTP)
		p.With(auth.RequireRole("admin")).Post("/admin/users/{userID}/points", s.grantPointsHTTP)
	})
}

func pagination(r *http.Request) (int, int, error) {
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		value, err := platform.DecodeCursor(raw)
		if err != nil {
			return 0, 0, platform.Validation(map[string][]string{"cursor": {"游标无效"}})
		}
		offset = value
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			return 0, 0, platform.Validation(map[string][]string{"limit": {"必须在 1 到 100 之间"}})
		}
		limit = value
	}
	return offset, limit, nil
}
func currentUser(r *http.Request) identity.User {
	u, _ := identity.UserFromContext(r.Context())
	return u
}

func (s *Service) listTypesHTTP(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.ListTypes(r.Context(), admin)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteData(w, r, http.StatusOK, items)
	}
}
func (s *Service) listProductsHTTP(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		offset, limit, err := pagination(r)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		items, next, err := s.ListProducts(r.Context(), r.URL.Query().Get("type_id"), admin, offset, limit)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteList(w, r, items, next)
	}
}
func (s *Service) productHTTP(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		item, err := s.Product(r.Context(), chi.URLParam(r, "productID"), admin)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.Itoa(item.Version)))
		platform.WriteData(w, r, http.StatusOK, item)
	}
}
func (s *Service) createTypeHTTP(w http.ResponseWriter, r *http.Request) {
	var input TypeInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.CreateType(r.Context(), currentUser(r), platform.RequestID(r.Context()), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusCreated, item)
}
func (s *Service) updateTypeHTTP(w http.ResponseWriter, r *http.Request) {
	var input TypeInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.UpdateType(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "typeID"), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) deleteTypeHTTP(w http.ResponseWriter, r *http.Request) {
	err := s.DeleteType(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "typeID"), r.URL.Query().Get("reason"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"deleted": true})
}
func (s *Service) createProductHTTP(w http.ResponseWriter, r *http.Request) {
	var input ProductInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.CreateProduct(r.Context(), currentUser(r), platform.RequestID(r.Context()), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", "\"1\"")
	platform.WriteData(w, r, http.StatusCreated, item)
}
func (s *Service) updateProductHTTP(w http.ResponseWriter, r *http.Request) {
	var input ProductInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.UpdateProduct(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "productID"), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(item.Version)))
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) deleteProductHTTP(w http.ResponseWriter, r *http.Request) {
	err := s.DeleteProduct(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "productID"), r.URL.Query().Get("reason"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"deleted": true})
}
func (s *Service) cartHTTP(w http.ResponseWriter, r *http.Request) {
	items, err := s.Cart(r.Context(), currentUser(r).ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, items)
}
func (s *Service) setCartHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Quantity int `json:"quantity"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.SetCartItem(r.Context(), currentUser(r).ID, chi.URLParam(r, "productID"), input.Quantity)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) deleteCartHTTP(w http.ResponseWriter, r *http.Request) {
	changed, err := s.DeleteCartItem(r.Context(), currentUser(r).ID, chi.URLParam(r, "productID"))
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]bool{"deleted": changed})
}
func (s *Service) createOrderHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AddressID string `json:"address_id"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := platform.DecodeJSON(w, r, &input); err != nil {
			platform.WriteError(w, r, err)
			return
		}
	}
	item, replayed, err := s.CreateOrderWithAddress(r.Context(), currentUser(r).ID, r.Header.Get("Idempotency-Key"), input.AddressID)
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
func (s *Service) ordersHTTP(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		offset, limit, err := pagination(r)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		items, next, err := s.ListOrders(r.Context(), currentUser(r).ID, r.URL.Query().Get("status"), admin, offset, limit)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteList(w, r, items, next)
	}
}
func (s *Service) orderHTTP(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		item, err := s.Order(r.Context(), chi.URLParam(r, "orderID"), currentUser(r).ID, admin)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteData(w, r, http.StatusOK, item)
	}
}
func (s *Service) cancelOrderHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := platform.DecodeJSON(w, r, &input); err != nil {
			platform.WriteError(w, r, err)
			return
		}
	}
	item, _, err := s.TransitionOrder(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "orderID"), OrderTransitionInput{Status: "cancelled", Reason: input.Reason}, false)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) transitionOrderHTTP(w http.ResponseWriter, r *http.Request) {
	var input OrderTransitionInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, _, err := s.TransitionOrder(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "orderID"), input, true)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) checkInHTTP(w http.ResponseWriter, r *http.Request) {
	item, replayed, err := s.CheckIn(r.Context(), currentUser(r).ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) gamificationHTTP(w http.ResponseWriter, r *http.Request) {
	item, err := s.GamificationStatus(r.Context(), currentUser(r).ID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) pointsHTTP(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pagination(r)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	items, next, err := s.PointEvents(r.Context(), currentUser(r).ID, offset, limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteList(w, r, items, next)
}
func (s *Service) ranksHTTP(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	items, err := s.Ranks(r.Context(), limit)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, items)
}
func (s *Service) tasksHTTP(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := ""
		if !admin {
			userID = currentUser(r).ID
		}
		items, err := s.Tasks(r.Context(), userID, admin)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteData(w, r, http.StatusOK, items)
	}
}
func (s *Service) createTaskHTTP(w http.ResponseWriter, r *http.Request) {
	var input TaskInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.CreateTask(r.Context(), currentUser(r), platform.RequestID(r.Context()), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusCreated, item)
}
func (s *Service) updateTaskHTTP(w http.ResponseWriter, r *http.Request) {
	var input TaskInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.UpdateTask(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "taskID"), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) awardTaskHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ClaimKey string `json:"claim_key"`
		Reason   string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, replayed, err := s.AwardTask(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "userID"), chi.URLParam(r, "taskID"), input.ClaimKey, input.Reason)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) framesHTTP(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := ""
		if !admin {
			userID = currentUser(r).ID
		}
		items, err := s.Frames(r.Context(), userID, admin)
		if err != nil {
			platform.WriteError(w, r, err)
			return
		}
		platform.WriteData(w, r, http.StatusOK, items)
	}
}
func (s *Service) createFrameHTTP(w http.ResponseWriter, r *http.Request) {
	var input FrameInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.CreateFrame(r.Context(), currentUser(r), platform.RequestID(r.Context()), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusCreated, item)
}
func (s *Service) updateFrameHTTP(w http.ResponseWriter, r *http.Request) {
	var input FrameInput
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.UpdateFrame(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "frameID"), input)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) grantFrameHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := platform.DecodeJSON(w, r, &input); err != nil {
			platform.WriteError(w, r, err)
			return
		}
	}
	item, _, err := s.GrantFrame(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "userID"), chi.URLParam(r, "frameID"), input.Reason)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
func (s *Service) selectFrameHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		FrameID string `json:"frame_id"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, err := s.SelectFrame(r.Context(), currentUser(r).ID, input.FrameID)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	platform.WriteData(w, r, http.StatusOK, map[string]any{"selected_frame": item})
}
func (s *Service) grantPointsHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Amount      int    `json:"amount"`
		Description string `json:"description"`
	}
	if err := platform.DecodeJSON(w, r, &input); err != nil {
		platform.WriteError(w, r, err)
		return
	}
	item, replayed, err := s.GrantPoints(r.Context(), currentUser(r), platform.RequestID(r.Context()), chi.URLParam(r, "userID"), r.Header.Get("Idempotency-Key"), input.Description, input.Amount)
	if err != nil {
		platform.WriteError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	platform.WriteData(w, r, http.StatusOK, item)
}
