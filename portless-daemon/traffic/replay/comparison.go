package replay

import (
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

const (
	maxJSONNodes   = 10000
	maxJSONDepth   = 64
	maxChanges     = 1000
	maxChangeBytes = 256 << 10
	maxTextLines   = 2000
	maxTextSteps   = 1000000
)

type jsonValue struct {
	kind   string
	scalar string
	object map[string]*jsonValue
	array  []*jsonValue
}

func parseJSON(body string) (*jsonValue, string) {
	if !utf8.ValidString(body) {
		return nil, "Body is not valid UTF-8."
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	nodes := 0
	value, err := readJSON(decoder, 0, &nodes)
	if err != nil {
		return nil, err.Error()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, "JSON contains trailing tokens or invalid trailing data."
	}
	return value, ""
}

func readJSON(decoder *json.Decoder, depth int, nodes *int) (*jsonValue, error) {
	*nodes++
	if depth > maxJSONDepth || *nodes > maxJSONNodes {
		return nil, errors.New("JSON exceeds the structural comparison budget.")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, errors.New("Body is not valid JSON.")
	}
	node := &jsonValue{}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			node.kind, node.object = "object", map[string]*jsonValue{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				key, ok := keyToken.(string)
				if err != nil || !ok {
					return nil, errors.New("Body is not valid JSON.")
				}
				if _, exists := node.object[key]; exists {
					return nil, errors.New("JSON has duplicate object keys; text comparison preserves the ambiguity.")
				}
				child, err := readJSON(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				node.object[key] = child
			}
			if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
				return nil, errors.New("Body is not valid JSON.")
			}
		case '[':
			node.kind = "array"
			for decoder.More() {
				child, err := readJSON(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				node.array = append(node.array, child)
			}
			if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
				return nil, errors.New("Body is not valid JSON.")
			}
		default:
			return nil, errors.New("Body is not valid JSON.")
		}
	case json.Number:
		node.kind, node.scalar = "number", string(value)
	case string:
		encoded, _ := json.Marshal(value)
		node.kind, node.scalar = "string", string(encoded)
	case bool:
		node.kind, node.scalar = "boolean", strconv.FormatBool(value)
	case nil:
		node.kind, node.scalar = "null", "null"
	default:
		return nil, errors.New("Body is not valid JSON.")
	}
	return node, nil
}

type changeCollector struct {
	changes   []contract.TrafficReplayChange
	bytes     int
	limited   bool
	different bool
}

func (c *changeCollector) add(path, kind, before, after string) {
	c.different = true
	size := len(path) + len(kind) + len(before) + len(after) + 64
	if len(c.changes) >= maxChanges || c.bytes+size > maxChangeBytes {
		c.limited = true
		return
	}
	c.changes = append(c.changes, contract.TrafficReplayChange{Path: path, Kind: kind, Before: before, After: after})
	c.bytes += size
}

func displayJSON(value *jsonValue) string {
	if value == nil {
		return ""
	}
	if value.kind == "object" {
		return "{object}"
	}
	if value.kind == "array" {
		return "[array]"
	}
	return value.scalar
}

