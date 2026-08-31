package node

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/discovery/spec"
	"github.com/runportless/portless/portless-daemon/projects/discovery/statichealth"
)

const (
	nodeRawRouteRank       = 300
	nodeRegisteredGetRank  = 350
	nestControllerRank     = 400
	nextFilesystemRank     = 450
	nestHealthCheckRank    = 500
	javascriptTokenLookout = 48
)

type nodeHealthDetection struct {
	path       string
	evidence   *model.Evidence
	diagnostic *spec.Diagnostic
}

type javascriptTokenKind uint8

const (
	javascriptIdentifier javascriptTokenKind = iota + 1
	javascriptString
	javascriptPunctuation
)

type javascriptToken struct {
	kind  javascriptTokenKind
	value string
}

type nodeSource struct {
	file   string
	tokens []javascriptToken
}

func detectNodeHealth(ctx context.Context, workspace spec.Workspace, packages []packageManifest, current packageManifest, framework string) (nodeHealthDetection, error) {
	var sources []nodeSource
	for _, file := range workspace.Files() {
		if !nodeSourceFile(file) || !ownedByPackage(file, current.directory, packages) {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return nodeHealthDetection{}, err
		}
		sources = append(sources, nodeSource{file: file, tokens: tokenizeJavaScript(encoded)})
	}
	if len(sources) == 0 {
		return nodeHealthDetection{}, nil
	}

	globalPrefix := ""
	nestPrefixKnown := true
	if framework == "nestjs" {
		prefixes := make(map[string]struct{})
		for _, source := range sources {
			values, dynamic := staticMethodStringArguments(source.tokens, "setGlobalPrefix")
			nestPrefixKnown = nestPrefixKnown && !dynamic
			for _, prefix := range values {
				cleaned := statichealth.JoinPath(prefix)
				if cleaned != "" {
					prefixes[cleaned] = struct{}{}
				}
			}
		}
		if len(prefixes) > 1 {
			nestPrefixKnown = false
		} else if len(prefixes) == 1 {
			for prefix := range prefixes {
				globalPrefix = prefix
			}
		}
	}

	var candidates []statichealth.Candidate
	nestRoutesSuppressed := false
	rawRuntimeProven := false
	for _, source := range sources {
		if containsMethodCall(source.tokens, "listen") && containsNodePortEnvironment(source.tokens) {
			rawRuntimeProven = true
			break
		}
	}
	for _, source := range sources {
		if framework == "nestjs" {
			nestCandidates := nestHealthCandidates(source, globalPrefix)
			if nestPrefixKnown {
				candidates = append(candidates, nestCandidates...)
			} else if len(nestCandidates) > 0 {
				nestRoutesSuppressed = true
			}
		}
		if framework == "nextjs" {
			if candidate, ok := nextHealthCandidate(current.directory, source); ok {
				candidates = append(candidates, candidate)
			}
		}
		candidates = append(candidates, registeredGetHealthCandidates(source)...)
		candidates = append(candidates, rawNodeHealthCandidates(source, rawRuntimeProven)...)
	}

	selection := statichealth.Select(candidates)
	if len(selection.Ambiguous) > 0 {
		paths := make([]string, 0, len(selection.Ambiguous))
		for _, candidate := range selection.Ambiguous {
			paths = append(paths, candidate.Path)
		}
		return nodeHealthDetection{diagnostic: &spec.Diagnostic{
			Severity: spec.SeverityInfo, Code: "AMBIGUOUS_HEALTH_ENDPOINT", File: selection.Ambiguous[0].File,
			Message: fmt.Sprintf("equally strong readiness routes %s were found; TCP readiness was kept", strings.Join(paths, ", ")),
		}}, nil
	}
	if selection.Candidate.Path == "" {
		if nestRoutesSuppressed {
			return nodeHealthDetection{diagnostic: &spec.Diagnostic{
				Severity: spec.SeverityInfo, Code: "DYNAMIC_HEALTH_ENDPOINT", File: current.file,
				Message: "NestJS readiness routes use an ambiguous or dynamic global prefix; TCP readiness was kept",
			}}, nil
		}
		return nodeHealthDetection{}, nil
	}
	return nodeHealthDetection{
		path: selection.Candidate.Path,
		evidence: &model.Evidence{
			File: selection.Candidate.File, Explanation: selection.Candidate.Explanation, Confidence: "high",
		},
	}, nil
}

