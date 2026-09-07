package contract

import "github.com/runportless/portless/portless-daemon/model"

// TrafficExchangeQuery filters captured exchanges by protocol, service, edge,
// sequence, and result limit.
type TrafficExchangeQuery struct {
	Protocol string
	Service  string
	Edge     string
	After    int64
	Limit    int
}

// TrafficTraceQuery filters trace summaries by service, edge, background
// classification, and result limit.
type TrafficTraceQuery struct {
	Service           string
	Edge              string
	IncludeBackground bool
	Limit             int
}

// TrafficExchangeList is a collection of captured exchanges.
type TrafficExchangeList struct {
	Exchanges []TrafficExchange `json:"exchanges"`
}

// TrafficTraceList contains filtered summaries and the complete environment
// projection's watermarks, independent of the returned row count.
type TrafficTraceList struct {
	Traces          []TrafficTrace `json:"traces"`
	Revision        uint64         `json:"revision"`
	ThroughSequence int64          `json:"throughSequence"`
}

// TrafficClearResponse reports the live traffic window removed through an
// environment-local exchange sequence. Durable recordings are not affected.
type TrafficClearResponse struct {
	Cleared         int    `json:"cleared"`
	ThroughSequence int64  `json:"throughSequence"`
	Revision        uint64 `json:"revision"`
}

// TrafficClearPreview describes live exchanges through a reviewed watermark.
// Expected.ParentCreatedAt binds the preview to the current daemon lifetime.
type TrafficClearPreview struct {
	Count           int             `json:"count"`
	ThroughSequence int64           `json:"throughSequence"`
	Expected        ResourceVersion `json:"expected"`
	Retained        []string        `json:"retained"`
}

// RecordingList is a collection of retained traffic recordings.
type RecordingList struct {
	Recordings []Recording `json:"recordings"`
}

// RecordingExport is the portable versioned representation of a recording and
// its captured events.
type RecordingExport struct {
	SchemaVersion int               `json:"schemaVersion"`
	Project       string            `json:"project"`
	Environment   string            `json:"environment"`
	Recording     string            `json:"recording"`
	Exchanges     []TrafficExchange `json:"exchanges"`
}

// RecordingExportSnapshot identifies the retained recording snapshot being exported.
type RecordingExportSnapshot = model.RecordingExportSnapshot

// RecordingExportChunkQuery selects the next signed cursor and decoded chunk size.
type RecordingExportChunkQuery struct {
	Cursor   string
	MaxBytes int
}

// RecordingExportChunk contains lossless schema-4 JSON bytes encoded as base64.
// Complete is true only after the document suffix has been returned.
type RecordingExportChunk struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Snapshot      RecordingExportSnapshot `json:"snapshot"`
	Data          string                  `json:"data"`
	NextCursor    string                  `json:"nextCursor,omitempty"`
	Complete      bool                    `json:"complete"`
}

// FaultList is a collection of traffic fault rules.
type FaultList struct {
	Faults []FaultRule `json:"faults"`
}

// DisableFaultsResponse reports how many fault rules were disabled.
type DisableFaultsResponse struct {
	Disabled int64 `json:"disabled"`
}
