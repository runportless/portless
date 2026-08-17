package contract

import "time"

// BrowserClaimRequest identifies the UI path to open after authentication.
type BrowserClaimRequest struct {
	Next string `json:"next"`
}

// BrowserClaimResponse contains a short-lived, single-use authentication URL.
type BrowserClaimResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Session describes the authenticated actor and browser mutation token.
type Session struct {
	Actor   string `json:"actor"`
	Browser bool   `json:"browser"`
	CSRF    string `json:"csrf"`
}
