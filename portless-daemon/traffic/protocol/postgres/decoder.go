// Package postgres decodes bounded PostgreSQL frontend/backend protocol operations.
package postgres

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
)

const (
	sslRequestCode                     = 80877103
	cancelRequestCode                  = 80877102
	maximumPreparedStatements          = protocol.MaximumPendingOperations
	maximumPreparedStatementNameBytes  = 1 << 10
	maximumPreparedStatementParameters = 1 << 10
)

type packet struct {
	kind      byte
	payload   []byte
	wireBytes int64
}

type pendingOperation struct {
	operation      protocol.Operation
	limit          int
	columns        []string
	parameterTypes []uint32
}

type preparedStatement struct {
	operation      string
	background     bool
	parameterTypes []uint32
}

// Decoder incrementally decodes one PostgreSQL protocol v3 connection.
type Decoder struct {
	mu                        sync.Mutex
	config                    protocol.Config
	requestBuffer             []byte
	responseBuffer            []byte
	startup                   bool
	sslPending                bool
	pending                   []*pendingOperation
	current                   *pendingOperation
	prepared                  map[string]preparedStatement
	nextTransactionSequence   uint64
	activeTransactionSequence uint64
	state                     protocol.State
}

// New creates a PostgreSQL protocol decoder.
func New(config protocol.Config) protocol.Session {
	return &Decoder{
		config: config, startup: true, prepared: make(map[string]preparedStatement),
		state: protocol.State{Inspection: model.TrafficInspectionDecoded},
	}
}

// Observe consumes one unchanged TCP stream chunk.
func (d *Decoder) Observe(direction protocol.Direction, content []byte, observed time.Time) []protocol.Operation {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(content) == 0 || d.state.Inspection != model.TrafficInspectionDecoded {
		return nil
	}
	if direction == protocol.DirectionResponse && d.sslPending {
		decision := content[0]
		content = content[1:]
		d.sslPending = false
		if decision == 'S' {
			d.state = protocol.State{Inspection: model.TrafficInspectionEncrypted, Reason: "TLS-encrypted PostgreSQL traffic"}
			return nil
		}
		if decision != 'N' {
			d.state = protocol.State{Inspection: model.TrafficInspectionMalformed, Reason: "invalid PostgreSQL SSL negotiation response"}
			return nil
		}
		if len(content) == 0 {
			return nil
		}
	}
	if direction == protocol.DirectionRequest {
		return d.observeRequest(content, observed)
	}
	return d.observeResponse(content, observed)
}

