package client

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// RecordingExportChunk reads a bounded base64 segment of a stable recording snapshot.
func (c *Client) RecordingExportChunk(ctx context.Context, project, environment, name string, query contract.RecordingExportChunkQuery) (contract.RecordingExportChunk, error) {
	values := url.Values{"cursor": {query.Cursor}, "maxBytes": {strconv.Itoa(query.MaxBytes)}}
	var result contract.RecordingExportChunk
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/recordings/"+EscapePath(name)+"/export/chunks?"+values.Encode(), nil, &result)
	return result, err
}

// WriteRecordingExport streams the complete selected snapshot to writer, beyond buffered read limits.
// On any error the writer may contain a partial document; file callers should stage and rename it.
func (c *Client) WriteRecordingExport(ctx context.Context, project, environment, name string, writer io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+environmentPath(project, environment)+"/recordings/"+EscapePath(name)+"/export", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if c.clientKind != "" {
		request.Header.Set(contract.ClientKindHeader, string(c.clientKind))
	}
	streamClient := *c.http
	streamClient.Timeout = 0
	response, err := streamClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope contract.ErrorEnvelope
		content, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if err != nil {
			return err
		}
		_ = json.Unmarshal(content, &envelope)
		message := envelope.Error.Message
		if message == "" {
			message = response.Status
		}
		return &ClientError{Status: response.StatusCode, Code: envelope.Error.Code, Message: message, Subject: envelope.Error.Subject, Details: envelope.Error.Details, Remediation: envelope.Error.Remediation}
	}
	if _, err := io.Copy(writer, response.Body); err != nil {
		return fmt.Errorf("stream recording export: %w", err)
	}
	return nil
}
