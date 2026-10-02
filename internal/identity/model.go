// Identity models are the only shapes returned by login and authentication.
package identity

type User struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Email       string `json:"email,omitempty"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	Bio         string `json:"bio,omitempty"`
	Role        string `json:"role,omitempty"`
	Status      string `json:"-"`
	CreatedAt   string `json:"created_at,omitempty"`
}

type Session struct {
	SessionID    string `json:"session_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	User         User   `json:"user"`
}
