// Package client implements the authenticated HTTP transport used by Portless
// control-plane clients.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/portless-run/portless/portless-daemon/api/contract"
)

// Client is an authenticated typed client for one Portless daemon API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New constructs a daemon API client. A nil httpClient uses
// http.DefaultClient.
func New(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: httpClient}
}

// ClientError is a non-success daemon API response with its structured error
// metadata preserved.
type ClientError struct {
	Status      int
	Code        string
	Message     string
	Subject     map[string]any
	Details     map[string]any
	Remediation []contract.Remediation
}

// Error returns the daemon's error code and message.
func (e *ClientError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	return c.doWithHeaders(ctx, method, path, input, output, nil)
}

func (c *Client) doWithHeaders(ctx context.Context, method, path string, input, output any, extraHeaders map[string]string) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range extraHeaders {
		request.Header.Set(name, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope contract.ErrorEnvelope
		_ = json.Unmarshal(content, &envelope)
		message := envelope.Error.Message
		if message == "" {
			message = strings.TrimSpace(string(content))
		}
		if message == "" {
			message = response.Status
		}
		return &ClientError{Status: response.StatusCode, Code: envelope.Error.Code, Message: message, Subject: envelope.Error.Subject, Details: envelope.Error.Details, Remediation: envelope.Error.Remediation}
	}
	if output == nil || len(content) == 0 {
		return nil
	}
	if bytes, ok := output.(*[]byte); ok {
		*bytes = append((*bytes)[:0], content...)
		return nil
	}
	if err := json.Unmarshal(content, output); err != nil {
		return fmt.Errorf("decode daemon response: %w", err)
	}
	return nil
}

// EscapePath URL-escapes path segments and joins them with slashes.
func EscapePath(values ...string) string {
	encoded := make([]string, len(values))
	for index, value := range values {
		encoded[index] = url.PathEscape(value)
	}
	return strings.Join(encoded, "/")
}
