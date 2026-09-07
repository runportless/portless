package contract

import (
	"github.com/runportless/portless/portless-daemon/model"
	"time"
)

// MockMetadataHeader requests payload-free mock mutation and preview receipts.
const MockMetadataHeader = "Portless-Mock-Metadata"

// ProjectMetadata contains safe logical topology and public environment summaries.
type ProjectMetadata = model.ProjectMetadata

// ProjectMetadataList is a bounded page of safe project summaries.
type ProjectMetadataList struct {
	Projects   []ProjectMetadata `json:"projects"`
	Total      int               `json:"total"`
	NextOffset int               `json:"nextOffset,omitempty"`
}

// ProjectDeclarationQuery selects an identity-bound range of safe declaration JSON.
type ProjectDeclarationQuery struct {
	Offset            int
	Limit             int
	ExpectedRevision  int64
	ExpectedCreatedAt time.Time
}

// ProjectDeclarationChunk contains a range of safe declaration JSON and its identity.
type ProjectDeclarationChunk struct {
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	Redacted  bool      `json:"redacted"`
	Content   TextRange `json:"content"`
}

// MockScenarioMetadata describes a mock without its saved application data.
type MockScenarioMetadata = model.MockScenarioMetadata

// MockRouteMetadata describes the matching structure of a saved route.
type MockRouteMetadata = model.MockRouteMetadata

// MockMetadataQuery selects a stable page of scenario routes or scenarios.
type MockMetadataQuery struct {
	Offset             int
	Limit              int
	ExpectedModifiedAt time.Time
}

// MockScenarioMetadataList is a bounded page of scenario summaries.
type MockScenarioMetadataList struct {
	Scenarios  []MockScenarioMetadata `json:"scenarios"`
	Total      int                    `json:"total"`
	NextOffset int                    `json:"nextOffset,omitempty"`
}

// MockScenarioMetadataPage contains one scenario and a page of route metadata.
type MockScenarioMetadataPage struct {
	Scenario   MockScenarioMetadata `json:"scenario"`
	Routes     []MockRouteMetadata  `json:"routes"`
	NextOffset int                  `json:"nextOffset,omitempty"`
}

// TextRange is a UTF-8-aligned byte range in a complete saved document.
// Content must be reassembled before parsing or submitting it as a replacement.
type TextRange struct {
	Content    string `json:"content"`
	MediaType  string `json:"mediaType"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"nextOffset,omitempty"`
	TotalBytes int    `json:"totalBytes"`
}

// MockRouteQuery selects metadata and an optional range of saved payload JSON.
type MockRouteQuery struct {
	IncludePayloads    bool
	Offset             int
	Limit              int
	ExpectedModifiedAt time.Time
}

// MockRouteDetail separates safe metadata from explicitly requested saved payloads.
type MockRouteDetail struct {
	Scenario MockScenarioMetadata `json:"scenario"`
	Route    MockRouteMetadata    `json:"route"`
	Payload  *TextRange           `json:"payload,omitempty"`
}

// TrafficTracePageQuery selects a revision-bound page of trace spans.
type TrafficTracePageQuery struct {
	Offset           int
	Limit            int
	ExpectedRevision uint64
}

// TrafficTracePage returns trace metadata and a bounded page of spans.
type TrafficTracePage struct {
	Trace      TrafficTrace `json:"trace"`
	NextOffset int          `json:"nextOffset,omitempty"`
}
