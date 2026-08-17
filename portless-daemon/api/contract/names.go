package contract

import "github.com/portless-run/portless/portless-daemon/model"

// ParseEnvironmentSelector validates and separates a public
// project/environment selector.
func ParseEnvironmentSelector(selector string) (string, string, error) {
	return model.ParseEnvironmentSelector(selector)
}

// ValidateServiceName checks whether name is a public, non-reserved service
// label.
func ValidateServiceName(name string) error {
	return model.ValidateServiceName(name)
}

// ValidateArtifactName checks whether name is a public recording or fault
// slug.
func ValidateArtifactName(name string) error {
	return model.ValidateArtifactName(name)
}