func nodeSourceFile(file string) bool {
	base := strings.ToLower(path.Base(file))
	if strings.HasSuffix(base, ".d.ts") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.Contains(base, ".min.") {
		return false
	}
	extension := strings.ToLower(path.Ext(base))
	switch extension {
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx":
	default:
		return false
	}
	for _, component := range strings.Split(strings.ToLower(file), "/") {
		switch component {
		case "test", "tests", "__test__", "__tests__", "fixtures", "generated":
			return false
		}
	}
	return true
}

func ownedByPackage(file, directory string, packages []packageManifest) bool {
	fileDirectory := path.Dir(file)
	owner := ""
	for _, candidate := range packages {
		if !directoryContains(candidate.directory, fileDirectory) {
			continue
		}
		if owner == "" || len(candidate.directory) > len(owner) {
			owner = candidate.directory
		}
	}
	return owner == directory
}

func directoryContains(directory, target string) bool {
	return directory == "." || target == directory || strings.HasPrefix(target, directory+"/")
}

func nestHealthCandidates(source nodeSource, globalPrefix string) []statichealth.Candidate {
	var result []statichealth.Candidate
	tokens := source.tokens
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index].value != "@" || tokens[index+1].value != "Controller" {
			continue
		}
		controllerPrefix, afterDecorator, ok := decoratorStringArgument(tokens, index+1)
		if !ok {
			continue
		}
		classIndex := findToken(tokens, afterDecorator, "class", javascriptTokenLookout)
		if classIndex < 0 {
			continue
		}
		bodyStart := findToken(tokens, classIndex+1, "{", javascriptTokenLookout)
		if bodyStart < 0 {
			continue
		}
		bodyEnd := matchingPunctuation(tokens, bodyStart, "{", "}")
		if bodyEnd < 0 {
			bodyEnd = len(tokens)
		}
		for routeIndex := bodyStart + 1; routeIndex+1 < bodyEnd; routeIndex++ {
			if tokens[routeIndex].value != "@" || tokens[routeIndex+1].value != "Get" {
				continue
			}
			route, afterRoute, routeOK := decoratorStringArgument(tokens, routeIndex+1)
			if !routeOK {
				continue
			}
			endpoint := statichealth.JoinPath(globalPrefix, controllerPrefix, route)
			semantic := statichealth.SemanticRank(endpoint)
			healthCheck := decoratorNearby(tokens, routeIndex, afterRoute, bodyStart, bodyEnd, "HealthCheck")
			if semantic == 0 && !healthCheck {
				continue
			}
			rank := nestControllerRank + semantic
			explanation := fmt.Sprintf("NestJS GET readiness route %s found", endpoint)
			if healthCheck {
				rank = nestHealthCheckRank + semantic
				explanation = fmt.Sprintf("NestJS @HealthCheck GET route %s found", endpoint)
			}
			result = append(result, statichealth.Candidate{Path: endpoint, File: source.file, Explanation: explanation, Rank: rank})
		}
		index = bodyEnd
	}
	return result
}

func decoratorStringArgument(tokens []javascriptToken, nameIndex int) (string, int, bool) {
	if nameIndex+1 >= len(tokens) || tokens[nameIndex+1].value != "(" {
		return "", nameIndex + 1, false
	}
	closing := matchingPunctuation(tokens, nameIndex+1, "(", ")")
	if closing < 0 {
		return "", nameIndex + 1, false
	}
	if closing == nameIndex+2 {
		return "", closing + 1, true
	}
	if closing == nameIndex+3 && tokens[nameIndex+2].kind == javascriptString {
		return tokens[nameIndex+2].value, closing + 1, true
	}
	return "", closing + 1, false
}

