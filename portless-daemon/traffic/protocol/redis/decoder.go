// Package redis decodes bounded RESP2 and RESP3 operations used by Redis and Valkey.
package redis

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
)

const (
	maximumRESPDepth          = 32
	maximumRESPAggregateItems = 4096
)

type value struct {
	kind   byte
	text   []byte
	values []value
}

type pendingOperation struct {
	operation protocol.Operation
	limit     int
}

// Decoder incrementally decodes one Redis serialization protocol session.
type Decoder struct {
	mu               sync.Mutex
	config           protocol.Config
	requestBuffer    []byte
	responseBuffer   []byte
	pending          []*pendingOperation
	state            protocol.State
	requestObserved  bool
	responseObserved bool
}

// New creates a Redis/Valkey protocol decoder.
func New(config protocol.Config) protocol.Session {
	return &Decoder{config: config, state: protocol.State{Inspection: model.TrafficInspectionDecoded}}
}

// Observe consumes one unchanged chunk from a direction of the TCP stream.
func (d *Decoder) Observe(direction protocol.Direction, content []byte, observed time.Time) []protocol.Operation {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(content) == 0 || d.state.Inspection != model.TrafficInspectionDecoded {
		return nil
	}
	if direction == protocol.DirectionRequest {
		if !d.requestObserved && looksLikeTLS(content) {
			d.state = protocol.State{Inspection: model.TrafficInspectionEncrypted, Reason: "TLS-encrypted Redis traffic"}
			return nil
		}
		d.requestObserved = true
		return d.observeRequest(content, observed)
	}
	if !d.responseObserved && looksLikeTLS(content) {
		d.state = protocol.State{Inspection: model.TrafficInspectionEncrypted, Reason: "TLS-encrypted Redis traffic"}
		return nil
	}
	d.responseObserved = true
	return d.observeResponse(content, observed)
}

// Close completes pending operations as incomplete when the connection closes.
func (d *Decoder) Close(completed time.Time, connectionErr error) []protocol.Operation {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Inspection == model.TrafficInspectionDecoded && (len(d.requestBuffer) > 0 || len(d.responseBuffer) > 0) {
		d.state = protocol.State{Inspection: model.TrafficInspectionMalformed, Reason: "Redis connection closed during a protocol frame"}
	}
	result := make([]protocol.Operation, 0, len(d.pending))
	for _, pending := range d.pending {
		pending.operation.CompletedAt = completed
		pending.operation.Outcome = model.TrafficTCPOutcomeIncomplete
		if d.state.Inspection != model.TrafficInspectionDecoded {
			pending.operation.Inspection = d.state.Inspection
			pending.operation.InspectionReason = d.state.Reason
		}
		if connectionErr != nil {
			pending.operation.Error = connectionErr.Error()
		}
		result = append(result, pending.operation)
	}
	d.pending = nil
	return result
}

// State returns the current decoder inspection state.
func (d *Decoder) State() protocol.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *Decoder) observeRequest(content []byte, observed time.Time) []protocol.Operation {
	if !d.appendBuffer(&d.requestBuffer, content) {
		return nil
	}
	var completed []protocol.Operation
	for len(d.requestBuffer) > 0 {
		parsed, consumed, more, err := parseValue(d.requestBuffer, 0)
		if more {
			break
		}
		if err != nil {
			if isRESPPrefix(d.requestBuffer[0]) {
				d.state = protocol.StateForError(err)
				break
			}
			// Redis also permits a legacy inline command. Accept one bounded line
			// before classifying the connection as malformed.
			lineEnd := bytes.Index(d.requestBuffer, []byte("\r\n"))
			if lineEnd < 0 {
				if len(d.requestBuffer) > protocol.MaximumControlLine {
					d.fail(model.TrafficInspectionLimited, "Redis inline command exceeded the bounded control-line limit")
				}
				break
			}
			if lineEnd > protocol.MaximumControlLine {
				d.fail(model.TrafficInspectionLimited, "Redis inline command exceeded the bounded control-line limit")
				break
			}
			parts := strings.Fields(string(d.requestBuffer[:lineEnd]))
			if len(parts) == 0 {
				d.fail(model.TrafficInspectionMalformed, "invalid RESP request framing")
				break
			}
			parsed.kind = '*'
			for _, part := range parts {
				parsed.values = append(parsed.values, value{kind: '$', text: []byte(part)})
			}
			consumed = lineEnd + 2
		}
		d.requestBuffer = d.requestBuffer[consumed:]
		operation := d.newRequest(parsed, int64(consumed), observed)
		if len(d.pending) >= protocol.MaximumPendingOperations {
			oldest := d.pending[0]
			d.pending = d.pending[1:]
			oldest.operation.CompletedAt = observed
			oldest.operation.Outcome = model.TrafficTCPOutcomeIncomplete
			oldest.operation.Inspection = model.TrafficInspectionLimited
			oldest.operation.InspectionReason = "Redis pipeline exceeded the bounded pending-operation limit"
			completed = append(completed, oldest.operation)
		}
		d.pending = append(d.pending, operation)
	}
	return completed
}

