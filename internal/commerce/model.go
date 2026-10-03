package commerce

type ProductType struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Product struct {
	ID          string `json:"id"`
	TypeID      string `json:"type_id"`
	TypeName    string `json:"type_name"`
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Inventory   int    `json:"inventory"`
	Status      string `json:"status"`
	Version     int    `json:"version"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type CartItem struct {
	Product   Product `json:"product"`
	Quantity  int     `json:"quantity"`
	UpdatedAt string  `json:"updated_at"`
}

type OrderItem struct {
	ProductID string `json:"product_id"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Quantity  int    `json:"quantity"`
}

type Order struct {
	ID                 string                   `json:"id"`
	UserID             string                   `json:"user_id"`
	Status             string                   `json:"status"`
	FulfillmentCarrier string                   `json:"fulfillment_carrier"`
	TrackingCode       string                   `json:"tracking_code"`
	CancelledAt        string                   `json:"cancelled_at,omitempty"`
	FulfilledAt        string                   `json:"fulfilled_at,omitempty"`
	CreatedAt          string                   `json:"created_at"`
	UpdatedAt          string                   `json:"updated_at"`
	Items              []OrderItem              `json:"items"`
	ShippingAddress    *ShippingAddressSnapshot `json:"shipping_address,omitempty"`
	TrackingEvents     []TrackingEvent          `json:"tracking_events"`
}

type ShippingAddress struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Region        string `json:"region"`
	AddressLine   string `json:"address_line"`
	PostalCode    string `json:"postal_code"`
	Default       bool   `json:"is_default"`
	Version       int    `json:"version"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type ShippingAddressSnapshot struct {
	SourceAddressID string `json:"source_address_id,omitempty"`
	Label           string `json:"label"`
	RecipientName   string `json:"recipient_name"`
	Phone           string `json:"phone"`
	Region          string `json:"region"`
	AddressLine     string `json:"address_line"`
	PostalCode      string `json:"postal_code"`
}

type TrackingEvent struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Location    string `json:"location"`
	Source      string `json:"source"`
	OccurredAt  string `json:"occurred_at"`
	CreatedAt   string `json:"created_at"`
}

type AvatarFrame struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	ImageURL  string `json:"image_url"`
	Status    string `json:"status"`
	Entitled  bool   `json:"entitled,omitempty"`
	Selected  bool   `json:"selected,omitempty"`
	GrantedAt string `json:"granted_at,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Task struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	RepeatPolicy  string `json:"repeat_policy"`
	RewardPoints  int    `json:"reward_points"`
	RewardFrameID string `json:"reward_frame_id,omitempty"`
	Status        string `json:"status"`
	Claimed       bool   `json:"claimed,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type PointEvent struct {
	ID          string `json:"id"`
	Amount      int    `json:"amount"`
	EventType   string `json:"event_type"`
	EventKey    string `json:"event_key"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

type GamificationStatus struct {
	Balance        int          `json:"points_balance"`
	LifetimePoints int          `json:"lifetime_points"`
	Level          int          `json:"level"`
	LevelName      string       `json:"level_name"`
	Title          string       `json:"title"`
	Rank           int          `json:"rank"`
	SelectedFrame  *AvatarFrame `json:"selected_frame,omitempty"`
}

type RankEntry struct {
	Rank           int    `json:"rank"`
	UserID         string `json:"user_id"`
	DisplayName    string `json:"display_name"`
	LifetimePoints int    `json:"lifetime_points"`
	Level          int    `json:"level"`
	Title          string `json:"title"`
}
