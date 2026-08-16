package contract

import "time"

type BrowserClaimRequest struct {
	Next string `json:"next"`
}

type BrowserClaimResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type Session struct {
	Actor   string `json:"actor"`
	Browser bool   `json:"browser"`
	CSRF    string `json:"csrf"`
}