func (d *Decoder) observeResponse(content []byte, observed time.Time) []protocol.Operation {
	if !d.appendBuffer(&d.responseBuffer, content) {
		return nil
	}
	var completed []protocol.Operation
	for len(d.responseBuffer) > 0 {
		parsed, consumed, more, err := parseValue(d.responseBuffer, 0)
		if more {
			break
		}
		if err != nil {
			d.state = protocol.StateForError(err)
			break
		}
		d.responseBuffer = d.responseBuffer[consumed:]
		if parsed.kind == '|' && len(d.pending) > 0 {
			pending := d.pending[0]
			message := messageForValue(parsed, int64(consumed), pending.operation.StartedAt, observed, pending.limit, false)
			message.Type = "attributes"
			if protocol.AppendMessage(&pending.operation.ResponseMessages, message, pending.limit) {
				pending.operation.Inspection = model.TrafficInspectionLimited
				pending.operation.InspectionReason = "Redis response attributes were truncated by the capture limit"
			}
			pending.operation.ResponseBytes += int64(consumed)
			continue
		}
		if isPush(parsed) && len(d.pending) > 0 && pushAcknowledges(d.pending[0].operation.Name, parsed) {
			// RESP3 represents subscription acknowledgements as pushes even though
			// they complete the corresponding subscription command.
		} else if isPush(parsed) || len(d.pending) == 0 {
			completed = append(completed, d.responseOnly(parsed, int64(consumed), observed))
			continue
		}
		pending := d.pending[0]
		d.pending = d.pending[1:]
		message := messageForValue(parsed, int64(consumed), pending.operation.StartedAt, observed, pending.limit, false)
		limited := protocol.AppendMessage(&pending.operation.ResponseMessages, message, pending.limit)
		pending.operation.ResponseBytes += int64(consumed)
		pending.operation.CompletedAt = observed
		pending.operation.Outcome = model.TrafficTCPOutcomeSuccess
		if limited {
			pending.operation.Inspection = model.TrafficInspectionLimited
			pending.operation.InspectionReason = "Redis response payload was truncated by the capture limit"
		}
		if isErrorValue(parsed) {
			pending.operation.Outcome = model.TrafficTCPOutcomeError
			pending.operation.Error = strings.TrimSpace(string(parsed.text))
		}
		completed = append(completed, pending.operation)
	}
	return completed
}

func (d *Decoder) newRequest(parsed value, wireBytes int64, observed time.Time) *pendingOperation {
	policy := d.config.CapturePolicy()
	captureLimit := policy.CaptureLimit()
	operationName, summary := commandSummary(parsed)
	operation := protocol.Operation{
		StartedAt: observed, Name: operationName, Inspection: model.TrafficInspectionDecoded,
		Background: backgroundCommand(parsed), Outcome: model.TrafficTCPOutcomeSuccess, RequestBytes: wireBytes, Policy: policy,
	}
	message := messageForValue(redactAuthentication(parsed), wireBytes, observed, observed, captureLimit, true)
	message.Summary = summary
	if protocol.AppendMessage(&operation.RequestMessages, message, captureLimit) {
		operation.Inspection = model.TrafficInspectionLimited
		operation.InspectionReason = "Redis request payload was truncated by the capture limit"
	}
	return &pendingOperation{operation: operation, limit: captureLimit}
}

func (d *Decoder) responseOnly(parsed value, wireBytes int64, observed time.Time) protocol.Operation {
	policy := d.config.CapturePolicy()
	captureLimit := policy.CaptureLimit()
	name := "RESPONSE"
	if isPush(parsed) {
		name = "PUSH"
		if first := firstValue(parsed); first != "" {
			name = strings.ToUpper(first)
		}
	}
	operation := protocol.Operation{
		StartedAt: observed, CompletedAt: observed, Name: name, Inspection: model.TrafficInspectionDecoded,
		Outcome: model.TrafficTCPOutcomeOneWay, ResponseBytes: wireBytes, Policy: policy,
	}
	message := messageForValue(parsed, wireBytes, observed, observed, captureLimit, false)
	if protocol.AppendMessage(&operation.ResponseMessages, message, captureLimit) {
		operation.Inspection = model.TrafficInspectionLimited
		operation.InspectionReason = "Redis push payload was truncated by the capture limit"
	}
	if isErrorValue(parsed) {
		operation.Outcome = model.TrafficTCPOutcomeError
		operation.Error = strings.TrimSpace(string(parsed.text))
	}
	return operation
}