// Close completes unfinished PostgreSQL operations as incomplete.
func (d *Decoder) Close(completed time.Time, connectionErr error) []protocol.Operation {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Inspection == model.TrafficInspectionDecoded && (len(d.requestBuffer) > 0 || len(d.responseBuffer) > 0) {
		d.state = protocol.State{Inspection: model.TrafficInspectionMalformed, Reason: "PostgreSQL connection closed during a protocol packet"}
	}
	if d.current != nil {
		d.pending = append(d.pending, d.current)
		d.current = nil
	}
	result := make([]protocol.Operation, 0, len(d.pending))
	for _, pending := range d.pending {
		pending.operation.CompletedAt = completed
		pending.operation.Outcome = model.TrafficTCPOutcomeIncomplete
		if d.activeTransactionSequence != 0 {
			pending.operation.TransactionSequence = d.activeTransactionSequence
		}
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

// State returns the current inspection result.
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
	if d.startup {
		if len(d.requestBuffer) >= 4 {
			length := int(binary.BigEndian.Uint32(d.requestBuffer[:4]))
			if length < 8 {
				d.fail(model.TrafficInspectionMalformed, "invalid PostgreSQL startup packet length")
				return completed
			}
			if length > protocol.MaximumPayloadLimit+8 {
				d.fail(model.TrafficInspectionLimited, "PostgreSQL startup packet exceeded the bounded inspection limit")
				return completed
			}
			if len(d.requestBuffer) < length {
				return completed
			}
			startup := append([]byte(nil), d.requestBuffer[:length]...)
			d.requestBuffer = d.requestBuffer[length:]
			code := binary.BigEndian.Uint32(startup[4:8])
			switch code {
			case sslRequestCode:
				d.sslPending = true
				return completed
			case cancelRequestCode:
				completed = append(completed, d.cancelOperation(startup, observed))
				return completed
			default:
				d.startup = false
			}
		}
	}
	packets, err := parsePackets(&d.requestBuffer)
	if err != nil {
		d.state = protocol.StateForError(err)
		return completed
	}
	for _, decoded := range packets {
		completed = append(completed, d.clientPacket(decoded, observed)...)
	}
	return completed
}

func (d *Decoder) observeResponse(content []byte, observed time.Time) []protocol.Operation {
	if !d.appendBuffer(&d.responseBuffer, content) {
		return nil
	}
	packets, err := parsePackets(&d.responseBuffer)
	if err != nil {
		d.state = protocol.StateForError(err)
		return nil
	}
	var completed []protocol.Operation
	for _, decoded := range packets {
		if decoded.kind == 'A' {
			completed = append(completed, d.notificationOperation(decoded, observed))
			continue
		}
		if len(d.pending) == 0 {
			if decoded.kind == 'E' {
				completed = append(completed, d.connectError(decoded, observed))
			}
			continue
		}
		pending := d.pending[0]
		if decoded.kind == 'C' && pending.operation.Name == "EXTENDED QUERY" {
			if command := cstring(decoded.payload); command != "" {
				pending.operation.Name = sqlOperation(command)
			}
		}
		message, columns, errorText := serverMessage(decoded, pending.operation.StartedAt, observed, pending.limit, pending.columns)
		if len(columns) > 0 {
			pending.columns = columns
		}
		if message.Type != "" {
			if protocol.AppendMessage(&pending.operation.ResponseMessages, message, pending.limit) {
				pending.operation.Inspection = model.TrafficInspectionLimited
				pending.operation.InspectionReason = "PostgreSQL response payload was truncated by the capture limit"
			}
		}
		pending.operation.ResponseBytes += decoded.wireBytes
		if errorText != "" {
			pending.operation.Error = errorText
			pending.operation.Outcome = model.TrafficTCPOutcomeError
		}
		if decoded.kind == 'Z' {
			d.pending = d.pending[1:]
			pending.operation.CompletedAt = observed
			d.assignTransaction(&pending.operation, decoded.payload)
			if pending.operation.Outcome == "" {
				pending.operation.Outcome = model.TrafficTCPOutcomeSuccess
			}
			completed = append(completed, pending.operation)
		}
	}
	return completed
}

func (d *Decoder) assignTransaction(operation *protocol.Operation, readyPayload []byte) {
	if len(readyPayload) == 0 {
		return
	}
	switch readyPayload[0] {
	case 'T', 'E':
		if d.activeTransactionSequence == 0 {
			d.nextTransactionSequence++
			d.activeTransactionSequence = d.nextTransactionSequence
		}
		operation.TransactionSequence = d.activeTransactionSequence
	case 'I':
		if d.activeTransactionSequence != 0 {
			operation.TransactionSequence = d.activeTransactionSequence
			d.activeTransactionSequence = 0
		}
	}
}

func (d *Decoder) clientPacket(decoded packet, observed time.Time) []protocol.Operation {
	if decoded.kind == 'p' || decoded.kind == 'X' {
		return nil
	}
	if decoded.kind == 'Q' {
		delete(d.prepared, "")
		query := cstring(decoded.payload)
		pending := d.newPending(sqlOperation(query), observed)
		pending.operation.Background = backgroundQuery(query)
		message := contentMessage("query", query, "text/x-sql", decoded, observed, observed, pending.limit)
		message.Summary = summarizeSQL(query)
		d.appendRequest(pending, message, decoded.wireBytes)
		return d.enqueue(pending, observed)
	}
	if d.current == nil {
		d.current = d.newPending("EXTENDED QUERY", observed)
	}
	pending := d.current
	if decoded.kind == 'P' {
		statement, query, rest := parseStrings(decoded.payload, 2)
		pending.parameterTypes = parseParameterTypes(rest)
		pending.operation.Background = backgroundQuery(query)
		if query != "" {
			pending.operation.Name = sqlOperation(query)
		}
		d.rememberPrepared(statement, query, pending.parameterTypes)
	}
	if decoded.kind == 'B' {
		_, statement, _ := parseStrings(decoded.payload, 2)
		d.resolvePrepared(pending, statement)
	}
	if decoded.kind == 'C' && len(decoded.payload) > 1 && decoded.payload[0] == 'S' {
		delete(d.prepared, cstring(decoded.payload[1:]))
	}
	message := clientMessage(decoded, pending.operation.StartedAt, observed, pending.limit, pending.parameterTypes)
	if message.Type != "" {
		d.appendRequest(pending, message, decoded.wireBytes)
	} else {
		pending.operation.RequestBytes += decoded.wireBytes
	}
	if decoded.kind == 'S' {
		d.current = nil
		return d.enqueue(pending, observed)
	}
	return nil
}

func (d *Decoder) rememberPrepared(name, query string, parameterTypes []uint32) {
	if len(name) > maximumPreparedStatementNameBytes || len(parameterTypes) > maximumPreparedStatementParameters {
		return
	}
	if _, exists := d.prepared[name]; !exists && len(d.prepared) >= maximumPreparedStatements {
		return
	}
	d.prepared[name] = preparedStatement{
		operation: sqlOperation(query), background: backgroundQuery(query),
		parameterTypes: append([]uint32(nil), parameterTypes...),
	}
}

func (d *Decoder) resolvePrepared(pending *pendingOperation, name string) {
	prepared, exists := d.prepared[name]
	if !exists {
		return
	}
	pending.operation.Name = prepared.operation
	pending.operation.Background = prepared.background
	pending.parameterTypes = prepared.parameterTypes
}

func (d *Decoder) newPending(name string, observed time.Time) *pendingOperation {
	policy := d.config.CapturePolicy()
	return &pendingOperation{operation: protocol.Operation{
		StartedAt: observed, Name: name, Inspection: model.TrafficInspectionDecoded,
		Policy: policy,
	}, limit: policy.CaptureLimit()}
}

func (d *Decoder) appendRequest(pending *pendingOperation, message model.TrafficMessage, wireBytes int64) {
	pending.operation.RequestBytes += wireBytes
	if protocol.AppendMessage(&pending.operation.RequestMessages, message, pending.limit) {
		pending.operation.Inspection = model.TrafficInspectionLimited
		pending.operation.InspectionReason = "PostgreSQL request payload was truncated by the capture limit"
	}
}

func (d *Decoder) enqueue(pending *pendingOperation, observed time.Time) []protocol.Operation {
	if len(d.pending) < protocol.MaximumPendingOperations {
		d.pending = append(d.pending, pending)
		return nil
	}
	oldest := d.pending[0]
	d.pending = append(d.pending[1:], pending)
	oldest.operation.CompletedAt = observed
	oldest.operation.Outcome = model.TrafficTCPOutcomeIncomplete
	oldest.operation.Inspection = model.TrafficInspectionLimited
	oldest.operation.InspectionReason = "PostgreSQL pipeline exceeded the bounded pending-operation limit"
	return []protocol.Operation{oldest.operation}
}

func (d *Decoder) cancelOperation(content []byte, observed time.Time) protocol.Operation {
	policy := d.config.CapturePolicy()
	message := model.TrafficMessage{Type: "cancel-request", Summary: "Cancel backend request", WireBytes: int64(len(content))}
	return protocol.Operation{
		StartedAt: observed, CompletedAt: observed, Name: "CANCEL", Inspection: model.TrafficInspectionDecoded,
		Outcome: model.TrafficTCPOutcomeOneWay, RequestMessages: []model.TrafficMessage{message}, RequestBytes: int64(len(content)), Policy: policy,
	}
}

func (d *Decoder) notificationOperation(decoded packet, observed time.Time) protocol.Operation {
	policy := d.config.CapturePolicy()
	message, _, _ := serverMessage(decoded, observed, observed, policy.CaptureLimit(), nil)
	return protocol.Operation{
		StartedAt: observed, CompletedAt: observed, Name: "NOTIFY", Inspection: model.TrafficInspectionDecoded,
		Outcome: model.TrafficTCPOutcomeOneWay, ResponseMessages: []model.TrafficMessage{message}, ResponseBytes: decoded.wireBytes, Policy: policy,
	}
}

func (d *Decoder) connectError(decoded packet, observed time.Time) protocol.Operation {
	policy := d.config.CapturePolicy()
	message, _, errorText := serverMessage(decoded, observed, observed, policy.CaptureLimit(), nil)
	return protocol.Operation{
		StartedAt: observed, CompletedAt: observed, Name: "CONNECT", Inspection: model.TrafficInspectionDecoded,
		Outcome: model.TrafficTCPOutcomeError, ResponseMessages: []model.TrafficMessage{message}, ResponseBytes: decoded.wireBytes, Error: errorText, Policy: policy,
	}
}

func (d *Decoder) appendBuffer(destination *[]byte, content []byte) bool {
	if len(*destination)+len(content) > protocol.MaximumPayloadLimit+64<<10 {
		d.fail(model.TrafficInspectionLimited, "PostgreSQL frame exceeded the bounded parser buffer")
		*destination = nil
		return false
	}
	*destination = append(*destination, content...)
	return true
}

func (d *Decoder) fail(inspection model.TrafficInspection, reason string) {
	d.state = protocol.State{Inspection: inspection, Reason: reason}
}

func parsePackets(buffer *[]byte) ([]packet, error) {
	var result []packet
	for len(*buffer) >= 5 {
		length := int(binary.BigEndian.Uint32((*buffer)[1:5]))
		if length < 4 {
			return nil, errors.New("invalid PostgreSQL packet length")
		}
		if length > protocol.MaximumPayloadLimit+4 {
			return nil, protocol.LimitError("PostgreSQL packet exceeded the bounded inspection limit")
		}
		total := 1 + length
		if len(*buffer) < total {
			break
		}
		result = append(result, packet{kind: (*buffer)[0], payload: append([]byte(nil), (*buffer)[5:total]...), wireBytes: int64(total)})
		*buffer = (*buffer)[total:]
	}
	return result, nil
}

func clientMessage(decoded packet, started, observed time.Time, limit int, parameterTypes []uint32) model.TrafficMessage {
	switch decoded.kind {
	case 'P':
		statement, query, _ := parseStrings(decoded.payload, 2)
		message := contentMessage("parse", query, "text/x-sql", decoded, started, observed, limit)
		message.Summary = "Parse " + summarizeSQL(query)
		message.Fields = append(message.Fields, model.TrafficMessageField{Name: "statement", Value: statement})
		return message
	case 'B':
		portal, statement, parameters := parseBind(decoded.payload, parameterTypes)
		content, _ := json.MarshalIndent(parameters, "", "  ")
		message := bytesMessage("bind", "Bind parameters", content, "application/json", decoded, started, observed, limit)
		message.Fields = append(message.Fields,
			model.TrafficMessageField{Name: "portal", Value: portal},
			model.TrafficMessageField{Name: "statement", Value: statement},
			model.TrafficMessageField{Name: "parameters", Value: fmt.Sprint(len(parameters))},
		)
		return message
	case 'D':
		return simpleMessage("describe", "Describe", decoded, started, observed)
	case 'E':
		portal := cstring(decoded.payload)
		message := simpleMessage("execute", "Execute", decoded, started, observed)
		message.Fields = append(message.Fields, model.TrafficMessageField{Name: "portal", Value: portal})
		return message
	case 'S':
		return simpleMessage("sync", "Sync", decoded, started, observed)
	case 'H':
		return simpleMessage("flush", "Flush", decoded, started, observed)
	case 'd':
		return bytesMessage("copy-data", "COPY data", decoded.payload, "application/octet-stream", decoded, started, observed, limit)
	case 'c':
		return simpleMessage("copy-done", "COPY done", decoded, started, observed)
	case 'f':
		return contentMessage("copy-fail", cstring(decoded.payload), "text/plain", decoded, started, observed, limit)
	default:
		return simpleMessage("frontend-message", fmt.Sprintf("Frontend %q", decoded.kind), decoded, started, observed)
	}
}

func serverMessage(decoded packet, started, observed time.Time, limit int, columns []string) (model.TrafficMessage, []string, string) {
	switch decoded.kind {
	case 'T':
		parsed := rowColumns(decoded.payload)
		message := simpleMessage("row-description", fmt.Sprintf("%d columns", len(parsed)), decoded, started, observed)
		for _, column := range parsed {
			message.Fields = append(message.Fields, model.TrafficMessageField{Name: "column", Value: column})
		}
		return message, parsed, ""
	case 'D':
		row := dataRow(decoded.payload, columns)
		content, _ := json.MarshalIndent(row, "", "  ")
		return bytesMessage("data-row", "Data row", content, "application/json", decoded, started, observed, limit), nil, ""
	case 'C':
		tag := cstring(decoded.payload)
		message := simpleMessage("command-complete", tag, decoded, started, observed)
		message.Fields = append(message.Fields, model.TrafficMessageField{Name: "command", Value: tag})
		return message, nil, ""
	case 'E', 'N':
		fields := errorFields(decoded.payload)
		summary := fields["M"]
		if summary == "" {
			summary = "PostgreSQL error"
		}
		kind := "notice"
		errorText := ""
		if decoded.kind == 'E' {
			kind = "error"
			errorText = summary
		}
		message := simpleMessage(kind, summary, decoded, started, observed)
		for _, key := range []string{"S", "C", "M", "D", "H", "W"} {
			if value := fields[key]; value != "" {
				message.Fields = append(message.Fields, model.TrafficMessageField{Name: errorFieldName(key), Value: value})
			}
		}
		return message, nil, errorText
	case 'Z':
		message := simpleMessage("ready", "Ready for query", decoded, started, observed)
		if len(decoded.payload) > 0 {
			message.Fields = append(message.Fields, model.TrafficMessageField{Name: "transactionStatus", Value: string(decoded.payload[0])})
		}
		return message, nil, ""
	case 'A':
		processID := uint32(0)
		payload := decoded.payload
		if len(payload) >= 4 {
			processID = binary.BigEndian.Uint32(payload[:4])
			payload = payload[4:]
		}
		channel, value, _ := parseStrings(payload, 2)
		message := contentMessage("notification", value, protocol.ContentType([]byte(value), "text/plain"), decoded, started, observed, limit)
		message.Summary = "NOTIFY " + channel
		message.Fields = append(message.Fields,
			model.TrafficMessageField{Name: "channel", Value: channel},
			model.TrafficMessageField{Name: "processId", Value: fmt.Sprint(processID)},
		)
		return message, nil, ""
	case 'd':
		return bytesMessage("copy-data", "COPY data", decoded.payload, "application/octet-stream", decoded, started, observed, limit), nil, ""
	default:
		return simpleMessage(serverMessageType(decoded.kind), serverMessageSummary(decoded.kind), decoded, started, observed), nil, ""
	}
}

func contentMessage(kind, content, contentType string, decoded packet, started, observed time.Time, limit int) model.TrafficMessage {
	return bytesMessage(kind, content, []byte(content), contentType, decoded, started, observed, limit)
}

func bytesMessage(kind, summary string, content []byte, contentType string, decoded packet, started, observed time.Time, limit int) model.TrafficMessage {
	retained, encoding, captured, truncated := protocol.CaptureContent(content, limit)
	return model.TrafficMessage{
		Type: kind, OffsetMS: protocol.OffsetMS(started, observed), Summary: summary, WireBytes: decoded.wireBytes,
		ContentBytes: int64(len(content)), CapturedBytes: captured, Truncated: truncated,
		Content: retained, ContentType: contentType, Encoding: encoding,
	}
}

func simpleMessage(kind, summary string, decoded packet, started, observed time.Time) model.TrafficMessage {
	return model.TrafficMessage{Type: kind, OffsetMS: protocol.OffsetMS(started, observed), Summary: summary, WireBytes: decoded.wireBytes}
}

func parseStrings(content []byte, count int) (string, string, []byte) {
	values := make([]string, 0, count)
	rest := content
	for len(values) < count {
		index := bytes.IndexByte(rest, 0)
		if index < 0 {
			values = append(values, string(rest))
			rest = nil
			break
		}
		values = append(values, string(rest[:index]))
		rest = rest[index+1:]
	}
	for len(values) < 2 {
		values = append(values, "")
	}
	return values[0], values[1], rest
}

func parseParameterTypes(content []byte) []uint32 {
	if len(content) < 2 {
		return nil
	}
	count := int(binary.BigEndian.Uint16(content[:2]))
	content = content[2:]
	if count == 0 || len(content) < count*4 {
		return nil
	}
	types := make([]uint32, count)
	for index := range types {
		types[index] = binary.BigEndian.Uint32(content[index*4:])
	}
	return types
}

func parseBind(content []byte, parameterTypes []uint32) (string, string, []any) {
	portal, statement, rest := parseStrings(content, 2)
	if len(rest) < 2 {
		return portal, statement, nil
	}
	formatCount := int(binary.BigEndian.Uint16(rest[:2]))
	rest = rest[2:]
	if len(rest) < formatCount*2+2 {
		return portal, statement, nil
	}
	formats := make([]uint16, formatCount)
	for index := range formats {
		formats[index] = binary.BigEndian.Uint16(rest[index*2:])
	}
	rest = rest[formatCount*2:]
	parameterCount := int(binary.BigEndian.Uint16(rest[:2]))
	rest = rest[2:]
	parameters := make([]any, 0, parameterCount)
	for index := 0; index < parameterCount; index++ {
		if len(rest) < 4 {
			break
		}
		length := int(int32(binary.BigEndian.Uint32(rest[:4])))
		rest = rest[4:]
		if length < 0 {
			parameters = append(parameters, nil)
			continue
		}
		if len(rest) < length {
			break
		}
		value := rest[:length]
		rest = rest[length:]
		format := uint16(0)
		if len(formats) == 1 {
			format = formats[0]
		} else if index < len(formats) {
			format = formats[index]
		}
		parameterType := uint32(0)
		if index < len(parameterTypes) {
			parameterType = parameterTypes[index]
		}
		parameters = append(parameters, decodeParameter(value, format, parameterType))
	}
	return portal, statement, parameters
}

func decodeParameter(value []byte, format uint16, parameterType uint32) any {
	if format == 0 {
		if utf8.Valid(value) {
			return string(value)
		}
		return map[string]string{"base64": base64.StdEncoding.EncodeToString(value)}
	}
	switch parameterType {
	case 16: // bool
		if len(value) == 1 {
			return value[0] != 0
		}
	case 20: // int8
		if len(value) == 8 {
			return int64(binary.BigEndian.Uint64(value))
		}
	case 21: // int2
		if len(value) == 2 {
			return int16(binary.BigEndian.Uint16(value))
		}
	case 23: // int4
		if len(value) == 4 {
			return int32(binary.BigEndian.Uint32(value))
		}
	case 26: // oid
		if len(value) == 4 {
			return binary.BigEndian.Uint32(value)
		}
	case 18, 19, 25, 1042, 1043, 114: // char, name, text, bpchar, varchar, json
		if utf8.Valid(value) {
			return string(value)
		}
	case 3802: // jsonb
		if len(value) > 1 && value[0] == 1 && utf8.Valid(value[1:]) {
			return string(value[1:])
		}
	case 2950: // uuid
		if len(value) == 16 {
			return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
		}
	}
	return map[string]string{"base64": base64.StdEncoding.EncodeToString(value)}
}

func rowColumns(content []byte) []string {
	if len(content) < 2 {
		return nil
	}
	count := int(binary.BigEndian.Uint16(content[:2]))
	rest := content[2:]
	columns := make([]string, 0, count)
	for index := 0; index < count; index++ {
		end := bytes.IndexByte(rest, 0)
		if end < 0 || len(rest) < end+1+18 {
			break
		}
		columns = append(columns, string(rest[:end]))
		rest = rest[end+1+18:]
	}
	return columns
}

func dataRow(content []byte, columns []string) any {
	if len(content) < 2 {
		return []any{}
	}
	count := int(binary.BigEndian.Uint16(content[:2]))
	rest := content[2:]
	values := make([]any, 0, count)
	for index := 0; index < count; index++ {
		if len(rest) < 4 {
			break
		}
		length := int(int32(binary.BigEndian.Uint32(rest[:4])))
		rest = rest[4:]
		if length < 0 {
			values = append(values, nil)
			continue
		}
		if len(rest) < length {
			break
		}
		value := rest[:length]
		rest = rest[length:]
		if utf8.Valid(value) {
			values = append(values, string(value))
		} else {
			values = append(values, map[string]string{"base64": base64.StdEncoding.EncodeToString(value)})
		}
	}
	if len(columns) != len(values) {
		return values
	}
	result := make(map[string]any, len(values))
	for index, value := range values {
		result[columns[index]] = value
	}
	return result
}

func errorFields(content []byte) map[string]string {
	result := make(map[string]string)
	for len(content) > 1 && content[0] != 0 {
		key := string(content[0])
		content = content[1:]
		end := bytes.IndexByte(content, 0)
		if end < 0 {
			break
		}
		result[key] = string(content[:end])
		content = content[end+1:]
	}
	return result
}

func errorFieldName(key string) string {
	switch key {
	case "S":
		return "severity"
	case "C":
		return "code"
	case "M":
		return "message"
	case "D":
		return "detail"
	case "H":
		return "hint"
	case "W":
		return "where"
	default:
		return key
	}
}

func sqlOperation(query string) string {
	fields := strings.Fields(strings.TrimSpace(query))
	if len(fields) == 0 {
		return "QUERY"
	}
	operation := strings.ToUpper(fields[0])
	if operation == "WITH" {
		return "QUERY"
	}
	if len(operation) > 24 {
		return "QUERY"
	}
	return operation
}

func backgroundQuery(query string) bool {
	query = strings.TrimSpace(query)
	query = strings.TrimSpace(strings.TrimRight(query, ";"))
	normalized := strings.ToUpper(strings.Join(strings.Fields(query), " "))
	if normalized == "" || normalized == "SELECT 1" || normalized == "SELECT 1::INTEGER" {
		return true
	}
	switch normalized {
	case "SHOW TRANSACTION ISOLATION LEVEL",
		"SHOW DEFAULT_TRANSACTION_ISOLATION",
		"SHOW TRANSACTION_READ_ONLY",
		"SHOW STANDARD_CONFORMING_STRINGS":
		return true
	}
	for _, setting := range []string{
		"APPLICATION_NAME",
		"CLIENT_ENCODING",
		"DATESTYLE",
		"EXTRA_FLOAT_DIGITS",
		"STANDARD_CONFORMING_STRINGS",
		"TIME ZONE",
		"TIMEZONE",
	} {
		prefix := "SET " + setting
		if normalized == prefix || strings.HasPrefix(normalized, prefix+" ") || strings.HasPrefix(normalized, prefix+"=") {
			return true
		}
	}
	return false
}

func summarizeSQL(query string) string {
	query = strings.Join(strings.Fields(query), " ")
	if len(query) > 160 {
		return query[:160] + "…"
	}
	return query
}

func cstring(content []byte) string {
	if index := bytes.IndexByte(content, 0); index >= 0 {
		content = content[:index]
	}
	return string(content)
}

func serverMessageType(kind byte) string {
	switch kind {
	case '1':
		return "parse-complete"
	case '2':
		return "bind-complete"
	case '3':
		return "close-complete"
	case 'n':
		return "no-data"
	case 's':
		return "portal-suspended"
	case 'I':
		return "empty-query"
	case 'G':
		return "copy-in"
	case 'H':
		return "copy-out"
	case 'W':
		return "copy-both"
	case 'c':
		return "copy-done"
	default:
		return "backend-message"
	}
}

func serverMessageSummary(kind byte) string {
	words := strings.Fields(strings.ReplaceAll(serverMessageType(kind), "-", " "))
	for index, word := range words {
		if word != "" {
			words[index] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
