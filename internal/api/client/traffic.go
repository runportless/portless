package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/portless-run/portless/internal/api/contract"
)

func trafficValues(query contract.TrafficQuery) url.Values {
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

func (c *Client) Traffic(ctx context.Context, project, environment string, query contract.TrafficQuery) (contract.TrafficList, error) {
	var result contract.TrafficList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/traffic?"+trafficValues(query).Encode(), nil, &result)
	return result, err
}

func (c *Client) TrafficEvent(ctx context.Context, project, environment string, sequence int64) (contract.TrafficEvent, error) {
	var result contract.TrafficEvent
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/traffic/"+strconv.FormatInt(sequence, 10), nil, &result)
	return result, err
}

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

func (c *Client) ListRecordings(ctx context.Context, project, environment string, limit int) (contract.RecordingList, error) {
	var result contract.RecordingList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/recordings?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

func (c *Client) Recording(ctx context.Context, project, environment, name string) (contract.Recording, error) {
	var result contract.Recording
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/recordings/"+EscapePath(name), nil, &result)
	return result, err
}

func (c *Client) StartRecording(ctx context.Context, project, environment string, input contract.Recording) (contract.Recording, error) {
	var result contract.Recording
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/recordings", input, &result)
	return result, err
}

func (c *Client) StopRecording(ctx context.Context, project, environment, name string) (contract.Recording, error) {
	var result contract.Recording
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/recordings/"+EscapePath(name)+"/stop", nil, &result)
	return result, err
}

func (c *Client) ExportRecording(ctx context.Context, project, environment, name string) ([]byte, error) {
	var result []byte
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/recordings/"+EscapePath(name)+"/export", nil, &result)
	return result, err
}

func (c *Client) DeleteRecording(ctx context.Context, project, environment, name string) error {
	return c.do(ctx, http.MethodDelete, environmentPath(project, environment)+"/recordings/"+EscapePath(name), nil, nil)
}

func (c *Client) ListFaults(ctx context.Context, project, environment string, limit int) (contract.FaultList, error) {
	var result contract.FaultList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/faults?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

func (c *Client) Fault(ctx context.Context, project, environment, name string) (contract.FaultRule, error) {
	var result contract.FaultRule
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/faults/"+EscapePath(name), nil, &result)
	return result, err
}

func (c *Client) CreateFault(ctx context.Context, project, environment string, input contract.FaultRule) (contract.FaultRule, error) {
	var result contract.FaultRule
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/faults", input, &result)
	return result, err
}

func (c *Client) SetFaultEnabled(ctx context.Context, project, environment, name string, enabled bool) (contract.FaultRule, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var result contract.FaultRule
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/faults/"+EscapePath(name)+"/"+action, nil, &result)
	return result, err
}

func (c *Client) DeleteFault(ctx context.Context, project, environment, name string) error {
	return c.do(ctx, http.MethodDelete, environmentPath(project, environment)+"/faults/"+EscapePath(name), nil, nil)
}

func (c *Client) DisableAllFaults(ctx context.Context, project, environment string) (contract.DisableFaultsResponse, error) {
	var result contract.DisableFaultsResponse
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/faults/disable-all", nil, &result)
	return result, err
}
