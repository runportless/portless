package client

import (
	"context"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// WithMockMetadata requests compact metadata receipts for mock mutations and previews.
func (c *Client) WithMockMetadata() *Client { clone := *c; clone.mockMetadata = true; return &clone }

func mockMetadataValues(query contract.MockMetadataQuery) url.Values {
	values := url.Values{"view": {"metadata"}, "offset": {strconv.Itoa(query.Offset)}, "limit": {strconv.Itoa(query.Limit)}}
	if !query.ExpectedModifiedAt.IsZero() {
		values.Set("expectedModifiedAt", query.ExpectedModifiedAt.UTC().Format(time.RFC3339Nano))
	}
	return values
}

// ListMockScenarioMetadata returns a bounded metadata page without saved payloads.
func (c *Client) ListMockScenarioMetadata(ctx context.Context, project, environment string, query contract.MockMetadataQuery) (contract.MockScenarioMetadataList, error) {
	var result contract.MockScenarioMetadataList
	err := c.do(ctx, http.MethodGet, mocksPath(project, environment)+"?"+mockMetadataValues(query).Encode(), nil, &result)
	return result, err
}

// MockScenarioMetadata returns a revision-bound page of safe route metadata.
func (c *Client) MockScenarioMetadata(ctx context.Context, project, environment, scenario string, query contract.MockMetadataQuery) (contract.MockScenarioMetadataPage, error) {
	var result contract.MockScenarioMetadataPage
	err := c.do(ctx, http.MethodGet, mocksPath(project, environment)+"/"+EscapePath(scenario)+"?"+mockMetadataValues(query).Encode(), nil, &result)
	return result, err
}

// MockRoute returns one route's metadata and an optional bounded payload JSON range.
func (c *Client) MockRoute(ctx context.Context, project, environment, scenario, route string, query contract.MockRouteQuery) (contract.MockRouteDetail, error) {
	values := mockMetadataValues(contract.MockMetadataQuery{Offset: query.Offset, Limit: query.Limit, ExpectedModifiedAt: query.ExpectedModifiedAt})
	values.Set("includePayloads", strconv.FormatBool(query.IncludePayloads))
	var result contract.MockRouteDetail
	err := c.do(ctx, http.MethodGet, mocksPath(project, environment)+"/"+EscapePath(scenario)+"/routes/"+EscapePath(route)+"?"+values.Encode(), nil, &result)
	return result, err
}

// TrafficTracePage returns a bounded, revision-bound page of trace spans.
func (c *Client) TrafficTracePage(ctx context.Context, project, environment string, number int64, query contract.TrafficTracePageQuery) (contract.TrafficTracePage, error) {
	values := url.Values{"view": {"page"}, "offset": {strconv.Itoa(query.Offset)}, "limit": {strconv.Itoa(query.Limit)}, "expectedRevision": {strconv.FormatUint(query.ExpectedRevision, 10)}}
	var result contract.TrafficTracePage
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/traffic/traces/"+strconv.FormatInt(number, 10)+"?"+values.Encode(), nil, &result)
	return result, err
}
