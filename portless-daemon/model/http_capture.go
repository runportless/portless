package model

import "time"

// HTTPCapture describes the availability and byte fidelity of a retained HTTP body.
type HTTPCapture struct {
	State         string `json:"state"`
	ObservedBytes int64  `json:"observedBytes"`
	CapturedBytes int64  `json:"capturedBytes"`
	Encoding      string `json:"encoding"`
	Exact         bool   `json:"exact"`
}

// TrafficReplay identifies an explicitly replayed HTTP root and its original exchange.
type TrafficReplay struct {
	Project     string    `json:"project"`
	Environment string    `json:"environment"`
	Sequence    int64     `json:"sequence"`
	StartedAt   time.Time `json:"startedAt"`
	Workspace   int64     `json:"workspace"`
	Run         int64     `json:"run"`
}
