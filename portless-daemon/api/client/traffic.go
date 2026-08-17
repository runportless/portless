package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/portless-run/portless/portless-daemon/api/contract"
)

func trafficValues(query contract.TrafficExchangeQuery) url.Values {
	values := url.Values{}
	if query.Protocol != "" {
		values.Set("protocol", query.Protocol)
	}
	if query.Service != "" {
		values.Set("service", query.Service)
	}
	if query.Edge != "" {
		values.Set("edge", query.Edge)
	}
	if query.After > 0 {
		values.Set("after", strconv.FormatInt(query.After, 10))
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	return values
}

func traceValues(query contract.TrafficTraceQuery) url.Values {
	values := url.Values{}
	if query.Service != "" {
		values.Set("service", query.Service)
	}
	if query.Edge != "" {
		values.Set("edge", query.Edge)
	}
	if query.IncludeBackground {
		values.Set("background", "include")
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	return values
}

// TrafficExchanges returns captured exchanges matching query.
func (c *Client) TrafficExchanges(ctx context.Context, project, environment string, query contract.TrafficExchangeQuery) (contract.TrafficExchangeList, error) {
	var result contract.TrafficExchangeList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/traffic/exchanges?"+trafficValues(query).Encode(), nil, &result)
	return result, err
}

// TrafficExchange returns one captured exchange by sequence number.
func (c *Client) TrafficExchange(ctx context.Context, project, environment string, sequence int64) (contract.TrafficExchange, error) {
	var result contract.TrafficExchange
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/traffic/exchanges/"+strconv.FormatInt(sequence, 10), nil, &result)
	return result, err
}

// TrafficTraces returns trace summaries matching query.
func (c *Client) TrafficTraces(ctx context.Context, project, environment string, query contract.TrafficTraceQuery) (contract.TrafficTraceList, error) {
	var result contract.TrafficTraceList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/traffic/traces?"+traceValues(query).Encode(), nil, &result)
	return result, err
}

// TrafficTrace returns one full trace projection by environment-local number.
func (c *Client) TrafficTrace(ctx context.Context, project, environment string, number int64) (contract.TrafficTrace, error) {
	var result contract.TrafficTrace
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/traffic/traces/"+strconv.FormatInt(number, 10), nil, &result)
	return result, err
}

// OpenEventStream opens an authenticated server-sent event stream for the
// requested environment topics. The caller must close the returned body.
func (c *Client) OpenEventStream(ctx context.Context, project, environment string, topics ...string) (io.ReadCloser, error) {
	query := url.Values{}
	for _, topic := range topics {
		query.Add("topic", topic)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+environmentPath(project, environment)+"/stream?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "text/event-stream")
	streamClient := *c.http
	streamClient.Timeout = 0
	response, err := streamClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response.Body, nil
	}
	defer response.Body.Close()
	content, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		return nil, readErr
	}
	var envelope contract.ErrorEnvelope
	_ = json.Unmarshal(content, &envelope)
	message := envelope.Error.Message
	if message == "" {
		message = strings.TrimSpace(string(content))
	}
	if message == "" {
		message = response.Status
	}
	return nil, &ClientError{Status: response.StatusCode, Code: envelope.Error.Code, Message: message, Subject: envelope.Error.Subject, Details: envelope.Error.Details, Remediation: envelope.Error.Remediation}
}

// ListRecordings returns retained recordings for an environment.
func (c *Client) ListRecordings(ctx context.Context, project, environment string, limit int) (contract.RecordingList, error) {
	var result contract.RecordingList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/recordings?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

// Recording returns one named traffic recording.
func (c *Client) Recording(ctx context.Context, project, environment, name string) (contract.Recording, error) {
	var result contract.Recording
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/recordings/"+EscapePath(name), nil, &result)
	return result, err
}

// StartRecording creates and activates a bounded traffic recording.
func (c *Client) StartRecording(ctx context.Context, project, environment string, input contract.Recording) (contract.Recording, error) {
	var result contract.Recording
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/recordings", input, &result)
	return result, err
}

// StopRecording deactivates a recording while retaining captured events.
func (c *Client) StopRecording(ctx context.Context, project, environment, name string) (contract.Recording, error) {
	var result contract.Recording
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/recordings/"+EscapePath(name)+"/stop", nil, &result)
	return result, err
}

// ExportRecording returns the portable JSON representation of a recording.
func (c *Client) ExportRecording(ctx context.Context, project, environment, name string) ([]byte, error) {
	var result []byte
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/recordings/"+EscapePath(name)+"/export", nil, &result)
	return result, err
}

// DeleteRecording permanently removes one retained recording.
func (c *Client) DeleteRecording(ctx context.Context, project, environment, name string) error {
	return c.do(ctx, http.MethodDelete, environmentPath(project, environment)+"/recordings/"+EscapePath(name), nil, nil)
}

// ListFaults returns fault rules for an environment.
func (c *Client) ListFaults(ctx context.Context, project, environment string, limit int) (contract.FaultList, error) {
	var result contract.FaultList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/faults?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

// Fault returns one named fault rule.
func (c *Client) Fault(ctx context.Context, project, environment, name string) (contract.FaultRule, error) {
	var result contract.FaultRule
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/faults/"+EscapePath(name), nil, &result)
	return result, err
}

// CreateFault creates and enables a scoped fault rule.
func (c *Client) CreateFault(ctx context.Context, project, environment string, input contract.FaultRule) (contract.FaultRule, error) {
	var result contract.FaultRule
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/faults", input, &result)
	return result, err
}

// SetFaultEnabled enables or disables one fault rule.
func (c *Client) SetFaultEnabled(ctx context.Context, project, environment, name string, enabled bool) (contract.FaultRule, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var result contract.FaultRule
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/faults/"+EscapePath(name)+"/"+action, nil, &result)
	return result, err
}

// DeleteFault permanently removes one fault rule.
func (c *Client) DeleteFault(ctx context.Context, project, environment, name string) error {
	return c.do(ctx, http.MethodDelete, environmentPath(project, environment)+"/faults/"+EscapePath(name), nil, nil)
}

// DisableAllFaults disables every active fault rule in an environment.
func (c *Client) DisableAllFaults(ctx context.Context, project, environment string) (contract.DisableFaultsResponse, error) {
	var result contract.DisableFaultsResponse
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/faults/disable-all", nil, &result)
	return result, err
}
