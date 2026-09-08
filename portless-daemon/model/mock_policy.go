package model

import (
	"encoding/json"
	"fmt"
)

// MockUnmatchedRequests selects how a scenario handles an unmatched HTTP request.
type MockUnmatchedRequests string

const (
	// MockUnmatchedReject returns a fixed 501 when no route matches.
	MockUnmatchedReject MockUnmatchedRequests = "reject"
	// MockUnmatchedForward keeps the real provider and forwards unmatched requests.
	MockUnmatchedForward MockUnmatchedRequests = "forward"
)

// Validate rejects policies other than reject and forward.
func (p MockUnmatchedRequests) Validate() error {
	if p != MockUnmatchedReject && p != MockUnmatchedForward {
		return fmt.Errorf("unmatchedRequests must be reject or forward")
	}
	return nil
}

// UnmarshalJSON validates explicitly supplied policies, including empty and null.
func (p *MockUnmatchedRequests) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	policy := MockUnmatchedRequests(value)
	if err := policy.Validate(); err != nil {
		return err
	}
	*p = policy
	return nil
}
