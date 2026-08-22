package environment

import (
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

type upOptions struct {
	name    string
	timeout time.Duration
	open    bool
	wait    bool
	debug   string
	managed bool
}

type downOptions struct {
	all     bool
	volumes bool
	yes     bool
	wait    bool
	timeout time.Duration
}

type upOutput struct {
	Environment model.Environment `json:"environment"`
	Operation   model.Operation   `json:"operation"`
	Warnings    []string          `json:"warnings"`
}
