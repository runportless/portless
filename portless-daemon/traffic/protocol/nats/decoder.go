// Package nats decodes bounded operations from the NATS client protocol.
package nats

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
)

type payloadFrame struct {
	command      string
	arguments    []string
	headerBytes  int
	payloadBytes int
	wireHeader   int
}

type stream struct {
	buffer  []byte
	pending *payloadFrame
}

type frame struct {
	command   string
	arguments []string
	headers   []model.TrafficMessageField
	payload   []byte
	wireBytes int64
}

// Decoder incrementally decodes one NATS client connection.
type Decoder struct {
	mu       sync.Mutex
	config   protocol.Config
	request  stream
	response stream
	state    protocol.State
	observed bool
}

// New creates a NATS protocol decoder.
func New(config protocol.Config) protocol.Session {
	return &Decoder{config: config, state: protocol.State{Inspection: model.TrafficInspectionDecoded}}
}

// Observe consumes one unchanged TCP stream chunk.
func (d *Decoder) Observe(direction protocol.Direction, content []byte, observed time.Time) []protocol.Operation {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(content) == 0 || d.state.Inspection != model.TrafficInspectionDecoded {
		return nil
	}
	if !d.observed && looksLikeTLS(content) {
		d.state = protocol.State{Inspection: model.TrafficInspectionEncrypted, Reason: "TLS-encrypted NATS traffic"}
		return nil
	}
	d.observed = true
	current := &d.response
	if direction == protocol.DirectionRequest {
		current = &d.request
	}
	frames, err := d.parse(current, content, direction)
	if err != nil {
		d.state = protocol.StateForError(err)
		return nil
	}
	operations := make([]protocol.Operation, 0, len(frames))
	for _, decoded := range frames {
		if operation, ok := d.operation(direction, decoded, observed); ok {
			operations = append(operations, operation)
		}
	}
	return operations
}

// Close marks incomplete NATS framing when the connection ends.
func (d *Decoder) Close(_ time.Time, _ error) []protocol.Operation {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Inspection == model.TrafficInspectionDecoded && (len(d.request.buffer) > 0 || len(d.response.buffer) > 0 || d.request.pending != nil || d.response.pending != nil) {
		d.state = protocol.State{Inspection: model.TrafficInspectionMalformed, Reason: "NATS connection closed during a protocol frame"}
	}
	return nil
}

// State returns the current inspection result.
func (d *Decoder) State() protocol.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *Decoder) parse(current *stream, content []byte, direction protocol.Direction) ([]frame, error) {
	if len(current.buffer)+len(content) > protocol.MaximumPayloadLimit+protocol.MaximumControlLine+2 {
		return nil, protocol.LimitError("NATS frame exceeded the bounded parser buffer")
	}
	current.buffer = append(current.buffer, content...)
	var frames []frame
	for {
		if current.pending != nil {
			pending := current.pending
			if len(current.buffer) < pending.payloadBytes+2 {
				break
			}
			if current.buffer[pending.payloadBytes] != '\r' || current.buffer[pending.payloadBytes+1] != '\n' {
				return nil, errors.New("invalid NATS payload terminator")
			}
			body := append([]byte(nil), current.buffer[:pending.payloadBytes]...)
			current.buffer = current.buffer[pending.payloadBytes+2:]
			decoded := frame{command: pending.command, arguments: pending.arguments, wireBytes: int64(pending.wireHeader + pending.payloadBytes + 2)}
			if pending.headerBytes > 0 {
				if pending.headerBytes > len(body) {
					return nil, errors.New("invalid NATS header payload length")
				}
				decoded.headers = parseHeaders(body[:pending.headerBytes])
				decoded.payload = body[pending.headerBytes:]
			} else {
				decoded.payload = body
			}
			frames = append(frames, decoded)
			current.pending = nil
			continue
		}
		lineEnd := bytes.Index(current.buffer, []byte("\r\n"))
		if lineEnd < 0 {
			if len(current.buffer) > protocol.MaximumControlLine {
				return nil, protocol.LimitError("NATS control line exceeded the bounded limit")
			}
			break
		}
		line := string(current.buffer[:lineEnd])
		current.buffer = current.buffer[lineEnd+2:]
		parts := strings.Fields(line)
		if len(parts) == 0 {
			return nil, errors.New("empty NATS control line")
		}
		command := strings.ToUpper(parts[0])
		arguments := parts[1:]
		headerBytes, payloadBytes, hasPayload, err := payloadLengths(command, arguments, direction)
		if err != nil {
			return nil, err
		}
		if hasPayload {
			if payloadBytes > protocol.MaximumPayloadLimit {
				return nil, protocol.LimitError("NATS payload exceeded the bounded inspection limit")
			}
			current.pending = &payloadFrame{command: command, arguments: append([]string(nil), arguments...), headerBytes: headerBytes, payloadBytes: payloadBytes, wireHeader: lineEnd + 2}
			continue
		}
		frames = append(frames, frame{command: command, arguments: append([]string(nil), arguments...), wireBytes: int64(lineEnd + 2)})
	}
	return frames, nil
}

