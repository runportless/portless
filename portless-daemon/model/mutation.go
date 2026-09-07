package model

import "time"

// ResourceVersion identifies a reviewed resource and, when needed, its containing environment.
// Creation timestamps prevent a reused public name from matching an earlier preview.
type ResourceVersion struct {
	StateDigest     string    `json:"stateDigest,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	ModifiedAt      time.Time `json:"modifiedAt,omitzero"`
	Revision        int64     `json:"revision,omitempty"`
	ParentCreatedAt time.Time `json:"parentCreatedAt,omitzero"`
}

// MockDeletionPreview describes the exact routes affected and why deletion may be blocked.
type MockDeletionPreview struct {
	Scenario MockScenarioMetadata `json:"scenario"`
	Route    string               `json:"route,omitempty"`
	Routes   []string             `json:"routes"`
	Expected ResourceVersion      `json:"expected"`
	Blocked  []string             `json:"blocked"`
	Retained []string             `json:"retained"`
}

// RecordingDeletionPreview reports the retained events and identity to be removed.
type RecordingDeletionPreview struct {
	Recording Recording       `json:"recording"`
	Expected  ResourceVersion `json:"expected"`
	Blocked   []string        `json:"blocked"`
	Retained  []string        `json:"retained"`
}

// FaultDeletionPreview reports the rule and identity to be removed.
type FaultDeletionPreview struct {
	Fault    FaultRule       `json:"fault"`
	Expected ResourceVersion `json:"expected"`
	Blocked  []string        `json:"blocked"`
	Retained []string        `json:"retained"`
}
