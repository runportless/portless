package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

func replayPath(project, environment string, number int64) string {
	path := environmentPath(project, environment) + "/traffic/replays"
	if number > 0 {
		path += "/" + strconv.FormatInt(number, 10)
	}
	return path
}

// PrepareTrafficReplay creates a non-executing workspace from a live exchange.
func (c *Client) PrepareTrafficReplay(ctx context.Context, project, environment string, input contract.PrepareTrafficReplayRequest) (contract.TrafficReplayWorkspace, error) {
	var result contract.TrafficReplayWorkspace
	err := c.do(ctx, http.MethodPost, replayPath(project, environment, 0), input, &result)
	return result, err
}

// UpdateTrafficReplayDraft prepares a request without contacting its application.
func (c *Client) UpdateTrafficReplayDraft(ctx context.Context, project, environment string, number int64, input contract.UpdateTrafficReplayDraftRequest) (contract.TrafficReplayWorkspace, error) {
	var result contract.TrafficReplayWorkspace
	err := c.do(ctx, http.MethodPut, replayPath(project, environment, number)+"/draft", input, &result)
	return result, err
}

// RunTrafficReplay admits one reviewed request with duplicate suppression.
func (c *Client) RunTrafficReplay(ctx context.Context, project, environment string, number int64, input contract.RunTrafficReplayRequest) (contract.TrafficReplayWorkspace, error) {
	var result contract.TrafficReplayWorkspace
	err := c.do(ctx, http.MethodPost, replayPath(project, environment, number)+"/runs", input, &result)
	return result, err
}

// TrafficReplay retrieves a workspace, optionally including the bounded latest comparison.
func (c *Client) TrafficReplay(ctx context.Context, project, environment string, number int64, expected contract.TrafficReplayIdentity, includeResult bool) (contract.TrafficReplayWorkspace, error) {
	values := url.Values{}
	if !expected.CreatedAt.IsZero() {
		values.Set("expectedCreatedAt", expected.CreatedAt.Format(time.RFC3339Nano))
	}
	if !expected.DaemonStartedAt.IsZero() {
		values.Set("expectedDaemonStartedAt", expected.DaemonStartedAt.Format(time.RFC3339Nano))
	}
	if includeResult {
		values.Set("include", "result")
	}
	var result contract.TrafficReplayWorkspace
	err := c.do(ctx, http.MethodGet, replayPath(project, environment, number)+"?"+values.Encode(), nil, &result)
	return result, err
}

// TouchTrafficReplay records user activity without preparing or sending an application request.
func (c *Client) TouchTrafficReplay(ctx context.Context, project, environment string, number int64, expected contract.TrafficReplayIdentity) error {
	return c.do(ctx, http.MethodPost, replayPath(project, environment, number)+"/activity", expected, nil)
}

// DeleteTrafficReplay releases workspace payloads without cancelling admitted runs or forgetting their receipts.
func (c *Client) DeleteTrafficReplay(ctx context.Context, project, environment string, number int64, expected contract.TrafficReplayIdentity) error {
	return c.do(ctx, http.MethodDelete, replayPath(project, environment, number), expected, nil)
}
