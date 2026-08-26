// Package mysql decodes bounded operations from the MySQL classic protocol.
package mysql

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
)

const (
	clientCompress     = uint32(0x00000020)
	clientSSL          = uint32(0x00000800)
	clientDeprecateEOF = uint32(0x01000000)
)

type packet struct {
	sequence  byte
	payload   []byte
	wireBytes int64
}

type resultPhase uint8

const (
	resultInitial resultPhase = iota
	resultColumns
	resultRows
	resultPrepareDefinitions
)

type pendingOperation struct {
	operation            protocol.Operation
	limit                int
	command              byte
	phase                resultPhase
	columnCount          int
	remainingDefinitions int
	columns              []string
	preparedSQL          string
	columnTerminatorSeen bool
}

// Decoder incrementally decodes one MySQL classic-protocol connection.
type Decoder struct {
	mu                  sync.Mutex
	config              protocol.Config
	requestBuffer       []byte
	responseBuffer      []byte
	serverHandshakeSeen bool
	clientHandshakeSeen bool
	ready               bool
	capabilities        uint32
	pending             []*pendingOperation
	prepared            map[uint32]string
	state               protocol.State
}

// New creates a MySQL classic-protocol decoder.
func New(config protocol.Config) protocol.Session {
	return &Decoder{config: config, prepared: make(map[uint32]string), state: protocol.State{Inspection: model.TrafficInspectionDecoded}}
}

// Observe consumes one unchanged TCP stream chunk.
func (d *Decoder) Observe(direction protocol.Direction, content []byte, observed time.Time) []protocol.Operation {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(content) == 0 || d.state.Inspection != model.TrafficInspectionDecoded {
		return nil
	}
	buffer := &d.responseBuffer
	if direction == protocol.DirectionRequest {
		buffer = &d.requestBuffer
	}
	if len(*buffer)+len(content) > protocol.MaximumPayloadLimit+64<<10 {
		d.state = protocol.State{Inspection: model.TrafficInspectionLimited, Reason: "MySQL packet exceeded the bounded parser buffer"}
		*buffer = nil
		return nil
	}
	*buffer = append(*buffer, content...)
	packets, err := parsePackets(buffer)
	if err != nil {
		d.state = protocol.StateForError(err)
		return nil
	}
	var completed []protocol.Operation
	for _, decoded := range packets {
		if !d.ready {
			completed = append(completed, d.handshakePacket(direction, decoded, observed)...)
			if d.state.Inspection != model.TrafficInspectionDecoded {
				break
			}
			continue
		}
		if direction == protocol.DirectionRequest {
			completed = append(completed, d.clientPacket(decoded, observed)...)
		} else {
			completed = append(completed, d.serverPacket(decoded, observed)...)
		}
	}
	return completed
}

