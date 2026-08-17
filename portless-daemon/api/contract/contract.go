// Package contract owns the versioned wire types shared by Portless API
// clients and the daemon HTTP server.
package contract

const APIVersion = "7.0.0"

type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Subject     map[string]any `json:"subject,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	Remediation []Remediation  `json:"remediation,omitempty"`
}

type Remediation struct {
	Label   string `json:"label"`
	Command string `json:"command,omitempty"`
	URL     string `json:"url,omitempty"`
}
