package client

import (
	"context"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ListProjectMetadata returns a bounded page of safe project summaries.
func (c *Client) ListProjectMetadata(ctx context.Context, offset, limit int) (contract.ProjectMetadataList, error) {
	var result contract.ProjectMetadataList
	values := url.Values{"view": {"metadata"}, "offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}
	err := c.do(ctx, http.MethodGet, "/api/v1/projects?"+values.Encode(), nil, &result)
	return result, err
}

// ProjectMetadata returns safe logical topology and public environment summaries.
func (c *Client) ProjectMetadata(ctx context.Context, project string) (contract.ProjectMetadata, error) {
	var result contract.ProjectMetadata
	err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+EscapePath(project)+"?view=metadata", nil, &result)
	return result, err
}

// ProjectDeclarationChunk returns a UTF-8-aligned range of the safe declaration document.
func (c *Client) ProjectDeclarationChunk(ctx context.Context, project string, query contract.ProjectDeclarationQuery) (contract.ProjectDeclarationChunk, error) {
	var result contract.ProjectDeclarationChunk
	values := url.Values{"view": {"chunk"}, "offset": {strconv.Itoa(query.Offset)}, "limit": {strconv.Itoa(query.Limit)}, "expectedRevision": {strconv.FormatInt(query.ExpectedRevision, 10)}}
	if !query.ExpectedCreatedAt.IsZero() {
		values.Set("expectedCreatedAt", query.ExpectedCreatedAt.UTC().Format(time.RFC3339Nano))
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+EscapePath(project)+"/declaration?"+values.Encode(), nil, &result)
	return result, err
}
