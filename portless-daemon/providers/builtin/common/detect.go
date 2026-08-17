// Package common contains source-scanning helpers shared by built-in resource
// plugins. It has no runtime behavior.
package common

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/providers"
)

type Detection struct {
	Name                string
	Explanation         string
	Markers             []string
	DefaultEnvironment  func(providers.Consumer) string
	ExplicitEnvironment func(string, providers.Consumer) string
}

func Detect(ctx context.Context, workspace providers.Workspace, consumers []providers.Consumer, config Detection) (providers.Findings, error) {
	var evidence []model.Evidence
	type detectedBinding struct {
		claim    providers.BindingClaim
		explicit bool
	}
	bindings := make(map[string]detectedBinding)
	for _, file := range workspace.Files() {
		if err := ctx.Err(); err != nil {
			return providers.Findings{}, err
		}
		if !TopologyFile(file) {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return providers.Findings{}, err
		}
		content := string(encoded)
		lower := strings.ToLower(content)
		if !ContainsMarker(lower, config.Markers) {
			continue
		}
		evidence = append(evidence, model.Evidence{File: file, Explanation: config.Explanation, Confidence: "high"})
		consumer := OwningConsumer(file, consumers)
		if consumer == nil {
			continue
		}
		environment := ""
		explicit := false
		if config.ExplicitEnvironment != nil {
			environment = config.ExplicitEnvironment(content, *consumer)
			explicit = environment != ""
		}
		if environment == "" && config.DefaultEnvironment != nil {
			environment = config.DefaultEnvironment(*consumer)
		}
		if environment == "" {
			continue
		}
		claim := providers.BindingClaim{ConsumerKey: consumer.Key, Environment: environment, Required: true}
		if existing, exists := bindings[consumer.Key]; exists {
			if existing.claim.Environment == environment {
				continue
			}
			if existing.explicit && explicit || !existing.explicit && !explicit {
				return providers.Findings{}, fmt.Errorf("resource configuration for %s uses conflicting environment variables %s and %s", consumer.Name, existing.claim.Environment, environment)
			}
			if existing.explicit {
				continue
			}
		}
		bindings[consumer.Key] = detectedBinding{claim: claim, explicit: explicit}
	}
	if len(evidence) == 0 {
		return providers.Findings{}, nil
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].File < evidence[j].File })
	claims := make([]providers.BindingClaim, 0, len(bindings))
	for _, binding := range bindings {
		claims = append(claims, binding.claim)
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].ConsumerKey < claims[j].ConsumerKey })
	return providers.Findings{Candidates: []providers.Candidate{{Key: ".", Name: config.Name, Evidence: evidence, Bindings: claims}}}, nil
}

func TopologyFile(file string) bool {
	base := path.Base(file)
	switch base {
	case "build.gradle", "build.gradle.kts", "go.mod", "package.json", "pom.xml", "pyproject.toml":
		return true
	}
	if strings.HasPrefix(base, "application") && (strings.HasSuffix(base, ".properties") || strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml")) {
		return true
	}
	if strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
		return true
	}
	// Actual .env files are intentionally excluded. Templates are useful static
	// evidence and should not contain developer credentials.
	return strings.HasSuffix(base, ".example") || strings.HasSuffix(base, ".sample") || strings.HasSuffix(base, ".template")
}

func ContainsMarker(content string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(content, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func FirstEnvironment(content string, names ...string) string {
	upper := strings.ToUpper(content)
	for _, name := range names {
		if strings.Contains(upper, name) {
			return name
		}
	}
	return ""
}

func OwningConsumer(file string, consumers []providers.Consumer) *providers.Consumer {
	directory := path.Dir(file)
	best := -1
	var owner *providers.Consumer
	for index := range consumers {
		consumerDirectory := consumers[index].Directory
		owned := consumerDirectory == "." || directory == consumerDirectory || strings.HasPrefix(directory, consumerDirectory+"/")
		if owned && len(consumerDirectory) > best {
			best = len(consumerDirectory)
			owner = &consumers[index]
		}
	}
	if owner == nil && len(consumers) == 1 {
		return &consumers[0]
	}
	return owner
}

func FrameworkEnvironment(consumer providers.Consumer, spring, fallback string) string {
	if consumer.Framework == "spring-boot" {
		return spring
	}
	return fallback
}
