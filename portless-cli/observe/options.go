package observe

import (
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

type listOptions struct {
	limit int
}

type logsOptions struct {
	tail       bool
	limit      int
	since      time.Duration
	timestamps bool
}

type serviceActionOptions struct {
	wait    bool
	timeout time.Duration
}

type logsOutput struct {
	Project     string           `json:"project"`
	Environment string           `json:"environment"`
	Entries     []model.LogEntry `json:"entries"`
}
