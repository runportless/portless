package contract

import "github.com/runportless/portless/portless-daemon/model"

// ValidateProjectName checks a public logical project name.
func ValidateProjectName(name string) error { return model.ValidateProjectName(name) }

// ValidateSourceName checks a public logical source name.
func ValidateSourceName(name string) error { return model.ValidateSourceName(name) }

// ValidateEnvironmentName checks a public environment name without its project.
func ValidateEnvironmentName(name string) error { return model.ValidateEnvironmentName(name) }

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
