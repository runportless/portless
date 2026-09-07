package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
)

// WithResourceVersion adds an If-Match condition to this client's mutations.
// The daemon validates resource-specific fields atomically with each supported change.
func (c *Client) WithResourceVersion(version contract.ResourceVersion) *Client {
	clone := *c
	clone.resourceVersion = &version
	return &clone
}

func resourceVersionHeader(version contract.ResourceVersion) string {
	encoded, _ := json.Marshal(version)
	return `"` + base64.RawURLEncoding.EncodeToString(encoded) + `"`
}

// PreviewMockDeletion returns exact routes and identity without changing state.
// An empty route requests deletion of the complete disabled scenario.
func (c *Client) PreviewMockDeletion(ctx context.Context, project, environment, scenario, route string) (contract.MockDeletionPreview, error) {
	path := mocksPath(project, environment) + "/" + EscapePath(scenario)
	if route != "" {
		path += "/routes/" + EscapePath(route)
	}
	var result contract.MockDeletionPreview
	err := c.do(ctx, http.MethodDelete, path+"?mode=preview", nil, &result)
	return result, err
}

// PreviewRecordingDeletion returns retained-event counts and deletion eligibility.
func (c *Client) PreviewRecordingDeletion(ctx context.Context, project, environment, name string) (contract.RecordingDeletionPreview, error) {
	var result contract.RecordingDeletionPreview
	err := c.do(ctx, http.MethodDelete, environmentPath(project, environment)+"/recordings/"+EscapePath(name)+"?mode=preview", nil, &result)
	return result, err
}

// PreviewFaultDeletion returns the rule's identity and deletion effects.
func (c *Client) PreviewFaultDeletion(ctx context.Context, project, environment, name string) (contract.FaultDeletionPreview, error) {
	var result contract.FaultDeletionPreview
	err := c.do(ctx, http.MethodDelete, environmentPath(project, environment)+"/faults/"+EscapePath(name)+"?mode=preview", nil, &result)
	return result, err
}

// PreviewConfiguration returns exact affected environment metadata and an atomic apply condition.
// Empty environment selects the whole project; source selects a logical source or checkout.
func (c *Client) PreviewConfiguration(ctx context.Context, project, environment, source string) (contract.ConfigurationPreview, error) {
	path := "/api/v1/projects/" + EscapePath(project)
	if environment != "" {
		path = environmentPath(project, environment)
	}
	if source != "" {
		path += "/sources/" + EscapePath(source)
	}
	var result contract.ConfigurationPreview
	err := c.do(ctx, http.MethodDelete, path+"?mode=preview", nil, &result)
	return result, err
}
