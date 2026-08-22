package client

import (
	"context"
	"net/http"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

// CreateBrowserClaim creates a short-lived, single-use browser authentication URL.
func (c *Client) CreateBrowserClaim(ctx context.Context, next string) (contract.BrowserClaimResponse, error) {
	var result contract.BrowserClaimResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/browser-claims", contract.BrowserClaimRequest{Next: next}, &result)
	return result, err
}
