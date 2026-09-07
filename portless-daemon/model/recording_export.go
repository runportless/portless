package model

import "time"

// RecordingExportSnapshot fixes a recording identity and retained-event watermark.
type RecordingExportSnapshot struct {
	Project              string    `json:"project"`
	Environment          string    `json:"environment"`
	Recording            string    `json:"recording"`
	RecordingStartedAt   time.Time `json:"recordingStartedAt"`
	EnvironmentCreatedAt time.Time `json:"environmentCreatedAt"`
	ThroughSequence      int64     `json:"throughSequence"`
	EventCount           int64     `json:"eventCount"`
}

// RecordingExportEvent is one already-redacted persisted exchange, read by indexed sequence.
type RecordingExportEvent struct {
	Sequence int64
	JSON     []byte
}
