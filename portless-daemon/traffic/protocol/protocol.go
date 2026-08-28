// Package protocol owns bounded, incremental decoding of supported TCP
// application protocols. Decoders observe copied byte slices and never own or
// mutate the bytes forwarded by the proxy.
package protocol

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runportless/portless/portless-daemon/model"
)

var errInspectionLimit = errors.New("protocol inspection limit reached")

const (
	// LivePayloadLimit is the retained byte limit for each operation direction
	// in the live traffic window.
	LivePayloadLimit = 64 << 10
	// MaximumPayloadLimit is the largest recording payload limit accepted by the daemon.
	MaximumPayloadLimit = 1 << 20
	// MaximumMessages is the maximum number of retained messages in one operation.
	MaximumMessages = 256
	// MaximumPendingOperations bounds protocol pipelines on one connection.
	MaximumPendingOperations = 128
	// MaximumControlLine bounds line-oriented protocol framing metadata.
	MaximumControlLine = 8 << 10
)

// Direction identifies the observed half of a proxied TCP stream.
type Direction uint8

const (
	// DirectionRequest carries bytes from the source service to its dependency.
	DirectionRequest Direction = iota + 1
	// DirectionResponse carries bytes from the dependency back to the source service.
	DirectionResponse
)

// CapturePolicy is the recording policy selected when an operation starts.
type CapturePolicy struct {
	Recording       string
	PersistPayloads bool
	// PayloadLimit is the recording's durable payload limit. Decoders use
	// CaptureLimit so the independent live window still retains its full prefix.
	PayloadLimit int
}

// PolicyFunc returns the current recording policy for a newly observed operation.
type PolicyFunc func() CapturePolicy

// Config contains bounded capture dependencies shared by protocol sessions.
type Config struct {
	Policy PolicyFunc
}

// CapturePolicy returns a normalized capture policy without exceeding daemon limits.
func (c Config) CapturePolicy() CapturePolicy {
	policy := CapturePolicy{PayloadLimit: LivePayloadLimit}
	if c.Policy != nil {
		policy = c.Policy()
	}
	if policy.PayloadLimit <= 0 {
		policy.PayloadLimit = LivePayloadLimit
	}
	if policy.PayloadLimit > MaximumPayloadLimit {
		policy.PayloadLimit = MaximumPayloadLimit
	}
	return policy
}

// CaptureLimit returns the larger live or durable limit used while decoding.
// The live and recording views are bounded independently after an operation
// completes.
func (p CapturePolicy) CaptureLimit() int {
	if p.PayloadLimit > LivePayloadLimit {
		return p.PayloadLimit
	}
	return LivePayloadLimit
}

// LimitError marks a decoder failure as a bounded-inspection limit rather than malformed input.
func LimitError(reason string) error {
	return fmt.Errorf("%w: %s", errInspectionLimit, reason)
}

// StateForError maps decoder errors to their public inspection classification.
func StateForError(err error) State {
	inspection := model.TrafficInspectionMalformed
	reason := err.Error()
	if errors.Is(err, errInspectionLimit) {
		inspection = model.TrafficInspectionLimited
		reason = strings.TrimPrefix(reason, errInspectionLimit.Error()+": ")
	}
	return State{Inspection: inspection, Reason: reason}
}

// State describes the current inspection result for a protocol session.
type State struct {
	Inspection model.TrafficInspection
	Reason     string
}

// Operation is one completed logical application operation emitted by a decoder.
type Operation struct {
	StartedAt           time.Time
	CompletedAt         time.Time
	Name                string
	Background          bool
	TransactionSequence uint64
	Inspection          model.TrafficInspection
	InspectionReason    string
	Outcome             model.TrafficTCPOutcome
	RequestMessages     []model.TrafficMessage
	ResponseMessages    []model.TrafficMessage
	RequestBytes        int64
	ResponseBytes       int64
	Error               string
	Policy              CapturePolicy
}

// Session incrementally observes both directions of one TCP connection.
// Implementations must be safe for one concurrent caller per direction.
type Session interface {
	// Observe consumes one unchanged stream chunk and returns newly completed operations.
	Observe(Direction, []byte, time.Time) []Operation
	// Close completes any unfinished operations after the connection ends.
	Close(time.Time, error) []Operation
	// State reports the current bounded inspection classification.
	State() State
}

// Factory creates a fresh decoder for one TCP connection.
type Factory func(Config) Session

// Registry maps declared application protocols to decoder factories.
type Registry struct {
	factories map[model.ApplicationProtocol]Factory
}

// NewRegistry creates an empty protocol decoder registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[model.ApplicationProtocol]Factory)}
}

// Register associates a non-empty application protocol with a decoder factory.
func (r *Registry) Register(applicationProtocol model.ApplicationProtocol, factory Factory) error {
	if r == nil || applicationProtocol == "" || factory == nil {
		return errors.New("protocol registration requires a protocol and factory")
	}
	if _, exists := r.factories[applicationProtocol]; exists {
		return errors.New("protocol decoder is already registered")
	}
	r.factories[applicationProtocol] = factory
	return nil
}

// Open creates a decoder for a declared protocol or reports that none exists.
func (r *Registry) Open(applicationProtocol model.ApplicationProtocol, config Config) (Session, bool) {
	if r == nil {
		return nil, false
	}
	factory, ok := r.factories[applicationProtocol]
	if !ok {
		return nil, false
	}
	return factory(config), true
}

// CaptureContent retains a bounded UTF-8 or base64 representation of content.
func CaptureContent(content []byte, limit int) (string, model.TrafficMessageEncoding, int64, bool) {
	total := len(content)
	if limit < 0 {
		limit = 0
	}
	retained := content
	truncated := false
	if len(retained) > limit {
		retained = retained[:limit]
		truncated = true
	}
	if utf8.Valid(retained) {
		return string(retained), model.TrafficMessageEncodingUTF8, int64(len(retained)), truncated
	}
	return base64.StdEncoding.EncodeToString(retained), model.TrafficMessageEncodingBase64, int64(len(retained)), truncated || len(retained) < total
}

// ContentType returns a useful display type for decoded application content.
func ContentType(content []byte, fallback string) string {
	if len(content) > 0 && json.Valid(content) {
		return "application/json"
	}
	return fallback
}

// OffsetMS returns a non-negative millisecond offset from an operation start.
func OffsetMS(started, observed time.Time) int64 {
	offset := observed.Sub(started).Milliseconds()
	if offset < 0 {
		return 0
	}
	return offset
}

// AppendMessage appends a message while enforcing the per-operation count and
// aggregate content-byte limits. It returns true when content was truncated or
// the message could not be retained.
func AppendMessage(messages *[]model.TrafficMessage, message model.TrafficMessage, limit int) bool {
	if len(*messages) >= MaximumMessages {
		return true
	}
	remaining := int64(limit)
	for _, retained := range *messages {
		remaining -= retained.CapturedBytes
	}
	limited := false
	if remaining < message.CapturedBytes {
		if remaining < 0 {
			remaining = 0
		}
		if message.Encoding == model.TrafficMessageEncodingUTF8 {
			bytes := []byte(message.Content)
			if int64(len(bytes)) > remaining {
				message.Content = strings.ToValidUTF8(string(bytes[:remaining]), "�")
			}
		} else {
			decoded, err := base64.StdEncoding.DecodeString(message.Content)
			if err == nil && int64(len(decoded)) > remaining {
				message.Content = base64.StdEncoding.EncodeToString(decoded[:remaining])
			}
		}
		message.CapturedBytes = remaining
		message.Truncated = true
		limited = true
	}
	*messages = append(*messages, message)
	return limited || message.Truncated
}
