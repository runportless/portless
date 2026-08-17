package contract

// TrafficQuery filters captured traffic by protocol, service, edge, sequence,
// and result limit.
type TrafficQuery struct {
	Protocol string
	Service  string
	Edge     string
	After    int64
	Limit    int
}

// TrafficList is a collection of captured traffic events.
type TrafficList struct {
	Traffic []TrafficEvent `json:"traffic"`
}

// RecordingList is a collection of retained traffic recordings.
type RecordingList struct {
	Recordings []Recording `json:"recordings"`
}

// RecordingExport is the portable versioned representation of a recording and
// its captured events.
type RecordingExport struct {
	SchemaVersion int            `json:"schemaVersion"`
	Project       string         `json:"project"`
	Environment   string         `json:"environment"`
	Recording     string         `json:"recording"`
	Traffic       []TrafficEvent `json:"traffic"`
}

// FaultList is a collection of traffic fault rules.
type FaultList struct {
	Faults []FaultRule `json:"faults"`
}

// DisableFaultsResponse reports how many fault rules were disabled.
type DisableFaultsResponse struct {
	Disabled int64 `json:"disabled"`
}
