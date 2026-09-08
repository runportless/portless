package contract

// SetMockScenarioPolicyRequest selects full or partial mocking while preserving
// the scenario's routes and enabled state.
type SetMockScenarioPolicyRequest struct {
	UnmatchedRequests MockUnmatchedRequests `json:"unmatchedRequests"`
}

// PreviewMockRequest evaluates a sample request against saved routes with an
// optional in-memory route draft. OriginalRoute identifies a saved route to
// replace, including when Draft has a new name; omitting it appends Draft as a
// new route for this preview only.
type PreviewMockRequest struct {
	Request       MockRequest `json:"request"`
	Draft         *MockRoute  `json:"draft,omitempty"`
	OriginalRoute string      `json:"originalRoute,omitempty"`
}
