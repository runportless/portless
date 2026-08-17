package traffic

import "time"

type listOptions struct {
	limit int
}

type trafficOptions struct {
	tail     bool
	protocol string
	limit    int
	service  string
	edge     string
}

type traceOptions struct {
	limit             int
	service           string
	edge              string
	includeBackground bool
}

type exportOptions struct {
	output string
	force  bool
}

type recordingOptions struct {
	edge      string
	duration  time.Duration
	maxEvents int64
}

type faultOptions struct {
	latency     int64
	jitter      int64
	status      int
	abort       bool
	probability float64
	method      string
	path        string
	duration    time.Duration
}
