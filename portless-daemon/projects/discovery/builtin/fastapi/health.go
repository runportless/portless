package fastapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/discovery/spec"
	"github.com/runportless/portless/portless-daemon/projects/discovery/statichealth"
)

const (
	fastAPIRouterRouteRank = 350
	fastAPIAppRouteRank    = 400
)

type fastAPIHealthDetection struct {
	path       string
	evidence   *model.Evidence
	diagnostic *spec.Diagnostic
}

type pythonTokenKind uint8

const (
	pythonIdentifier pythonTokenKind = iota + 1
	pythonString
	pythonPunctuation
)

type pythonToken struct {
	kind  pythonTokenKind
	value string
}

func detectFastAPIHealth(ctx context.Context, workspace spec.Workspace, entry entrypoint) (fastAPIHealthDetection, error) {
	encoded, err := workspace.ReadFile(ctx, entry.file)
	if err != nil {
		return fastAPIHealthDetection{}, err
	}
	tokens := tokenizePython(encoded)
	routers := fastAPIRouters(tokens)
	included := includedFastAPIRouters(tokens, entry.app, routers)
	var candidates []statichealth.Candidate
	for index := 0; index+5 < len(tokens); index++ {
		if tokens[index].value != "@" || tokens[index+1].kind != pythonIdentifier || tokens[index+2].value != "." ||
			tokens[index+3].value != "get" || tokens[index+4].value != "(" || tokens[index+5].kind != pythonString {
			continue
		}
		receiver := tokens[index+1].value
		route := tokens[index+5].value
		baseRank := 0
		explanation := ""
		endpoint := ""
		switch {
		case receiver == entry.app:
			endpoint = statichealth.JoinPath(route)
			baseRank = fastAPIAppRouteRank
			explanation = "FastAPI application"
		case mapContains(included, receiver):
			endpoint = statichealth.JoinPath(included[receiver], routers[receiver], route)
			baseRank = fastAPIRouterRouteRank
			explanation = "FastAPI included router"
		default:
			continue
		}
		semantic := statichealth.SemanticRank(endpoint)
		if semantic == 0 {
			continue
		}
		candidates = append(candidates, statichealth.Candidate{
			Path: endpoint, File: entry.file, Explanation: fmt.Sprintf("%s GET readiness route %s found", explanation, endpoint), Rank: baseRank + semantic,
		})
	}
	selection := statichealth.Select(candidates)
	if len(selection.Ambiguous) > 0 {
		paths := make([]string, 0, len(selection.Ambiguous))
		for _, candidate := range selection.Ambiguous {
			paths = append(paths, candidate.Path)
		}
		return fastAPIHealthDetection{diagnostic: &spec.Diagnostic{
			Severity: spec.SeverityInfo, Code: "AMBIGUOUS_HEALTH_ENDPOINT", File: entry.file,
			Message: fmt.Sprintf("equally strong readiness routes %s were found; TCP readiness was kept", strings.Join(paths, ", ")),
		}}, nil
	}
	if selection.Candidate.Path == "" {
		return fastAPIHealthDetection{}, nil
	}
	return fastAPIHealthDetection{
		path: selection.Candidate.Path,
		evidence: &model.Evidence{
			File: selection.Candidate.File, Explanation: selection.Candidate.Explanation, Confidence: "high",
		},
	}, nil
}

func fastAPIRouters(tokens []pythonToken) map[string]string {
	result := make(map[string]string)
	for index := 0; index+3 < len(tokens); index++ {
		if tokens[index].kind != pythonIdentifier || tokens[index+1].value != "=" {
			continue
		}
		opening := index + 3
		if tokens[index+2].value != "APIRouter" {
			if index+5 >= len(tokens) || tokens[index+2].kind != pythonIdentifier || tokens[index+3].value != "." || tokens[index+4].value != "APIRouter" {
				continue
			}
			opening = index + 5
		}
		if opening >= len(tokens) || tokens[opening].value != "(" {
			continue
		}
		closing := matchingPythonPunctuation(tokens, opening, "(", ")")
		if closing < 0 {
			continue
		}
		prefix, present, static := pythonKeywordString(tokens, opening+1, closing, "prefix")
		if present && !static {
			continue
		}
		if !present {
			prefix = ""
		}
		result[tokens[index].value] = prefix
	}
	return result
}

