package mocks

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
	"go.yaml.in/yaml/v3"
)

var openAPIMethods = []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}

// RoutesFromOpenAPI derives deterministic routes from a local OpenAPI 3.0 or
// 3.1 document. Only internal references and fixed examples or defaults are
// accepted; the importer never fetches a server or external reference.
func RoutesFromOpenAPI(document []byte) ([]model.MockRoute, []string, error) {
	var root map[string]any
	if err := yaml.Unmarshal(document, &root); err != nil {
		return nil, nil, fmt.Errorf("decode OpenAPI document: %w", err)
	}
	version, _ := root["openapi"].(string)
	if !strings.HasPrefix(version, "3.0.") && !strings.HasPrefix(version, "3.1.") {
		return nil, nil, errors.New("OpenAPI document must declare a 3.0.x or 3.1.x version")
	}
	if err := rejectExternalReferences(root); err != nil {
		return nil, nil, err
	}
	paths, ok := object(root["paths"])
	if !ok {
		return nil, nil, errors.New("OpenAPI document has no paths object")
	}
	pathNames := sortedKeys(paths)
	routes := []model.MockRoute{}
	warnings := []string{}
	names := map[string]int{}
	for _, routePath := range pathNames {
		pathItem, err := resolvedObject(root, paths[routePath], 0)
		if err != nil {
			return nil, warnings, fmt.Errorf("path %s: %w", routePath, err)
		}
		for _, method := range openAPIMethods {
			operationValue, exists := pathItem[method]
			if !exists {
				continue
			}
			operation, err := resolvedObject(root, operationValue, 0)
			if err != nil {
				return nil, warnings, fmt.Errorf("%s %s: %w", strings.ToUpper(method), routePath, err)
			}
			status, contentType, body, responseWarnings, err := openAPIResponse(root, operation)
			warnings = append(warnings, responseWarnings...)
			if err != nil {
				return nil, warnings, fmt.Errorf("%s %s: %w", strings.ToUpper(method), routePath, err)
			}
			query, err := openAPIQuery(root, pathItem, operation)
			if err != nil {
				return nil, warnings, fmt.Errorf("%s %s: %w", strings.ToUpper(method), routePath, err)
			}
			name, _ := operation["operationId"].(string)
			name = model.NormalizeDNSName(name)
			if name == "" {
				name = model.NormalizeDNSName(method + "-" + strings.Trim(strings.ReplaceAll(routePath, "/", "-"), "-"))
			}
			if name == "" {
				name = method + "-route"
			}
			names[name]++
			if names[name] > 1 {
				name = uniqueRouteName(name, names[name])
			}
			headers := map[string]string{}
			if contentType != "" {
				headers["Content-Type"] = contentType
			}
			routes = append(routes, model.MockRoute{
				Name: name, Method: strings.ToUpper(method), Path: routePath, Query: query,
				Status: status, Headers: headers, Body: body, Enabled: true,
			})
		}
	}
	if len(routes) == 0 {
		warnings = append(warnings, "The OpenAPI document contained no supported HTTP operations.")
	}
	return routes, warnings, nil
}

func uniqueRouteName(base string, number int) string {
	suffix := fmt.Sprintf("-%d", number)
	if len(base)+len(suffix) > 64 {
		base = strings.TrimRight(base[:64-len(suffix)], "-._")
	}
	return base + suffix
}