func decoratorNearby(tokens []javascriptToken, routeStart, routeEnd, bodyStart, bodyEnd int, name string) bool {
	lower := routeStart - 1
	minimum := routeStart - javascriptTokenLookout
	if minimum < bodyStart {
		minimum = bodyStart
	}
	for index := lower; index >= minimum; index-- {
		if tokens[index].value == "{" || tokens[index].value == "}" || tokens[index].value == ";" {
			break
		}
		if index+1 < len(tokens) && tokens[index].value == "@" && tokens[index+1].value == name {
			return true
		}
	}
	upper := routeEnd + javascriptTokenLookout
	if upper > bodyEnd {
		upper = bodyEnd
	}
	for index := routeEnd; index+1 < upper; index++ {
		if tokens[index].value == "{" || tokens[index].value == ";" {
			break
		}
		if tokens[index].value == "@" && tokens[index+1].value == name {
			return true
		}
	}
	return false
}

func nextHealthCandidate(packageDirectory string, source nodeSource) (statichealth.Candidate, bool) {
	relative := source.file
	if packageDirectory != "." {
		relative = strings.TrimPrefix(relative, packageDirectory+"/")
	}
	components := strings.Split(relative, "/")
	appIndex := componentIndex(components, "app")
	if appIndex >= 0 && strings.HasPrefix(path.Base(source.file), "route.") && exportedGET(source.tokens) {
		routeComponents := components[appIndex+1 : len(components)-1]
		filtered := routeComponents[:0]
		for _, component := range routeComponents {
			if strings.HasPrefix(component, "(") && strings.HasSuffix(component, ")") {
				continue
			}
			if strings.HasPrefix(component, "@") || strings.ContainsAny(component, "[]") {
				return statichealth.Candidate{}, false
			}
			filtered = append(filtered, component)
		}
		endpoint := statichealth.JoinPath(filtered...)
		semantic := statichealth.SemanticRank(endpoint)
		if semantic > 0 {
			return statichealth.Candidate{
				Path: endpoint, File: source.file, Explanation: fmt.Sprintf("Next.js GET route %s found", endpoint), Rank: nextFilesystemRank + semantic,
			}, true
		}
	}

	pagesIndex := componentIndex(components, "pages")
	if pagesIndex >= 0 && pagesIndex+1 < len(components) && components[pagesIndex+1] == "api" && exportedDefault(source.tokens) && containsGETMethodCheck(source.tokens) {
		routeComponents := append([]string(nil), components[pagesIndex+1:]...)
		routeComponents[len(routeComponents)-1] = strings.TrimSuffix(routeComponents[len(routeComponents)-1], path.Ext(routeComponents[len(routeComponents)-1]))
		if routeComponents[len(routeComponents)-1] == "index" {
			routeComponents = routeComponents[:len(routeComponents)-1]
		}
		endpoint := statichealth.JoinPath(routeComponents...)
		semantic := statichealth.SemanticRank(endpoint)
		if semantic > 0 {
			return statichealth.Candidate{
				Path: endpoint, File: source.file, Explanation: fmt.Sprintf("Next.js API route %s found", endpoint), Rank: nextFilesystemRank + semantic,
			}, true
		}
	}
	return statichealth.Candidate{}, false
}

func componentIndex(components []string, wanted string) int {
	for index, component := range components {
		if component == wanted && (index == 0 || index == 1 && components[0] == "src") {
			return index
		}
	}
	return -1
}

func exportedGET(tokens []javascriptToken) bool {
	for index := 0; index < len(tokens); index++ {
		if tokens[index].value != "export" {
			continue
		}
		for lookahead := index + 1; lookahead < len(tokens) && lookahead <= index+4; lookahead++ {
			if tokens[lookahead].value == "GET" {
				return true
			}
			if tokens[lookahead].value == ";" || tokens[lookahead].value == "}" {
				break
			}
		}
	}
	return false
}

func exportedDefault(tokens []javascriptToken) bool {
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index].value == "export" && tokens[index+1].value == "default" {
			return true
		}
	}
	return false
}

func containsGETMethodCheck(tokens []javascriptToken) bool {
	for index := 0; index+4 < len(tokens); index++ {
		if tokens[index].value != "." || tokens[index+1].value != "method" {
			continue
		}
		valueIndex := index + 2
		equalityCount := 0
		for valueIndex < len(tokens) && valueIndex <= index+5 && tokens[valueIndex].value == "=" {
			equalityCount++
			valueIndex++
		}
		if equalityCount >= 2 && valueIndex < len(tokens) && tokens[valueIndex].kind == javascriptString && strings.EqualFold(tokens[valueIndex].value, "GET") {
			return true
		}
	}
	return false
}

