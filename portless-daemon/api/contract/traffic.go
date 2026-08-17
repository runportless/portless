package contract

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

// TrafficTraceList is a collection of trace summaries.
type TrafficTraceList struct {
	Traces []TrafficTrace `json:"traces"`
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

// FaultList is a collection of traffic fault rules.
type FaultList struct {
	Faults []FaultRule `json:"faults"`
}

// DisableFaultsResponse reports how many fault rules were disabled.
type DisableFaultsResponse struct {
	Disabled int64 `json:"disabled"`
}
