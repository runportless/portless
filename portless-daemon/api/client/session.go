package client

import (
	"context"
	"net/http"

	"github.com/portless-run/portless/portless-daemon/api/contract"
)

func (c *Client) CreateBrowserClaim(ctx context.Context, next string) (contract.BrowserClaimResponse, error) {
	var result contract.BrowserClaimResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/browser-claims", contract.BrowserClaimRequest{Next: next}, &result)
	return result, err
}
