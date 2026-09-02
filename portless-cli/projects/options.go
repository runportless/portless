package projects

import "github.com/runportless/portless/portless-daemon/model"

type listOptions struct {
	limit int
}

type bindingOptions struct {
	provider       model.ProviderKind
	source         string
	remoteURL      string
	classification model.RemoteClassification
	writePolicy    model.WritePolicy
	healthPath     string
}