func (d *Decoder) appendBuffer(destination *[]byte, content []byte) bool {
	maximum := protocol.MaximumPayloadLimit + 64<<10
	if len(*destination)+len(content) > maximum {
		d.fail(model.TrafficInspectionLimited, "Redis frame exceeded the bounded parser buffer")
		*destination = nil
		return false
	}
	*destination = append(*destination, content...)
	return true
}

func (d *Decoder) fail(inspection model.TrafficInspection, reason string) {
	d.state = protocol.State{Inspection: inspection, Reason: reason}
}

func parseValue(content []byte, depth int) (value, int, bool, error) {
	if len(content) == 0 {
		return value{}, 0, true, nil
	}
	if depth > maximumRESPDepth {
		return value{}, 0, false, errors.New("RESP nesting is too deep")
	}
	kind := content[0]
	switch kind {
	case '+', '-', ':', ',', '(', '#', '_':
		line, consumed, more := parseLine(content[1:])
		if more {
			return value{}, 0, true, nil
		}
		return value{kind: kind, text: line}, consumed + 1, false, nil
	case '$', '!', '=':
		line, prefix, more := parseLine(content[1:])
		if more {
			return value{}, 0, true, nil
		}
		length, err := strconv.Atoi(string(line))
		if err != nil || length < -1 {
			return value{}, 0, false, errors.New("invalid RESP bulk length")
		}
		header := prefix + 1
		if length == -1 {
			return value{kind: kind}, header, false, nil
		}
		if length > protocol.MaximumPayloadLimit {
			return value{}, 0, false, protocol.LimitError("Redis bulk value exceeded the bounded inspection limit")
		}
		if len(content) < header+length+2 {
			return value{}, 0, true, nil
		}
		if content[header+length] != '\r' || content[header+length+1] != '\n' {
			return value{}, 0, false, errors.New("invalid RESP bulk terminator")
		}
		return value{kind: kind, text: append([]byte(nil), content[header:header+length]...)}, header + length + 2, false, nil
	case '*', '~', '>', '%', '|':
		line, prefix, more := parseLine(content[1:])
		if more {
			return value{}, 0, true, nil
		}
		count, err := strconv.Atoi(string(line))
		if err != nil || count < -1 {
			return value{}, 0, false, errors.New("invalid RESP aggregate length")
		}
		offset := prefix + 1
		if count == -1 {
			return value{kind: kind}, offset, false, nil
		}
		if count > maximumRESPAggregateItems {
			return value{}, 0, false, protocol.LimitError("Redis aggregate exceeded the bounded item limit")
		}
		if kind == '%' || kind == '|' {
			count *= 2
		}
		result := value{kind: kind, values: make([]value, 0, count)}
		for index := 0; index < count; index++ {
			item, consumed, itemMore, itemErr := parseValue(content[offset:], depth+1)
			if itemMore {
				return value{}, 0, true, nil
			}
			if itemErr != nil {
				return value{}, 0, false, itemErr
			}
			result.values = append(result.values, item)
			offset += consumed
		}
		return result, offset, false, nil
	default:
		return value{}, 0, false, fmt.Errorf("unknown RESP type %q", kind)
	}
}

func isRESPPrefix(kind byte) bool {
	switch kind {
	case '+', '-', ':', ',', '(', '#', '_', '$', '!', '=', '*', '~', '>', '%', '|', ';', '.':
		return true
	default:
		return false
	}
}

func parseLine(content []byte) ([]byte, int, bool) {
	index := bytes.Index(content, []byte("\r\n"))
	if index < 0 {
		return nil, 0, true
	}
	return content[:index], index + 2, false
}

func commandSummary(parsed value) (string, string) {
	command := strings.ToUpper(firstValue(parsed))
	if command == "" {
		command = "COMMAND"
	}
	summary := command
	if len(parsed.values) > 1 {
		argument := printable(parsed.values[1].text)
		if len(argument) > 80 {
			argument = argument[:80] + "…"
		}
		if argument != "" {
			summary += " " + argument
		}
	}
	return command, summary
}