func compareJSON(before, after *jsonValue, path string, changes *changeCollector) {
	if before == nil {
		changes.add(path, "added", "", displayJSON(after))
		return
	}
	if after == nil {
		changes.add(path, "removed", displayJSON(before), "")
		return
	}
	if before.kind != after.kind {
		changes.add(path, "type-changed", displayJSON(before), displayJSON(after))
		return
	}
	switch before.kind {
	case "object":
		keys := map[string]bool{}
		for key := range before.object {
			keys[key] = true
		}
		for key := range after.object {
			keys[key] = true
		}
		for _, key := range sortedKeys(keys) {
			compareJSON(before.object[key], after.object[key], path+"/"+strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"), changes)
		}
	case "array":
		for i := 0; i < max(len(before.array), len(after.array)); i++ {
			var left, right *jsonValue
			if i < len(before.array) {
				left = before.array[i]
			}
			if i < len(after.array) {
				right = after.array[i]
			}
			compareJSON(left, right, path+"/"+strconv.Itoa(i), changes)
		}
	default:
		if before.scalar != after.scalar {
			changes.add(path, "changed", before.scalar, after.scalar)
		}
	}
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func compareText(before, after string) contract.TrafficComparisonSection {
	section := contract.TrafficComparisonSection{State: "equal", Format: "text"}
	if before == after {
		return section
	}
	section.State = "different"
	left, right := strings.SplitAfter(before, "\n"), strings.SplitAfter(after, "\n")
	if len(left) > maxTextLines || len(right) > maxTextLines || (len(left)+1)*(len(right)+1) > maxTextSteps {
		section.Reason = "Text diff exceeds the line or work budget; inspect the bounded raw responses."
		return section
	}
	width := len(right) + 1
	lengths := make([]uint16, (len(left)+1)*width)
	for i := len(left) - 1; i >= 0; i-- {
		for j := len(right) - 1; j >= 0; j-- {
			if left[i] == right[j] {
				lengths[i*width+j] = lengths[(i+1)*width+j+1] + 1
			} else {
				lengths[i*width+j] = max(lengths[(i+1)*width+j], lengths[i*width+j+1])
			}
		}
	}
	changes := &changeCollector{}
	i, j := 0, 0
	for i < len(left) || j < len(right) {
		if i < len(left) && j < len(right) && left[i] == right[j] {
			i++
			j++
			continue
		}
		if j < len(right) && (i == len(left) || lengths[i*width+j+1] >= lengths[(i+1)*width+j]) {
			changes.add("/"+strconv.Itoa(j+1), "added", "", right[j])
			j++
		} else {
			changes.add("/"+strconv.Itoa(i+1), "removed", left[i], "")
			i++
		}
	}
	section.Changes = changes.changes
	if changes.limited {
		section.Reason = "Text changes exceed the display budget; inspect the bounded raw responses."
	}
	return section
}

func normalizedHeaders(headers map[string][]string) map[string][]string {
	result := map[string][]string{}
	for _, name := range sortedKeys(headers) {
		key := strings.ToLower(name)
		result[key] = append(result[key], headers[name]...)
	}
	return result
}

func compareHeaders(before, after map[string][]string) contract.TrafficComparisonSection {
	left, right := normalizedHeaders(before), normalizedHeaders(after)
	keys := map[string]bool{}
	for name := range left {
		keys[name] = true
	}
	for name := range right {
		keys[name] = true
	}
	changes := &changeCollector{}
	partial := false
	for _, name := range sortedKeys(keys) {
		first, existsFirst := left[name]
		second, existsSecond := right[name]
		if sensitiveHeader(name) || containsRedacted(first) || containsRedacted(second) {
			partial = true
			continue
		}
		beforeJSON, _ := json.Marshal(first)
		afterJSON, _ := json.Marshal(second)
		switch {
		case !existsFirst:
			changes.add(name, "added", "", string(afterJSON))
		case !existsSecond:
			changes.add(name, "removed", string(beforeJSON), "")
		case string(beforeJSON) != string(afterJSON):
			changes.add(name, "changed", string(beforeJSON), string(afterJSON))
		}
	}
	section := contract.TrafficComparisonSection{State: "equal", Format: "headers", Changes: changes.changes}
	if changes.different {
		section.State = "different"
	}
	if partial {
		section.State, section.Reason = "partial", "Redacted header values are unknown and cannot establish equality."
	}
	if changes.limited {
		section.Reason = "Header changes exceed the display budget."
	}
	return section
}

func isJSON(exchange model.TrafficExchange) bool {
	for name, values := range exchange.ResponseHeaders {
		if strings.EqualFold(name, "content-type") {
			for _, value := range values {
				if strings.Contains(strings.ToLower(value), "json") {
					return true
				}
			}
		}
	}
	return false
}

func compareBody(before, after model.TrafficExchange) contract.TrafficComparisonSection {
	if before.Status == 0 || after.Status == 0 {
		return contract.TrafficComparisonSection{State: "unavailable", Reason: "A response was not received for both requests."}
	}
	left, right := before.ResponseCapture, after.ResponseCapture
	if left == nil || right == nil {
		return contract.TrafficComparisonSection{State: "unavailable", Reason: "Body capture fidelity is unavailable."}
	}
	if !completeBody(left, before.ResponseBody) || !completeBody(right, after.ResponseBody) {
		section := contract.TrafficComparisonSection{State: "unavailable", Reason: "A response body is omitted, unsupported, or incomplete."}
		if before.ResponseBody != "" || after.ResponseBody != "" {
			section = compareText(before.ResponseBody, after.ResponseBody)
			section.State, section.Reason = "partial", "Comparison covers captured prefixes only; complete body equality is unknown."
		}
		return section
	}
	if strings.Contains(before.ResponseBody, redacted) || strings.Contains(after.ResponseBody, redacted) {
		section := compareText(before.ResponseBody, after.ResponseBody)
		section.State, section.Reason = "partial", "Redacted body values are unknown and cannot establish equality."
		return section
	}
	if isJSON(before) && isJSON(after) {
		first, firstReason := parseJSON(before.ResponseBody)
		second, secondReason := parseJSON(after.ResponseBody)
		if firstReason == "" && secondReason == "" {
			changes := &changeCollector{}
			compareJSON(first, second, "", changes)
			section := contract.TrafficComparisonSection{State: "equal", Format: "json", Changes: changes.changes}
			if changes.different {
				section.State = "different"
			}
			if changes.limited {
				section.Reason = "JSON changes exceed the display budget; inspect the bounded raw responses."
			}
			return section
		}
		section := compareText(before.ResponseBody, after.ResponseBody)
		section.Reason = firstReason
		if section.Reason == "" {
			section.Reason = secondReason
		}
		return section
	}
	return compareText(before.ResponseBody, after.ResponseBody)
}

// Compare returns a bounded, lossless comparison of safe captured HTTP responses.
func Compare(before, after model.TrafficExchange) contract.TrafficResponseComparison {
	if len(before.ResponseBody) > maxBodyBytes || len(after.ResponseBody) > maxBodyBytes || !headerBudget(before.ResponseHeaders, maxResponseHeaderBytes, false) || !headerBudget(after.ResponseHeaders, maxResponseHeaderBytes, false) {
		return contract.TrafficResponseComparison{State: "unavailable", OriginalStatus: before.Status, ReplayStatus: after.Status, StatusChanged: before.Status != after.Status, Headers: contract.TrafficComparisonSection{State: "unavailable", Reason: "Response exceeds the comparison capture budget."}, Body: contract.TrafficComparisonSection{State: "unavailable", Reason: "Response exceeds the comparison capture budget."}}
	}
	comparison := contract.TrafficResponseComparison{
		State: "equal", OriginalStatus: before.Status, ReplayStatus: after.Status,
		StatusChanged:   before.Status != after.Status,
		DurationDeltaMS: after.DurationMS - before.DurationMS,
		Headers:         compareHeaders(before.ResponseHeaders, after.ResponseHeaders), Body: compareBody(before, after),
	}
	if comparison.StatusChanged || comparison.Headers.State == "different" || comparison.Body.State == "different" {
		comparison.State = "different"
	}
	if comparison.Headers.State == "partial" || comparison.Body.State == "partial" || comparison.Body.State == "unavailable" {
		comparison.State = "partial"
	}
	if before.Status == 0 || after.Status == 0 {
		comparison.State = "unavailable"
		comparison.Headers.State = "unavailable"
		comparison.Headers.Reason = "A response was not received for both requests."
	}
	return comparison
}