func registeredGetHealthCandidates(source nodeSource) []statichealth.Candidate {
	servers, routers := registeredServerVariables(source.tokens)
	mounts := registeredRouterMounts(source.tokens, servers, routers)
	pluginMounts := registeredFastifyPluginMounts(source.tokens, servers)
	var result []statichealth.Candidate
	for index := 0; index+4 < len(source.tokens); index++ {
		receiver := source.tokens[index].value
		if source.tokens[index].kind != javascriptIdentifier || source.tokens[index+1].value != "." ||
			source.tokens[index+2].value != "get" || source.tokens[index+3].value != "(" || source.tokens[index+4].kind != javascriptString {
			continue
		}
		pluginPrefixes, pluginReceiver := pluginMounts[receiver]
		if !servers[receiver] && !routers[receiver] && !pluginReceiver {
			continue
		}
		prefixes := []string{""}
		if pluginReceiver {
			prefixes = pluginPrefixes
		} else if routers[receiver] && len(mounts[receiver]) > 0 {
			prefixes = mounts[receiver]
		}
		for _, prefix := range prefixes {
			endpoint := statichealth.JoinPath(prefix, source.tokens[index+4].value)
			semantic := statichealth.SemanticRank(endpoint)
			if semantic == 0 {
				continue
			}
			result = append(result, statichealth.Candidate{
				Path: endpoint, File: source.file, Explanation: fmt.Sprintf("Node GET readiness route %s found", endpoint), Rank: nodeRegisteredGetRank + semantic,
			})
		}
	}
	return result
}

func registeredFastifyPluginMounts(tokens []javascriptToken, servers map[string]bool) map[string][]string {
	receivers := fastifyPluginReceivers(tokens)
	result := make(map[string][]string)
	for index := 0; index+4 < len(tokens); index++ {
		if !servers[tokens[index].value] || tokens[index+1].value != "." || tokens[index+2].value != "register" || tokens[index+3].value != "(" || tokens[index+4].kind != javascriptIdentifier {
			continue
		}
		receiver := receivers[tokens[index+4].value]
		if receiver == "" {
			continue
		}
		closing := matchingPunctuation(tokens, index+3, "(", ")")
		if closing < 0 {
			continue
		}
		prefix := ""
		prefixPresent := false
		prefixStatic := true
		for optionIndex := index + 5; optionIndex+2 < closing; optionIndex++ {
			if tokens[optionIndex].value != "prefix" || tokens[optionIndex+1].value != ":" {
				continue
			}
			prefixPresent = true
			if tokens[optionIndex+2].kind != javascriptString {
				prefixStatic = false
				break
			}
			prefix = tokens[optionIndex+2].value
			break
		}
		if prefixPresent && !prefixStatic {
			continue
		}
		if prefix != "" {
			prefix = statichealth.JoinPath(prefix)
			if prefix == "" {
				continue
			}
		}
		result[receiver] = append(result[receiver], prefix)
	}
	for receiver := range result {
		sort.Strings(result[receiver])
	}
	return result
}

func fastifyPluginReceivers(tokens []javascriptToken) map[string]string {
	result := make(map[string]string)
	for index := 0; index < len(tokens); index++ {
		if tokens[index].value == "function" && index+3 < len(tokens) && tokens[index+1].kind == javascriptIdentifier && tokens[index+2].value == "(" && tokens[index+3].kind == javascriptIdentifier {
			result[tokens[index+1].value] = tokens[index+3].value
			continue
		}
		if tokens[index].value != "const" && tokens[index].value != "let" && tokens[index].value != "var" {
			continue
		}
		if index+4 >= len(tokens) || tokens[index+1].kind != javascriptIdentifier || tokens[index+2].value != "=" {
			continue
		}
		plugin := tokens[index+1].value
		parameterIndex := index + 3
		if tokens[parameterIndex].value == "async" {
			parameterIndex++
		}
		if parameterIndex+1 < len(tokens) && tokens[parameterIndex].value == "(" && tokens[parameterIndex+1].kind == javascriptIdentifier {
			closing := matchingPunctuation(tokens, parameterIndex, "(", ")")
			if closing >= 0 && closing+2 < len(tokens) && tokens[closing+1].value == "=" && tokens[closing+2].value == ">" {
				result[plugin] = tokens[parameterIndex+1].value
			}
			continue
		}
		if parameterIndex+2 < len(tokens) && tokens[parameterIndex].kind == javascriptIdentifier && tokens[parameterIndex+1].value == "=" && tokens[parameterIndex+2].value == ">" {
			result[plugin] = tokens[parameterIndex].value
		}
	}
	return result
}