func includedFastAPIRouters(tokens []pythonToken, app string, routers map[string]string) map[string]string {
	result := make(map[string]string)
	for index := 0; index+5 < len(tokens); index++ {
		if tokens[index].value != app || tokens[index+1].value != "." || tokens[index+2].value != "include_router" || tokens[index+3].value != "(" ||
			tokens[index+4].kind != pythonIdentifier || !mapContains(routers, tokens[index+4].value) {
			continue
		}
		closing := matchingPythonPunctuation(tokens, index+3, "(", ")")
		if closing < 0 {
			continue
		}
		prefix, present, static := pythonKeywordString(tokens, index+5, closing, "prefix")
		if present && !static {
			continue
		}
		if !present {
			prefix = "/"
		}
		result[tokens[index+4].value] = prefix
	}
	return result
}

func mapContains(values map[string]string, key string) bool {
	_, ok := values[key]
	return ok
}

func pythonKeywordString(tokens []pythonToken, start, end int, name string) (string, bool, bool) {
	for index := start; index+2 < end; index++ {
		if tokens[index].value != name || tokens[index+1].value != "=" {
			continue
		}
		if tokens[index+2].kind == pythonString {
			return tokens[index+2].value, true, true
		}
		return "", true, false
	}
	return "", false, false
}

func matchingPythonPunctuation(tokens []pythonToken, start int, opening, closing string) int {
	if start >= len(tokens) || tokens[start].value != opening {
		return -1
	}
	depth := 0
	for index := start; index < len(tokens); index++ {
		switch tokens[index].value {
		case opening:
			depth++
		case closing:
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func tokenizePython(encoded []byte) []pythonToken {
	var result []pythonToken
	for index := 0; index < len(encoded); {
		character := encoded[index]
		if character == ' ' || character == '\t' || character == '\r' || character == '\n' || character == '\f' {
			index++
			continue
		}
		if character == '#' {
			for index < len(encoded) && encoded[index] != '\n' && encoded[index] != '\r' {
				index++
			}
			continue
		}
		if character == '\'' || character == '"' {
			value, next, ok := pythonStringValue(encoded, index)
			if ok {
				result = append(result, pythonToken{kind: pythonString, value: value})
			}
			index = next
			continue
		}
		if isPythonIdentifierStart(character) {
			start := index
			index++
			for index < len(encoded) && isPythonIdentifierPart(encoded[index]) {
				index++
			}
			result = append(result, pythonToken{kind: pythonIdentifier, value: string(encoded[start:index])})
			continue
		}
		result = append(result, pythonToken{kind: pythonPunctuation, value: string(character)})
		index++
	}
	return result
}

func pythonStringValue(encoded []byte, start int) (string, int, bool) {
	quote := encoded[start]
	triple := start+2 < len(encoded) && encoded[start+1] == quote && encoded[start+2] == quote
	index := start + 1
	if triple {
		index = start + 3
	}
	var value strings.Builder
	for index < len(encoded) {
		if triple && index+2 < len(encoded) && encoded[index] == quote && encoded[index+1] == quote && encoded[index+2] == quote {
			return value.String(), index + 3, true
		}
		if !triple && encoded[index] == quote {
			return value.String(), index + 1, true
		}
		if encoded[index] != '\\' {
			value.WriteByte(encoded[index])
			index++
			continue
		}
		if index+1 >= len(encoded) {
			return "", len(encoded), false
		}
		index++
		switch encoded[index] {
		case '\\', '/', '\'', '"':
			value.WriteByte(encoded[index])
		default:
			return "", skipPythonString(encoded, index+1, quote, triple), false
		}
		index++
	}
	return "", len(encoded), false
}

func skipPythonString(encoded []byte, start int, quote byte, triple bool) int {
	for index := start; index < len(encoded); index++ {
		if encoded[index] == '\\' {
			index++
			continue
		}
		if triple && index+2 < len(encoded) && encoded[index] == quote && encoded[index+1] == quote && encoded[index+2] == quote {
			return index + 3
		}
		if !triple && encoded[index] == quote {
			return index + 1
		}
	}
	return len(encoded)
}

func isPythonIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isPythonIdentifierPart(value byte) bool {
	return isPythonIdentifierStart(value) || value >= '0' && value <= '9'
}
