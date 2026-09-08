package contract

import "time"

// TrafficReplayMaxBodyBytes bounds the UTF-8 bytes in an edited replay request body.
const TrafficReplayMaxBodyBytes = 25 << 20

// TrafficReplayIdentity prevents stale requests from addressing reused workspace numbers.
type TrafficReplayIdentity struct {
	CreatedAt       time.Time `json:"createdAt"`
	DaemonStartedAt time.Time `json:"daemonStartedAt"`
}

// PrepareTrafficReplayRequest freezes one specifically identified live exchange.
type PrepareTrafficReplayRequest struct {
	Sequence  int64     `json:"sequence"`
	StartedAt time.Time `json:"startedAt"`
}

// TrafficReplayDraft describes an edited request to the original logical service edge.
type TrafficReplayDraft struct {
	Environment    string              `json:"environment"`
	Method         string              `json:"method"`
	RequestTarget  string              `json:"requestTarget"`
	Headers        map[string][]string `json:"headers"`
	BodyMode       string              `json:"bodyMode"`
	Body           string              `json:"body"`
	OmittedHeaders []string            `json:"omittedHeaders,omitempty"`
}

// UpdateTrafficReplayDraftRequest prepares a complete request with optimistic concurrency.
type UpdateTrafficReplayDraftRequest struct {
	TrafficReplayIdentity
	Revision uint64             `json:"revision"`
	Draft    TrafficReplayDraft `json:"draft"`
}

// RunTrafficReplayRequest admits one reviewed request exactly once within its workspace.
type RunTrafficReplayRequest struct {
	TrafficReplayIdentity
	Revision           uint64 `json:"revision"`
	RunNumber          int64  `json:"runNumber"`
	ConfirmRemoteWrite bool   `json:"confirmRemoteWrite"`
}

// TrafficReplayDestination exposes the reviewed public endpoint and current provider policy.
type TrafficReplayDestination struct {
	Environment          string `json:"environment"`
	Provider             string `json:"provider"`
	MockScenario         string `json:"mockScenario,omitempty"`
	MockRoute            string `json:"mockRoute,omitempty"`
	Classification       string `json:"classification,omitempty"`
	WritePolicy          string `json:"writePolicy,omitempty"`
	URL                  string `json:"url"`
	RequiresConfirmation bool   `json:"requiresConfirmation"`
}

// TrafficReplayLimitation describes an omitted input or incomplete observation without its contents.
type TrafficReplayLimitation struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// TrafficReplayRun is a compact admission and outcome receipt independent of retained result bodies.
type TrafficReplayRun struct {
	Number      int64     `json:"number"`
	Revision    uint64    `json:"revision"`
	State       string    `json:"state"`
	Outcome     string    `json:"outcome"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt,omitzero"`
	Deadline    time.Time `json:"deadline"`
	Error       string    `json:"error,omitempty"`
}

// TrafficReplayChange preserves original scalar spellings in a bounded structural difference.
type TrafficReplayChange struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// TrafficComparisonSection distinguishes exact equality from partial or unavailable evidence.
type TrafficComparisonSection struct {
	State   string                `json:"state"`
	Format  string                `json:"format,omitempty"`
	Reason  string                `json:"reason,omitempty"`
	Changes []TrafficReplayChange `json:"changes,omitempty"`
}

// TrafficResponseComparison compares bounded redacted observations of two responses.
type TrafficResponseComparison struct {
	State           string                   `json:"state"`
	OriginalStatus  int                      `json:"originalStatus"`
	ReplayStatus    int                      `json:"replayStatus"`
	StatusChanged   bool                     `json:"statusChanged"`
	DurationDeltaMS int64                    `json:"durationDeltaMs"`
	Headers         TrafficComparisonSection `json:"headers"`
	Body            TrafficComparisonSection `json:"body"`
}

// TrafficReplayResult retains the last submitted safe request and its observed comparison.
type TrafficReplayResult struct {
	RunNumber   int64                     `json:"runNumber"`
	Request     TrafficReplayDraft        `json:"request"`
	Destination TrafficReplayDestination  `json:"destination"`
	Exchange    *TrafficExchange          `json:"exchange,omitempty"`
	Comparison  TrafficResponseComparison `json:"comparison"`
	Limitations []TrafficReplayLimitation `json:"limitations,omitempty"`
}

// TrafficReplayWorkspace freezes a live baseline and bounds repeated explicit requests.
type TrafficReplayWorkspace struct {
	TrafficReplayIdentity
	Project       string                    `json:"project"`
	Environment   string                    `json:"environment"`
	Number        int64                     `json:"number"`
	PreparedUntil time.Time                 `json:"preparedUntil,omitzero"`
	Revision      uint64                    `json:"revision"`
	NextRunNumber int64                     `json:"nextRunNumber"`
	Baseline      *TrafficExchange          `json:"baseline,omitempty"`
	Draft         *TrafficReplayDraft       `json:"draft,omitempty"`
	Destination   *TrafficReplayDestination `json:"destination,omitempty"`
	Limitations   []TrafficReplayLimitation `json:"limitations,omitempty"`
	Run           *TrafficReplayRun         `json:"run,omitempty"`
	Receipts      []TrafficReplayRun        `json:"receipts,omitempty"`
	Result        *TrafficReplayResult      `json:"result,omitempty"`
}

// TrafficReplayStatus returns admission metadata without copying captured or edited payloads.
type TrafficReplayStatus struct {
	TrafficReplayIdentity
	Project       string             `json:"project"`
	Environment   string             `json:"environment"`
	Number        int64              `json:"number"`
	Revision      uint64             `json:"revision"`
	NextRunNumber int64              `json:"nextRunNumber"`
	Closed        bool               `json:"closed"`
	Destinations  []string           `json:"destinations"`
	Run           *TrafficReplayRun  `json:"run,omitempty"`
	Receipts      []TrafficReplayRun `json:"receipts"`
}
