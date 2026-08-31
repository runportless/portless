// Package statichealth selects readiness endpoints proven by bounded static
// discovery. Framework detectors remain responsible for producing evidence;
// this package only normalizes paths and resolves competing candidates.
package statichealth

import (
	"path"
	"sort"
	"strings"
	"unicode"
)

// Candidate is one statically proven HTTP readiness endpoint.
type Candidate struct {
	Path        string
	File        string
	Explanation string
	Rank        int
}

// Selection is the deterministic result of comparing endpoint candidates.
// Ambiguous contains the equally ranked, distinct paths that prevented a
// selection.
type Selection struct {
	Candidate Candidate
	Ambiguous []Candidate
}

// Select chooses the highest-ranked endpoint. Equal-ranked evidence for
// different paths is intentionally ambiguous; duplicate evidence for one path
// is collapsed deterministically.
func Select(candidates []Candidate) Selection {
	normalized := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Path = CleanPath(candidate.Path)
		if candidate.Path == "" || candidate.File == "" || candidate.Explanation == "" || candidate.Rank <= 0 {
			continue
		}
		normalized = append(normalized, candidate)
	}
	if len(normalized) == 0 {
		return Selection{}
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Rank != normalized[j].Rank {
			return normalized[i].Rank > normalized[j].Rank
		}
		if normalized[i].Path != normalized[j].Path {
			return normalized[i].Path < normalized[j].Path
		}
		if normalized[i].File != normalized[j].File {
			return normalized[i].File < normalized[j].File
		}
		return normalized[i].Explanation < normalized[j].Explanation
	})
	topRank := normalized[0].Rank
	byPath := make(map[string]Candidate)
	for _, candidate := range normalized {
		if candidate.Rank != topRank {
			break
		}
		if _, exists := byPath[candidate.Path]; !exists {
			byPath[candidate.Path] = candidate
		}
	}
	if len(byPath) == 1 {
		return Selection{Candidate: normalized[0]}
	}
	paths := make([]string, 0, len(byPath))
	for candidatePath := range byPath {
		paths = append(paths, candidatePath)
	}
	sort.Strings(paths)
	result := Selection{Ambiguous: make([]Candidate, 0, len(paths))}
	for _, candidatePath := range paths {
		result.Ambiguous = append(result.Ambiguous, byPath[candidatePath])
	}
	return result
}

// SemanticRank classifies a literal route as a readiness endpoint. Readiness
// names outrank generic health names; liveness and unrelated status routes are
// not selected.
func SemanticRank(value string) int {
	cleaned := CleanPath(value)
	if cleaned == "" {
		return 0
	}
	component := strings.ToLower(path.Base(cleaned))
	switch component {
	case "ready", "readyz", "readiness":
		return 20
	case "health", "healthz", "healthcheck", "health-check":
		return 10
	default:
		return 0
	}
}

// JoinPath combines static route prefixes and returns a normalized absolute
// HTTP path. Empty components are allowed; dynamic or malformed components
// cause the result to be rejected.
func JoinPath(components ...string) string {
	joined := ""
	for _, component := range components {
		if component == "" || component == "/" {
			continue
		}
		if hasDynamicSyntax(component) {
			return ""
		}
		joined += "/" + strings.Trim(component, "/")
	}
	if joined == "" {
		joined = "/"
	}
	return CleanPath(joined)
}

// CleanPath validates and normalizes one literal endpoint path. Query strings,
// fragments, traversal, controls, backslashes, and dynamic route syntax are
// deliberately rejected.
func CleanPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#\\") || hasDynamicSyntax(value) {
		return ""
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return ""
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == "/" || strings.Contains(value, "//") {
		return ""
	}
	if strings.Contains(value, "/../") || strings.HasSuffix(value, "/..") || strings.Contains(value, "/./") || strings.HasSuffix(value, "/.") {
		return ""
	}
	return cleaned
}

func hasDynamicSyntax(value string) bool {
	return strings.ContainsAny(value, "{}*:$%") || strings.Contains(value, "${") || strings.Contains(value, "[") || strings.Contains(value, "]")
}
