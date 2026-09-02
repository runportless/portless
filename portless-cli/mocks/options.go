package mocks

type routeOptions struct {
	service  string
	method   string
	path     string
	query    []string
	status   int
	header   []string
	body     string
	bodyFile string
	delay    int64
	disabled bool
}

type previewOptions struct {
	service  string
	method   string
	path     string
	query    []string
	header   []string
	body     string
	bodyFile string
}