func payloadLengths(command string, arguments []string, direction protocol.Direction) (int, int, bool, error) {
	switch command {
	case "PUB":
		if direction != protocol.DirectionRequest || (len(arguments) != 2 && len(arguments) != 3) {
			return 0, 0, false, errors.New("invalid NATS PUB frame")
		}
		length, err := positiveLength(arguments[len(arguments)-1])
		return 0, length, true, err
	case "HPUB":
		if direction != protocol.DirectionRequest || (len(arguments) != 3 && len(arguments) != 4) {
			return 0, 0, false, errors.New("invalid NATS HPUB frame")
		}
		header, err := positiveLength(arguments[len(arguments)-2])
		if err != nil {
			return 0, 0, false, err
		}
		total, err := positiveLength(arguments[len(arguments)-1])
		if err != nil || header > total {
			return 0, 0, false, errors.New("invalid NATS HPUB lengths")
		}
		return header, total, true, nil
	case "MSG":
		if direction != protocol.DirectionResponse || (len(arguments) != 3 && len(arguments) != 4) {
			return 0, 0, false, errors.New("invalid NATS MSG frame")
		}
		length, err := positiveLength(arguments[len(arguments)-1])
		return 0, length, true, err
	case "HMSG":
		if direction != protocol.DirectionResponse || (len(arguments) != 4 && len(arguments) != 5) {
			return 0, 0, false, errors.New("invalid NATS HMSG frame")
		}
		header, err := positiveLength(arguments[len(arguments)-2])
		if err != nil {
			return 0, 0, false, err
		}
		total, err := positiveLength(arguments[len(arguments)-1])
		if err != nil || header > total {
			return 0, 0, false, errors.New("invalid NATS HMSG lengths")
		}
		return header, total, true, nil
	default:
		return 0, 0, false, nil
	}
}

func positiveLength(value string) (int, error) {
	length, err := strconv.Atoi(value)
	if err != nil || length < 0 {
		return 0, errors.New("invalid NATS payload length")
	}
	return length, nil
}

func (d *Decoder) operation(direction protocol.Direction, decoded frame, observed time.Time) (protocol.Operation, bool) {
	switch decoded.command {
	case "PING", "PONG", "+OK", "INFO", "CONNECT":
		return protocol.Operation{}, false
	}
	policy := d.config.CapturePolicy()
	captureLimit := policy.CaptureLimit()
	name := decoded.command
	outcome := model.TrafficTCPOutcomeOneWay
	errorText := ""
	if decoded.command == "-ERR" {
		name = "ERROR"
		outcome = model.TrafficTCPOutcomeError
		errorText = strings.Join(decoded.arguments, " ")
	}
	operation := protocol.Operation{
		StartedAt: observed, CompletedAt: observed, Name: name, Inspection: model.TrafficInspectionDecoded,
		Outcome: outcome, Error: errorText, Policy: policy,
	}
	message := messageForFrame(decoded, captureLimit)
	if direction == protocol.DirectionRequest {
		operation.RequestBytes = decoded.wireBytes
		if protocol.AppendMessage(&operation.RequestMessages, message, captureLimit) {
			operation.Inspection = model.TrafficInspectionLimited
			operation.InspectionReason = "NATS request payload was truncated by the capture limit"
		}
	} else {
		operation.ResponseBytes = decoded.wireBytes
		if protocol.AppendMessage(&operation.ResponseMessages, message, captureLimit) {
			operation.Inspection = model.TrafficInspectionLimited
			operation.InspectionReason = "NATS response payload was truncated by the capture limit"
		}
	}
	return operation, true
}

func messageForFrame(decoded frame, limit int) model.TrafficMessage {
	subject, reply := frameSubjects(decoded)
	summary := decoded.command
	if subject != "" {
		summary += " " + subject
	}
	retained, encoding, captured, truncated := protocol.CaptureContent(decoded.payload, limit)
	message := model.TrafficMessage{
		Type: strings.ToLower(decoded.command), Summary: summary, WireBytes: decoded.wireBytes,
		ContentBytes: int64(len(decoded.payload)), CapturedBytes: captured, Truncated: truncated,
		Content: retained, ContentType: protocol.ContentType(decoded.payload, "text/plain"), Encoding: encoding,
	}
	if subject != "" {
		message.Fields = append(message.Fields, model.TrafficMessageField{Name: "subject", Value: subject})
	}
	if reply != "" {
		message.Fields = append(message.Fields, model.TrafficMessageField{Name: "reply", Value: reply})
	}
	message.Fields = append(message.Fields, decoded.headers...)
	return message
}

func frameSubjects(decoded frame) (string, string) {
	arguments := decoded.arguments
	switch decoded.command {
	case "PUB", "HPUB":
		if len(arguments) > 0 {
			subject := arguments[0]
			if (decoded.command == "PUB" && len(arguments) == 3) || (decoded.command == "HPUB" && len(arguments) == 4) {
				return subject, arguments[1]
			}
			return subject, ""
		}
	case "MSG", "HMSG":
		if len(arguments) > 1 {
			subject := arguments[0]
			if (decoded.command == "MSG" && len(arguments) == 4) || (decoded.command == "HMSG" && len(arguments) == 5) {
				return subject, arguments[2]
			}
			return subject, ""
		}
	case "SUB":
		if len(arguments) > 0 {
			return arguments[0], ""
		}
	}
	return "", ""
}

func parseHeaders(content []byte) []model.TrafficMessageField {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	fields := make([]model.TrafficMessageField, 0, len(lines))
	for _, line := range lines {
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		fields = append(fields, model.TrafficMessageField{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
	}
	return fields
}

func looksLikeTLS(content []byte) bool {
	return len(content) >= 3 && content[0] == 0x16 && content[1] == 0x03
}