func backgroundCommand(parsed value) bool {
	command := strings.ToUpper(firstValue(parsed))
	switch command {
	case "AUTH", "COMMAND", "HELLO", "PING", "READONLY", "READWRITE", "SELECT":
		return true
	case "CLIENT":
		if len(parsed.values) < 2 {
			return false
		}
		subcommand := strings.ToUpper(string(parsed.values[1].text))
		return subcommand == "SETINFO" || subcommand == "SETNAME"
	default:
		return false
	}
}

func firstValue(parsed value) string {
	if len(parsed.values) == 0 {
		return string(parsed.text)
	}
	return string(parsed.values[0].text)
}

func isPush(parsed value) bool {
	if parsed.kind == '>' {
		return true
	}
	if parsed.kind != '*' || len(parsed.values) == 0 {
		return false
	}
	switch strings.ToLower(firstValue(parsed)) {
	case "message", "pmessage", "smessage", "invalidate":
		return true
	default:
		return false
	}
}

func pushAcknowledges(operation string, parsed value) bool {
	switch strings.ToUpper(operation) {
	case "SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE", "UNSUBSCRIBE", "PUNSUBSCRIBE", "SUNSUBSCRIBE":
		return strings.EqualFold(operation, firstValue(parsed))
	default:
		return false
	}
}

func isErrorValue(parsed value) bool { return parsed.kind == '-' || parsed.kind == '!' }

func redactAuthentication(parsed value) value {
	result := cloneValue(parsed)
	command := strings.ToUpper(firstValue(result))
	if command == "AUTH" {
		for index := 1; index < len(result.values); index++ {
			result.values[index].text = []byte("[REDACTED]")
		}
	}
	if command == "HELLO" {
		for index := 1; index < len(result.values); index++ {
			if strings.EqualFold(string(result.values[index].text), "AUTH") {
				for redacted := index + 1; redacted < len(result.values) && redacted <= index+2; redacted++ {
					result.values[redacted].text = []byte("[REDACTED]")
				}
			}
		}
	}
	return result
}

func cloneValue(input value) value {
	result := value{kind: input.kind, text: append([]byte(nil), input.text...), values: make([]value, len(input.values))}
	for index, item := range input.values {
		result.values[index] = cloneValue(item)
	}
	return result
}

func messageForValue(parsed value, wireBytes int64, started, observed time.Time, limit int, request bool) model.TrafficMessage {
	converted := jsonValue(parsed)
	content, _ := json.MarshalIndent(converted, "", "  ")
	retained, encoding, captured, truncated := protocol.CaptureContent(content, limit)
	typeName := "response"
	if request {
		typeName = "command"
	}
	message := model.TrafficMessage{
		Type: typeName, OffsetMS: protocol.OffsetMS(started, observed), Summary: responseSummary(parsed), WireBytes: wireBytes,
		ContentBytes: int64(len(content)), CapturedBytes: captured, Truncated: truncated,
		Content: retained, ContentType: "application/json", Encoding: encoding,
	}
	if request {
		command, summary := commandSummary(parsed)
		message.Summary = summary
		message.Fields = append(message.Fields, model.TrafficMessageField{Name: "command", Value: command})
		if len(parsed.values) > 1 {
			message.Fields = append(message.Fields, model.TrafficMessageField{Name: "key", Value: printable(parsed.values[1].text)})
		}
	}
	return message
}

func responseSummary(parsed value) string {
	if isErrorValue(parsed) {
		return "ERROR " + printable(parsed.text)
	}
	if isPush(parsed) {
		return strings.ToUpper(firstValue(parsed))
	}
	switch parsed.kind {
	case '+':
		return printable(parsed.text)
	case '$', '=':
		return fmt.Sprintf("%d byte value", len(parsed.text))
	case '*', '~', '%':
		return fmt.Sprintf("%d value response", len(parsed.values))
	default:
		return "response"
	}
}

func jsonValue(parsed value) any {
	if len(parsed.values) > 0 || parsed.kind == '*' || parsed.kind == '~' || parsed.kind == '>' || parsed.kind == '%' || parsed.kind == '|' {
		values := make([]any, 0, len(parsed.values))
		for _, item := range parsed.values {
			values = append(values, jsonValue(item))
		}
		return values
	}
	if utf8.Valid(parsed.text) {
		return string(parsed.text)
	}
	return map[string]string{"base64": base64.StdEncoding.EncodeToString(parsed.text)}
}

func printable(content []byte) string {
	if utf8.Valid(content) {
		return string(content)
	}
	return base64.StdEncoding.EncodeToString(content)
}

func looksLikeTLS(content []byte) bool {
	return len(content) >= 3 && content[0] == 0x16 && content[1] == 0x03
}
