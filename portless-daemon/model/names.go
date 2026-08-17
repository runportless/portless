package model

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

var (
	dnsNamePattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	artifactNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

var reservedServiceNames = map[string]struct{}{
	"external": {},
}

// NormalizeDNSName converts arbitrary input into a valid lowercase DNS label.
func NormalizeDNSName(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))
	var result strings.Builder
	lastDash := false
	for _, r := range input {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if r > unicode.MaxASCII {
				if !lastDash && result.Len() > 0 {
					result.WriteByte('-')
					lastDash = true
				}
				continue
			}
			result.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && result.Len() > 0 {
			result.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(result.String(), "-")
	if len(value) > 63 {
		value = strings.TrimRight(value[:63], "-")
	}
	if value != "" && value[0] >= '0' && value[0] <= '9' {
		value = "project-" + value
		if len(value) > 63 {
			value = strings.TrimRight(value[:63], "-")
		}
	}
	return value
}

// ValidateProjectName checks whether name is a permitted project DNS label.
func ValidateProjectName(name string) error {
	if !dnsNamePattern.MatchString(name) {
		return errors.New("project name must be a lowercase DNS label beginning with a letter")
	}
	if name == "portless" || name == "localhost" {
		return errors.New("project name is reserved")
	}
	return nil
}

// ValidateEnvironmentName checks whether name is a permitted environment DNS label.
func ValidateEnvironmentName(name string) error {
	if !dnsNamePattern.MatchString(name) {
		return errors.New("environment name must be a lowercase DNS label beginning with a letter")
	}
	if name == "portless" || name == "localhost" {
		return errors.New("environment name is reserved")
	}
	return nil
}

// ValidateSourceName checks whether name is a valid source identifier.
func ValidateSourceName(name string) error {
	if !dnsNamePattern.MatchString(name) {
		return errors.New("source name must be a lowercase DNS label beginning with a letter")
	}
	return nil
}

// EnvironmentSelector returns the canonical project/environment selector.
func EnvironmentSelector(project, environment string) string {
	return project + "/" + environment
}

// ParseEnvironmentSelector validates and separates a project/environment selector.
func ParseEnvironmentSelector(selector string) (string, string, error) {
	project, environment, found := strings.Cut(selector, "/")
	if !found || strings.Contains(environment, "/") {
		return "", "", errors.New("environment must be addressed as project/environment")
	}
	if err := ValidateProjectName(project); err != nil {
		return "", "", err
	}
	if err := ValidateEnvironmentName(environment); err != nil {
		return "", "", err
	}
	return project, environment, nil
}

// ValidateServiceName checks whether name is a valid, non-reserved service label.
func ValidateServiceName(name string) error {
	if !dnsNamePattern.MatchString(name) {
		return errors.New("service name must be a lowercase DNS label beginning with a letter")
	}
	if _, reserved := reservedServiceNames[name]; reserved {
		return errors.New("service name is reserved")
	}
	return nil
}

// ValidateArtifactName checks whether name is a safe recording or fault slug.
func ValidateArtifactName(name string) error {
	if !artifactNamePattern.MatchString(name) {
		return errors.New("name must be a lowercase URL-safe slug")
	}
	return nil
}
