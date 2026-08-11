package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

type ClientError struct {
	Status      int
	Code        string
	Message     string
	Subject     map[string]any
	Details     map[string]any
	Remediation []map[string]any
}

func (e *ClientError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

func Connect(ctx context.Context, paths Paths) (*Client, ControlRecord, error) {
	record, err := EnsureDaemon(ctx, paths)
	if err != nil {
		return nil, ControlRecord{}, err
	}
	tokenBytes, err := os.ReadFile(record.TokenPath)
	if err != nil {
		return nil, ControlRecord{}, fmt.Errorf("read CLI authentication token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, ControlRecord{}, errors.New("CLI authentication token is empty")
	}
	return &Client{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", record.Port), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}, record, nil
}

func (c *Client) Do(ctx context.Context, method, path string, input, output any) error {
	return c.DoWithHeaders(ctx, method, path, input, output, nil)
}

func (c *Client) DoWithHeaders(ctx context.Context, method, path string, input, output any, extraHeaders map[string]string) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range extraHeaders {
		request.Header.Set(name, value)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code        string           `json:"code"`
				Message     string           `json:"message"`
				Subject     map[string]any   `json:"subject"`
				Details     map[string]any   `json:"details"`
				Remediation []map[string]any `json:"remediation"`
			} `json:"error"`
		}
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

func EscapePath(values ...string) string {
	encoded := make([]string, len(values))
	for index, value := range values {
		encoded[index] = url.PathEscape(value)
	}
	return strings.Join(encoded, "/")
}