func openAPIResponse(root, operation map[string]any) (int, string, string, []string, error) {
	responses, ok := object(operation["responses"])
	if !ok {
		return 0, "", "", nil, errors.New("operation has no responses object")
	}
	codes := sortedKeys(responses)
	selectedCode := ""
	for _, code := range codes {
		status, err := strconv.Atoi(code)
		if err == nil && status >= 200 && status < 300 {
			selectedCode = code
			break
		}
	}
	if selectedCode == "" {
		if _, exists := responses["default"]; exists {
			selectedCode = "default"
		} else {
			return 0, "", "", nil, errors.New("operation has no fixed 2xx or default response")
		}
	}
	status := 200
	if selectedCode != "default" {
		status, _ = strconv.Atoi(selectedCode)
	}
	response, err := resolvedObject(root, responses[selectedCode], 0)
	if err != nil {
		return 0, "", "", nil, err
	}
	content, ok := object(response["content"])
	if !ok || len(content) == 0 {
		return status, "", "", []string{"Response " + selectedCode + " has no example body; the generated route returns an empty body."}, nil
	}
	contentTypes := sortedKeys(content)
	selectedType := contentTypes[0]
	for _, candidate := range contentTypes {
		if strings.Contains(strings.ToLower(candidate), "json") {
			selectedType = candidate
			break
		}
	}
	media, err := resolvedObject(root, content[selectedType], 0)
	if err != nil {
		return 0, "", "", nil, err
	}
	example, found, err := openAPIExample(root, media)
	if err != nil {
		return 0, "", "", nil, err
	}
	if !found {
		return status, selectedType, "", []string{"Response " + selectedCode + " has no example or schema default; the generated route returns an empty body."}, nil
	}
	if text, ok := example.(string); ok && !strings.Contains(strings.ToLower(selectedType), "json") {
		return status, selectedType, text, nil, nil
	}
	encoded, err := json.Marshal(example)
	if err != nil {
		return 0, "", "", nil, fmt.Errorf("encode response example: %w", err)
	}
	return status, selectedType, string(encoded), nil, nil
}

func openAPIExample(root, media map[string]any) (any, bool, error) {
	if example, exists := media["example"]; exists {
		return example, true, nil
	}
	if examples, ok := object(media["examples"]); ok {
		for _, name := range sortedKeys(examples) {
			entry, err := resolvedObject(root, examples[name], 0)
			if err != nil {
				return nil, false, err
			}
			if value, exists := entry["value"]; exists {
				return value, true, nil
			}
		}
	}
	if schemaValue, exists := media["schema"]; exists {
		schema, err := resolvedObject(root, schemaValue, 0)
		if err != nil {
			return nil, false, err
		}
		if example, exists := schema["example"]; exists {
			return example, true, nil
		}
		if value, exists := schema["default"]; exists {
			return value, true, nil
		}
	}
	return nil, false, nil
}

func openAPIQuery(root, pathItem, operation map[string]any) (map[string]string, error) {
	result := map[string]string{}
	for _, owner := range []map[string]any{pathItem, operation} {
		parameters, _ := owner["parameters"].([]any)
		for _, parameterValue := range parameters {
			parameter, err := resolvedObject(root, parameterValue, 0)
			if err != nil {
				return nil, err
			}
			location, _ := parameter["in"].(string)
			required, _ := parameter["required"].(bool)
			name, _ := parameter["name"].(string)
			if location != "query" || !required || name == "" {
				continue
			}
			value, exists := parameter["example"]
			if !exists {
				if schema, ok := object(parameter["schema"]); ok {
					value, exists = schema["example"]
					if !exists {
						value, exists = schema["default"]
					}
				}
			}
			if exists {
				result[name] = fmt.Sprint(value)
			} else {
				result[name] = ""
			}
		}
	}
	return result, nil
}

func resolvedObject(root map[string]any, value any, depth int) (map[string]any, error) {
	if depth > 32 {
		return nil, errors.New("internal reference depth exceeds 32")
	}
	current, ok := object(value)
	if !ok {
		return nil, errors.New("expected an object")
	}
	reference, _ := current["$ref"].(string)
	if reference == "" {
		return current, nil
	}
	if !strings.HasPrefix(reference, "#/") {
		return nil, fmt.Errorf("external reference %q is not supported", reference)
	}
	resolved, err := resolveJSONPointer(root, reference)
	if err != nil {
		return nil, err
	}
	return resolvedObject(root, resolved, depth+1)
}

func resolveJSONPointer(root map[string]any, reference string) (any, error) {
	var current any = root
	for _, raw := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		object, ok := object(current)
		if !ok {
			return nil, fmt.Errorf("internal reference %q traverses a non-object", reference)
		}
		current, ok = object[key]
		if !ok {
			return nil, fmt.Errorf("internal reference %q was not found", reference)
		}
	}
	return current, nil
}

func rejectExternalReferences(value any) error {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if key == "$ref" {
				if reference, ok := child.(string); ok && !strings.HasPrefix(reference, "#/") {
					return fmt.Errorf("external reference %q is not supported", reference)
				}
			}
			if err := rejectExternalReferences(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range current {
			if err := rejectExternalReferences(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func object(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func sortedKeys(values map[string]any) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
