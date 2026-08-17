// Package contract owns the versioned wire types shared by Portless API
// clients and the daemon HTTP server.
package contract

// APIVersion is the semantic version of the daemon HTTP contract.
const APIVersion = "7.0.0"

// ErrorEnvelope is the stable top-level JSON shape for API failures.
type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

// APIError describes a structured daemon API failure and possible remediation.
type APIError struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Subject     map[string]any `json:"subject,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	Remediation []Remediation  `json:"remediation,omitempty"`
}

// Remediation describes one command or URL that may resolve an API failure.
type Remediation struct {
	Label   string `json:"label"`
	Command string `json:"command,omitempty"`
	URL     string `json:"url,omitempty"`
}
