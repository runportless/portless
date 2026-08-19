package mocks

type routeOptions struct {
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
	method   string
	path     string
	query    []string
	header   []string
	body     string
	bodyFile string
}