func registeredServerVariables(tokens []javascriptToken) (map[string]bool, map[string]bool) {
	servers := make(map[string]bool)
	routers := make(map[string]bool)
	for index := 0; index+4 < len(tokens); index++ {
		if tokens[index].value != "const" && tokens[index].value != "let" && tokens[index].value != "var" {
			continue
		}
		if tokens[index+1].kind != javascriptIdentifier || tokens[index+2].value != "=" {
			continue
		}
		name := tokens[index+1].value
		right := index + 3
		if tokens[right].value == "await" {
			right++
		}
		if right >= len(tokens) || tokens[right].kind != javascriptIdentifier {
			continue
		}
		constructor := strings.ToLower(tokens[right].value)
		if constructor == "express" || constructor == "fastify" {
			if right+1 < len(tokens) && tokens[right+1].value == "(" {
				servers[name] = true
			}
			if right+3 < len(tokens) && tokens[right+1].value == "." && strings.EqualFold(tokens[right+2].value, "Router") && tokens[right+3].value == "(" {
				routers[name] = true
			}
		}
		if constructor == "router" && right+1 < len(tokens) && tokens[right+1].value == "(" {
			routers[name] = true
		}
	}
	return servers, routers
}

func registeredRouterMounts(tokens []javascriptToken, servers, routers map[string]bool) map[string][]string {
	result := make(map[string][]string)
	for index := 0; index+6 < len(tokens); index++ {
		if !servers[tokens[index].value] || tokens[index+1].value != "." || tokens[index+2].value != "use" || tokens[index+3].value != "(" ||
			tokens[index+4].kind != javascriptString || tokens[index+5].value != "," || !routers[tokens[index+6].value] {
			continue
		}
		prefix := statichealth.JoinPath(tokens[index+4].value)
		if prefix != "" {
			result[tokens[index+6].value] = append(result[tokens[index+6].value], prefix)
		}
	}
	for router := range result {
		sort.Strings(result[router])
	}
	return result
}

func rawNodeHealthCandidates(source nodeSource, runtimeProven bool) []statichealth.Candidate {
	if !runtimeProven || !containsCall(source.tokens, "createServer") {
		return nil
	}
	var result []statichealth.Candidate
	for index := 0; index+4 < len(source.tokens); index++ {
		if source.tokens[index].value != "." || source.tokens[index+1].kind != javascriptIdentifier {
			continue
		}
		property := source.tokens[index+1].value
		if property != "url" && property != "pathname" {
			continue
		}
		valueIndex := index + 2
		equalityCount := 0
		for valueIndex < len(source.tokens) && valueIndex <= index+5 && source.tokens[valueIndex].value == "=" {
			equalityCount++
			valueIndex++
		}
		if equalityCount < 2 || valueIndex >= len(source.tokens) || source.tokens[valueIndex].kind != javascriptString {
			continue
		}
		endpoint := statichealth.CleanPath(source.tokens[valueIndex].value)
		semantic := statichealth.SemanticRank(endpoint)
		if semantic == 0 {
			continue
		}
		result = append(result, statichealth.Candidate{
			Path: endpoint, File: source.file, Explanation: fmt.Sprintf("Node HTTP readiness path comparison for %s found", endpoint), Rank: nodeRawRouteRank + semantic,
		})
	}
	return result
}

func containsMethodCall(tokens []javascriptToken, method string) bool {
	for index := 0; index+2 < len(tokens); index++ {
		if tokens[index].value == "." && tokens[index+1].value == method && tokens[index+2].value == "(" {
			return true
		}
	}
	return false
}

func containsNodePortEnvironment(tokens []javascriptToken) bool {
	for index := 0; index+4 < len(tokens); index++ {
		if tokens[index].value != "process" || tokens[index+1].value != "." || tokens[index+2].value != "env" {
			continue
		}
		if tokens[index+3].value == "." && tokens[index+4].value == "PORT" {
			return true
		}
		if index+5 < len(tokens) && tokens[index+3].value == "[" && tokens[index+4].kind == javascriptString && tokens[index+4].value == "PORT" && tokens[index+5].value == "]" {
			return true
		}
	}
	return false
}

