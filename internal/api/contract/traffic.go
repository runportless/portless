package contract

type TrafficQuery struct {
	Protocol string
	Service  string
	Edge     string
	After    int64
	Limit    int
}

type TrafficList struct {
	Traffic []TrafficEvent `json:"traffic"`
}

type RecordingList struct {
	Recordings []Recording `json:"recordings"`
}

type RecordingExport struct {
	SchemaVersion int            `json:"schemaVersion"`
	Project       string         `json:"project"`
	Environment   string         `json:"environment"`
	Recording     string         `json:"recording"`
	Traffic       []TrafficEvent `json:"traffic"`
}

type FaultList struct {
	Faults []FaultRule `json:"faults"`
}

type DisableFaultsResponse struct {
	Disabled int64 `json:"disabled"`
}