// Close completes unfinished MySQL operations as incomplete.
func (d *Decoder) Close(completed time.Time, connectionErr error) []protocol.Operation {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Inspection == model.TrafficInspectionDecoded && (len(d.requestBuffer) > 0 || len(d.responseBuffer) > 0) {
		d.state = protocol.State{Inspection: model.TrafficInspectionMalformed, Reason: "MySQL connection closed during a protocol packet"}
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

// State returns the current inspection result.
func (d *Decoder) State() protocol.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *Decoder) handshakePacket(direction protocol.Direction, decoded packet, observed time.Time) []protocol.Operation {
	if direction == protocol.DirectionResponse {
		if !d.serverHandshakeSeen {
			d.serverHandshakeSeen = true
			if len(decoded.payload) > 0 && decoded.payload[0] == 0xff {
				return []protocol.Operation{d.connectionError(decoded, observed)}
			}
			return nil
		}
		if len(decoded.payload) > 0 && decoded.payload[0] == 0xff {
			return []protocol.Operation{d.connectionError(decoded, observed)}
		}
		if len(decoded.payload) > 0 && decoded.payload[0] == 0x00 {
			d.ready = true
		}
		return nil
	}
	if !d.serverHandshakeSeen || d.clientHandshakeSeen || len(decoded.payload) < 4 {
		return nil
	}
	d.clientHandshakeSeen = true
	d.capabilities = binary.LittleEndian.Uint32(decoded.payload[:4])
	if d.capabilities&clientSSL != 0 && len(decoded.payload) <= 36 {
		d.state = protocol.State{Inspection: model.TrafficInspectionEncrypted, Reason: "TLS-encrypted MySQL traffic"}
		return nil
	}
	if d.capabilities&clientCompress != 0 {
		d.state = protocol.State{Inspection: model.TrafficInspectionUnsupported, Reason: "MySQL compressed protocol is not inspectable"}
	}
	return nil
}

func (d *Decoder) clientPacket(decoded packet, observed time.Time) []protocol.Operation {
	if len(decoded.payload) == 0 {
		return nil
	}
	command := decoded.payload[0]
	payload := decoded.payload[1:]
	if command == 0x19 && len(payload) >= 4 { // COM_STMT_CLOSE has no response.
		statementID := binary.LittleEndian.Uint32(payload[:4])
		delete(d.prepared, statementID)
		return []protocol.Operation{d.oneWay("CLOSE STATEMENT", command, decoded, observed, statementID)}
	}
	pending := d.newPending(command, payload, decoded, observed)
	if pending == nil {
		return nil
	}
	if len(d.pending) < protocol.MaximumPendingOperations {
		d.pending = append(d.pending, pending)
		return nil
	}
	oldest := d.pending[0]
	d.pending = append(d.pending[1:], pending)
	oldest.operation.CompletedAt = observed
	oldest.operation.Outcome = model.TrafficTCPOutcomeIncomplete
	oldest.operation.Inspection = model.TrafficInspectionLimited
	oldest.operation.InspectionReason = "MySQL pipeline exceeded the bounded pending-operation limit"
	return []protocol.Operation{oldest.operation}
}

func (d *Decoder) newPending(command byte, payload []byte, decoded packet, observed time.Time) *pendingOperation {
	policy := d.config.CapturePolicy()
	captureLimit := policy.CaptureLimit()
	name, summary, content, contentType, fields := d.describeCommand(command, payload)
	message := messageWithContent("command", summary, content, contentType, decoded, observed, observed, captureLimit)
	message.Fields = append(message.Fields, fields...)
	operation := protocol.Operation{
		StartedAt: observed, Name: name, Inspection: model.TrafficInspectionDecoded,
		RequestBytes: decoded.wireBytes, Policy: policy,
	}
	if protocol.AppendMessage(&operation.RequestMessages, message, captureLimit) {
		operation.Inspection = model.TrafficInspectionLimited
		operation.InspectionReason = "MySQL request payload was truncated by the capture limit"
	}
	pending := &pendingOperation{operation: operation, limit: captureLimit, command: command}
	if command == 0x16 {
		pending.preparedSQL = string(payload)
	}
	return pending
}

func (d *Decoder) serverPacket(decoded packet, observed time.Time) []protocol.Operation {
	if len(d.pending) == 0 {
		return nil
	}
	pending := d.pending[0]
	pending.operation.ResponseBytes += decoded.wireBytes
	message, terminal, errorText := d.describeResponse(pending, decoded, observed)
	if message.Type != "" {
		if protocol.AppendMessage(&pending.operation.ResponseMessages, message, pending.limit) {
			pending.operation.Inspection = model.TrafficInspectionLimited
			pending.operation.InspectionReason = "MySQL response payload was truncated by the capture limit"
		}
	}
	if errorText != "" {
		pending.operation.Error = errorText
		pending.operation.Outcome = model.TrafficTCPOutcomeError
	}
	if !terminal {
		return nil
	}
	d.pending = d.pending[1:]
	pending.operation.CompletedAt = observed
	if pending.operation.Outcome == "" {
		pending.operation.Outcome = model.TrafficTCPOutcomeSuccess
	}
	return []protocol.Operation{pending.operation}
}

func (d *Decoder) describeResponse(pending *pendingOperation, decoded packet, observed time.Time) (model.TrafficMessage, bool, string) {
	payload := decoded.payload
	if len(payload) == 0 {
		return simpleMessage("empty", "Empty MySQL packet", decoded, pending.operation.StartedAt, observed), false, ""
	}
	if payload[0] == 0xff {
		code, state, message := parseError(payload)
		result := simpleMessage("error", message, decoded, pending.operation.StartedAt, observed)
		result.Fields = append(result.Fields, model.TrafficMessageField{Name: "code", Value: fmt.Sprint(code)})
		if state != "" {
			result.Fields = append(result.Fields, model.TrafficMessageField{Name: "sqlState", Value: state})
		}
		return result, true, message
	}
	if pending.phase == resultInitial {
		if pending.command == 0x16 && payload[0] == 0x00 && len(payload) >= 12 {
			statementID := binary.LittleEndian.Uint32(payload[1:5])
			columns := int(binary.LittleEndian.Uint16(payload[5:7]))
			parameters := int(binary.LittleEndian.Uint16(payload[7:9]))
			d.prepared[statementID] = pending.preparedSQL
			pending.remainingDefinitions = columns + parameters
			pending.phase = resultPrepareDefinitions
			message := simpleMessage("prepare-ok", "Prepared statement", decoded, pending.operation.StartedAt, observed)
			message.Fields = append(message.Fields,
				model.TrafficMessageField{Name: "statementId", Value: fmt.Sprint(statementID)},
				model.TrafficMessageField{Name: "parameters", Value: fmt.Sprint(parameters)},
				model.TrafficMessageField{Name: "columns", Value: fmt.Sprint(columns)},
			)
			return message, pending.remainingDefinitions == 0, ""
		}
		if payload[0] == 0x00 {
			return okMessage(decoded, pending.operation.StartedAt, observed), true, ""
		}
		columnCount, consumed, ok := readLengthEncodedInteger(payload)
		if !ok || consumed != len(payload) {
			return rawMessage("response", "MySQL response", payload, decoded, pending.operation.StartedAt, observed, pending.limit), false, ""
		}
		pending.columnCount = int(columnCount)
		pending.remainingDefinitions = int(columnCount)
		pending.phase = resultColumns
		message := simpleMessage("result-set", fmt.Sprintf("%d columns", columnCount), decoded, pending.operation.StartedAt, observed)
		message.Fields = append(message.Fields, model.TrafficMessageField{Name: "columns", Value: fmt.Sprint(columnCount)})
		return message, columnCount == 0, ""
	}
	if pending.phase == resultPrepareDefinitions {
		if isEOFPacket(payload) || isOKPacket(payload) {
			return simpleMessage("definition-end", "Definition metadata complete", decoded, pending.operation.StartedAt, observed), pending.remainingDefinitions == 0, ""
		}
		if pending.remainingDefinitions > 0 {
			pending.remainingDefinitions--
		}
		return columnMessage(decoded, pending.operation.StartedAt, observed), pending.remainingDefinitions == 0, ""
	}
	if pending.phase == resultColumns {
		if pending.remainingDefinitions > 0 {
			pending.remainingDefinitions--
			column := columnName(payload)
			pending.columns = append(pending.columns, column)
			if pending.remainingDefinitions == 0 {
				pending.phase = resultRows
			}
			return columnMessage(decoded, pending.operation.StartedAt, observed), false, ""
		}
		pending.phase = resultRows
	}
	if pending.phase == resultRows {
		if !pending.columnTerminatorSeen && (isEOFPacket(payload) || isOKPacket(payload)) {
			pending.columnTerminatorSeen = true
			return simpleMessage("column-end", "Column metadata complete", decoded, pending.operation.StartedAt, observed), false, ""
		}
		pending.columnTerminatorSeen = true
		if isEOFPacket(payload) {
			return simpleMessage("result-end", "Result set complete", decoded, pending.operation.StartedAt, observed), true, ""
		}
		if row, ok := parseTextRow(payload, pending.columnCount); ok {
			content := any(row)
			if len(pending.columns) == len(row) {
				mapped := make(map[string]any, len(row))
				for index, value := range row {
					mapped[pending.columns[index]] = value
				}
				content = mapped
			}
			encoded, _ := json.MarshalIndent(content, "", "  ")
			return messageWithContent("row", "Data row", encoded, "application/json", decoded, pending.operation.StartedAt, observed, pending.limit), false, ""
		}
		if isOKPacket(payload) && d.capabilities&clientDeprecateEOF != 0 {
			return okMessage(decoded, pending.operation.StartedAt, observed), true, ""
		}
		return rawMessage("row", "Binary or undecoded row", payload, decoded, pending.operation.StartedAt, observed, pending.limit), false, ""
	}
	return rawMessage("response", "MySQL response", payload, decoded, pending.operation.StartedAt, observed, pending.limit), false, ""
}

func (d *Decoder) describeCommand(command byte, payload []byte) (string, string, []byte, string, []model.TrafficMessageField) {
	switch command {
	case 0x03:
		query := string(payload)
		return sqlOperation(query), summarizeSQL(query), payload, "text/x-sql", nil
	case 0x02:
		return "USE", "Use database " + string(payload), payload, "text/plain", []model.TrafficMessageField{{Name: "database", Value: string(payload)}}
	case 0x0e:
		return "PING", "Ping", nil, "", nil
	case 0x16:
		query := string(payload)
		return "PREPARE", summarizeSQL(query), payload, "text/x-sql", nil
	case 0x17:
		if len(payload) < 4 {
			return "EXECUTE", "Execute prepared statement", payload, "application/octet-stream", nil
		}
		statementID := binary.LittleEndian.Uint32(payload[:4])
		query := d.prepared[statementID]
		name := "EXECUTE"
		if query != "" {
			name = sqlOperation(query)
		}
		content := payload[4:]
		return name, "Execute " + summarizeSQL(query), content, "application/octet-stream", []model.TrafficMessageField{{Name: "statementId", Value: fmt.Sprint(statementID)}, {Name: "statement", Value: query}}
	case 0x11:
		username := nulTerminatedString(payload)
		return "CHANGE USER", "Change user " + username, nil, "", []model.TrafficMessageField{{Name: "username", Value: username}, {Name: "authentication", Value: "[REDACTED]"}}
	default:
		return commandName(command), commandName(command), payload, "application/octet-stream", nil
	}
}

func nulTerminatedString(payload []byte) string {
	for index, value := range payload {
		if value == 0 {
			return string(payload[:index])
		}
	}
	return string(payload)
}

func (d *Decoder) oneWay(name string, command byte, decoded packet, observed time.Time, statementID uint32) protocol.Operation {
	policy := d.config.CapturePolicy()
	message := simpleMessage(strings.ToLower(commandName(command)), name, decoded, observed, observed)
	message.Fields = append(message.Fields, model.TrafficMessageField{Name: "statementId", Value: fmt.Sprint(statementID)})
	return protocol.Operation{
		StartedAt: observed, CompletedAt: observed, Name: name, Inspection: model.TrafficInspectionDecoded,
		Outcome: model.TrafficTCPOutcomeOneWay, RequestMessages: []model.TrafficMessage{message}, RequestBytes: decoded.wireBytes, Policy: policy,
	}
}

func (d *Decoder) connectionError(decoded packet, observed time.Time) protocol.Operation {
	policy := d.config.CapturePolicy()
	code, state, message := parseError(decoded.payload)
	response := simpleMessage("error", message, decoded, observed, observed)
	response.Fields = append(response.Fields, model.TrafficMessageField{Name: "code", Value: fmt.Sprint(code)})
	if state != "" {
		response.Fields = append(response.Fields, model.TrafficMessageField{Name: "sqlState", Value: state})
	}
	return protocol.Operation{
		StartedAt: observed, CompletedAt: observed, Name: "CONNECT", Inspection: model.TrafficInspectionDecoded,
		Outcome: model.TrafficTCPOutcomeError, ResponseMessages: []model.TrafficMessage{response}, ResponseBytes: decoded.wireBytes, Error: message, Policy: policy,
	}
}

func parsePackets(buffer *[]byte) ([]packet, error) {
	var result []packet
	for len(*buffer) >= 4 {
		length := int((*buffer)[0]) | int((*buffer)[1])<<8 | int((*buffer)[2])<<16
		if length > protocol.MaximumPayloadLimit {
			return nil, protocol.LimitError("MySQL packet exceeded the bounded inspection limit")
		}
		total := length + 4
		if len(*buffer) < total {
			break
		}
		result = append(result, packet{sequence: (*buffer)[3], payload: append([]byte(nil), (*buffer)[4:total]...), wireBytes: int64(total)})
		*buffer = (*buffer)[total:]
	}
	return result, nil
}

func messageWithContent(kind, summary string, content []byte, contentType string, decoded packet, started, observed time.Time, limit int) model.TrafficMessage {
	retained, encoding, captured, truncated := protocol.CaptureContent(content, limit)
	return model.TrafficMessage{
		Type: kind, OffsetMS: protocol.OffsetMS(started, observed), Summary: summary, WireBytes: decoded.wireBytes,
		ContentBytes: int64(len(content)), CapturedBytes: captured, Truncated: truncated,
		Content: retained, ContentType: protocol.ContentType(content, contentType), Encoding: encoding,
	}
}

func rawMessage(kind, summary string, content []byte, decoded packet, started, observed time.Time, limit int) model.TrafficMessage {
	return messageWithContent(kind, summary, content, "application/octet-stream", decoded, started, observed, limit)
}

func simpleMessage(kind, summary string, decoded packet, started, observed time.Time) model.TrafficMessage {
	return model.TrafficMessage{Type: kind, OffsetMS: protocol.OffsetMS(started, observed), Summary: summary, WireBytes: decoded.wireBytes}
}

func okMessage(decoded packet, started, observed time.Time) model.TrafficMessage {
	message := simpleMessage("ok", "OK", decoded, started, observed)
	if len(decoded.payload) > 1 {
		if affected, consumed, ok := readLengthEncodedInteger(decoded.payload[1:]); ok {
			message.Fields = append(message.Fields, model.TrafficMessageField{Name: "affectedRows", Value: fmt.Sprint(affected)})
			if inserted, _, insertedOK := readLengthEncodedInteger(decoded.payload[1+consumed:]); insertedOK {
				message.Fields = append(message.Fields, model.TrafficMessageField{Name: "lastInsertId", Value: fmt.Sprint(inserted)})
			}
		}
	}
	return message
}

func columnMessage(decoded packet, started, observed time.Time) model.TrafficMessage {
	name := columnName(decoded.payload)
	message := simpleMessage("column", name, decoded, started, observed)
	if name != "" {
		message.Fields = append(message.Fields, model.TrafficMessageField{Name: "name", Value: name})
	}
	return message
}

func columnName(payload []byte) string {
	// Column definitions contain six length-encoded strings; the fifth is the
	// display name. Return an empty name when any framing is incomplete.
	rest := payload
	for index := 0; index < 6; index++ {
		value, consumed, ok := readLengthEncodedString(rest)
		if !ok {
			return ""
		}
		if index == 4 {
			return value
		}
		rest = rest[consumed:]
	}
	return ""
}

func parseTextRow(payload []byte, count int) ([]any, bool) {
	rest := payload
	result := make([]any, 0, count)
	for index := 0; index < count; index++ {
		if len(rest) == 0 {
			return nil, false
		}
		if rest[0] == 0xfb {
			result = append(result, nil)
			rest = rest[1:]
			continue
		}
		length, consumed, ok := readLengthEncodedInteger(rest)
		if !ok || length > uint64(len(rest)-consumed) {
			return nil, false
		}
		value := rest[consumed : consumed+int(length)]
		rest = rest[consumed+int(length):]
		if utf8.Valid(value) {
			result = append(result, string(value))
		} else {
			result = append(result, map[string]string{"base64": base64.StdEncoding.EncodeToString(value)})
		}
	}
	return result, len(rest) == 0
}

func readLengthEncodedString(payload []byte) (string, int, bool) {
	length, consumed, ok := readLengthEncodedInteger(payload)
	if !ok || length > uint64(len(payload)-consumed) {
		return "", 0, false
	}
	return string(payload[consumed : consumed+int(length)]), consumed + int(length), true
}

func readLengthEncodedInteger(payload []byte) (uint64, int, bool) {
	if len(payload) == 0 {
		return 0, 0, false
	}
	switch payload[0] {
	case 0xfc:
		if len(payload) < 3 {
			return 0, 0, false
		}
		return uint64(binary.LittleEndian.Uint16(payload[1:3])), 3, true
	case 0xfd:
		if len(payload) < 4 {
			return 0, 0, false
		}
		return uint64(payload[1]) | uint64(payload[2])<<8 | uint64(payload[3])<<16, 4, true
	case 0xfe:
		if len(payload) < 9 {
			return 0, 0, false
		}
		return binary.LittleEndian.Uint64(payload[1:9]), 9, true
	case 0xfb, 0xff:
		return 0, 0, false
	default:
		return uint64(payload[0]), 1, true
	}
}

func parseError(payload []byte) (uint16, string, string) {
	if len(payload) < 3 || payload[0] != 0xff {
		return 0, "", "MySQL error"
	}
	code := binary.LittleEndian.Uint16(payload[1:3])
	rest := payload[3:]
	state := ""
	if len(rest) >= 6 && rest[0] == '#' {
		state = string(rest[1:6])
		rest = rest[6:]
	}
	return code, state, string(rest)
}

func isEOFPacket(payload []byte) bool {
	return len(payload) > 0 && payload[0] == 0xfe && len(payload) < 9
}

func isOKPacket(payload []byte) bool { return len(payload) >= 7 && payload[0] == 0x00 }

func commandName(command byte) string {
	switch command {
	case 0x01:
		return "QUIT"
	case 0x02:
		return "INIT DB"
	case 0x03:
		return "QUERY"
	case 0x0e:
		return "PING"
	case 0x11:
		return "CHANGE USER"
	case 0x16:
		return "PREPARE"
	case 0x17:
		return "EXECUTE"
	case 0x19:
		return "CLOSE STATEMENT"
	default:
		return fmt.Sprintf("COMMAND 0x%02x", command)
	}
}

func sqlOperation(query string) string {
	fields := strings.Fields(strings.TrimSpace(query))
	if len(fields) == 0 {
		return "QUERY"
	}
	operation := strings.ToUpper(fields[0])
	if operation == "WITH" || len(operation) > 24 {
		return "QUERY"
	}
	return operation
}

func summarizeSQL(query string) string {
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return "prepared statement"
	}
	if len(query) > 160 {
		return query[:160] + "…"
	}
	return query
}