func containsCall(tokens []javascriptToken, name string) bool {
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index].value == name && tokens[index+1].value == "(" {
			return true
		}
	}
	return false
}

func staticMethodStringArguments(tokens []javascriptToken, method string) ([]string, bool) {
	var result []string
	dynamic := false
	for index := 0; index+4 < len(tokens); index++ {
		if tokens[index].kind != javascriptIdentifier || tokens[index+1].value != "." || tokens[index+2].value != method || tokens[index+3].value != "(" {
			continue
		}
		if tokens[index+4].kind != javascriptString {
			dynamic = true
			continue
		}
		result = append(result, tokens[index+4].value)
	}
	return result, dynamic
}

func findToken(tokens []javascriptToken, start int, value string, limit int) int {
	end := start + limit
	if end > len(tokens) {
		end = len(tokens)
	}
	for index := start; index < end; index++ {
		if tokens[index].value == value {
			return index
		}
	}
	return -1
}

func matchingPunctuation(tokens []javascriptToken, start int, opening, closing string) int {
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

func tokenizeJavaScript(encoded []byte) []javascriptToken {
	var result []javascriptToken
	for index := 0; index < len(encoded); {
		character := encoded[index]
		if isJavaScriptSpace(character) {
			index++
			continue
		}
		if character == '/' && index+1 < len(encoded) && encoded[index+1] == '/' {
			index += 2
			for index < len(encoded) && encoded[index] != '\n' && encoded[index] != '\r' {
				index++
			}
			continue
		}
		if character == '/' && index+1 < len(encoded) && encoded[index+1] == '*' {
			index += 2
			for index+1 < len(encoded) && !(encoded[index] == '*' && encoded[index+1] == '/') {
				index++
			}
			if index+1 < len(encoded) {
				index += 2
			}
			continue
		}
		if character == '\'' || character == '"' || character == '`' {
			value, next, ok := javascriptStringValue(encoded, index)
			if ok {
				result = append(result, javascriptToken{kind: javascriptString, value: value})
			}
			index = next
			continue
		}
		if isJavaScriptIdentifierStart(character) {
			start := index
			index++
			for index < len(encoded) && isJavaScriptIdentifierPart(encoded[index]) {
				index++
			}
			result = append(result, javascriptToken{kind: javascriptIdentifier, value: string(encoded[start:index])})
			continue
		}
		result = append(result, javascriptToken{kind: javascriptPunctuation, value: string(character)})
		index++
	}
	return result
}

func javascriptStringValue(encoded []byte, start int) (string, int, bool) {
	quote := encoded[start]
	var value strings.Builder
	static := true
	for index := start + 1; index < len(encoded); index++ {
		character := encoded[index]
		if character == quote {
			return value.String(), index + 1, static
		}
		if quote == '`' && character == '$' && index+1 < len(encoded) && encoded[index+1] == '{' {
			static = false
		}
		if character != '\\' {
			value.WriteByte(character)
			continue
		}
		if index+1 >= len(encoded) {
			return "", len(encoded), false
		}
		index++
		escaped := encoded[index]
		switch escaped {
		case '\\', '/', '\'', '"', '`':
			value.WriteByte(escaped)
		case 'n':
			value.WriteByte('\n')
		case 'r':
			value.WriteByte('\r')
		case 't':
			value.WriteByte('\t')
		case 'x':
			if index+2 >= len(encoded) {
				return "", len(encoded), false
			}
			decoded, err := strconv.ParseUint(string(encoded[index+1:index+3]), 16, 8)
			if err != nil {
				static = false
			} else {
				value.WriteByte(byte(decoded))
			}
			index += 2
		default:
			static = false
			value.WriteByte(escaped)
		}
	}
	return "", len(encoded), false
}

func isJavaScriptSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n' || value == '\f'
}

func isJavaScriptIdentifierStart(value byte) bool {
	return value == '_' || value == '$' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isJavaScriptIdentifierPart(value byte) bool {
	return isJavaScriptIdentifierStart(value) || value >= '0' && value <= '9'
}
