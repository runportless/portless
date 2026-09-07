package portlessmcp

import "github.com/runportless/portless/portless-daemon/api/contract"

// This input excludes daemon-assigned creation and modification timestamps.
type mockRouteDraft struct {
	Name    string                               `json:"name"`
	Service string                               `json:"service"`
	Method  string                               `json:"method"`
	Path    string                               `json:"path"`
	Query   map[string]contract.MockQueryMatcher `json:"query,omitempty"`
	Status  int                                  `json:"status"`
	Headers map[string]string                    `json:"headers,omitempty"`
	Body    string                               `json:"body,omitempty"`
	DelayMS int64                                `json:"delayMs,omitempty"`
	Enabled bool                                 `json:"enabled"`
}

func (draft *mockRouteDraft) contract() *contract.MockRoute {
	if draft == nil {
		return nil
	}
	return &contract.MockRoute{Name: draft.Name, Service: draft.Service, Method: draft.Method, Path: draft.Path, Query: draft.Query, Status: draft.Status, Headers: draft.Headers, Body: draft.Body, DelayMS: draft.DelayMS, Enabled: draft.Enabled}
}
