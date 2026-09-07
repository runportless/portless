// Package recordingexport delivers complete recorded-traffic JSON through bounded, resumable chunks.
package recordingexport

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/runportless/portless/portless-daemon/model"
	"io"
	"strings"
)

// MaximumChunkBytes bounds decoded export data independently of JSON/base64 overhead.
const MaximumChunkBytes = 128 << 10

// Source supplies identity-checked, indexed reads of persisted redacted exchanges.
type Source interface {
	// RecordingExportSnapshot atomically captures the recording's current retained-event watermark.
	RecordingExportSnapshot(context.Context, string, string, string) (model.RecordingExportSnapshot, error)
	// ValidateRecordingExportSnapshot rejects a deleted or replaced recording identity.
	ValidateRecordingExportSnapshot(context.Context, model.RecordingExportSnapshot) error
	// RecordingExportEvent reads one exact sequence or the next descending event before a sequence.
	RecordingExportEvent(context.Context, model.RecordingExportSnapshot, int64, int64) (model.RecordingExportEvent, error)
}

// Chunk is one ordered segment of a complete schema-4 recording export document.
type Chunk struct {
	Snapshot   model.RecordingExportSnapshot
	Bytes      []byte
	NextCursor string
	Complete   bool
}

// Exporter signs stateless continuation cursors and never retains export payloads or files.
type Exporter struct {
	source Source
	secret string
}

// New constructs a daemon-lifetime export cursor signer over a bounded source.
func New(source Source) *Exporter { return &Exporter{source: source, secret: rand.Text()} }

type cursor struct {
	Version  int                           `json:"version"`
	Snapshot model.RecordingExportSnapshot `json:"snapshot"`
	Stage    string                        `json:"stage"`
	Before   int64                         `json:"before"`
	Sequence int64                         `json:"sequence"`
	Offset   int                           `json:"offset"`
	Emitted  int64                         `json:"emitted"`
}

func (e *Exporter) sign(value cursor) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(e.secret))
	_, _ = mac.Write(encoded)
	return base64.RawURLEncoding.EncodeToString(encoded) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (e *Exporter) parse(value, project, environment, recording string) (cursor, error) {
	var result cursor
	if len(value) > 4096 {
		return result, errors.New("export cursor exceeds its size limit")
	}
	encoded, signature, ok := strings.Cut(value, ".")
	if !ok {
		return result, errors.New("invalid export cursor")
	}
	content, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return result, errors.New("invalid export cursor encoding")
	}
	got, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return result, errors.New("invalid export cursor signature")
	}
	mac := hmac.New(sha256.New, []byte(e.secret))
	_, _ = mac.Write(content)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return result, errors.New("export cursor changed or belongs to a previous daemon; restart export")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, errors.New("invalid export cursor data")
	}
	if result.Version != 1 || !strings.EqualFold(result.Snapshot.Project, project) || !strings.EqualFold(result.Snapshot.Environment, environment) || !strings.EqualFold(result.Snapshot.Recording, recording) || result.Offset < 0 || result.Emitted < 0 || result.Emitted > result.Snapshot.EventCount {
		return cursor{}, errors.New("export cursor does not match the selected recording")
	}
	return result, nil
}

func prefix(snapshot model.RecordingExportSnapshot) []byte {
	encoded, _ := json.Marshal(struct {
		SchemaVersion int    `json:"schemaVersion"`
		Project       string `json:"project"`
		Environment   string `json:"environment"`
		Recording     string `json:"recording"`
	}{4, snapshot.Project, snapshot.Environment, snapshot.Recording})
	return append(encoded[:len(encoded)-1], []byte(`,"exchanges":[`)...)
}

// Read returns bounded lossless bytes, including event fragments when a single event is large.
// A cursor selects a snapshot position only; callers must independently authorize its scope.
func (e *Exporter) Read(ctx context.Context, project, environment, recording, continuation string, maxBytes int) (Chunk, error) {
	var result Chunk
	if maxBytes == 0 {
		maxBytes = MaximumChunkBytes
	}
	if maxBytes < 1 || maxBytes > MaximumChunkBytes {
		return result, fmt.Errorf("maxBytes must be between 1 and %d", MaximumChunkBytes)
	}
	var position cursor
	var err error
	if continuation == "" {
		snapshot, err := e.source.RecordingExportSnapshot(ctx, project, environment, recording)
		if err != nil {
			return result, err
		}
		position = cursor{Version: 1, Snapshot: snapshot, Stage: "prefix"}
	} else {
		position, err = e.parse(continuation, project, environment, recording)
		if err != nil {
			return result, err
		}
	}
	if err := e.source.ValidateRecordingExportSnapshot(ctx, position.Snapshot); err != nil {
		return result, err
	}
	result.Snapshot = position.Snapshot
	result.Bytes = make([]byte, 0, maxBytes)
	for len(result.Bytes) < maxBytes {
		if err := ctx.Err(); err != nil {
			return Chunk{}, err
		}
		var part []byte
		switch position.Stage {
		case "prefix":
			part = prefix(position.Snapshot)
		case "events":
			if position.Emitted == position.Snapshot.EventCount {
				position.Stage = "suffix"
				position.Offset = 0
				continue
			}
			event, err := e.source.RecordingExportEvent(ctx, position.Snapshot, position.Before, position.Sequence)
			if err != nil {
				return Chunk{}, fmt.Errorf("read recording export event: %w", err)
			}
			if !json.Valid(event.JSON) {
				return Chunk{}, errors.New("retained recording event is not valid JSON")
			}
			position.Sequence = event.Sequence
			if position.Emitted > 0 {
				part = append([]byte{','}, event.JSON...)
			} else {
				part = event.JSON
			}
		case "suffix":
			part = []byte("]}\n")
		default:
			return Chunk{}, errors.New("invalid export cursor position")
		}
		if position.Offset > len(part) {
			return Chunk{}, errors.New("recording export content changed")
		}
		count := min(maxBytes-len(result.Bytes), len(part)-position.Offset)
		result.Bytes = append(result.Bytes, part[position.Offset:position.Offset+count]...)
		position.Offset += count
		if position.Offset < len(part) {
			break
		}
		position.Offset = 0
		switch position.Stage {
		case "prefix":
			position.Stage = "events"
		case "events":
			position.Before = position.Sequence
			position.Sequence = 0
			position.Emitted++
		case "suffix":
			result.Complete = true
			return result, nil
		}
	}
	result.NextCursor, err = e.sign(position)
	return result, err
}

// Write streams exactly the same snapshot document produced by Read without accumulating it.
func (e *Exporter) Write(ctx context.Context, project, environment, recording string, writer io.Writer) error {
	continuation := ""
	for {
		chunk, err := e.Read(ctx, project, environment, recording, continuation, MaximumChunkBytes)
		if err != nil {
			return err
		}
		if n, err := writer.Write(chunk.Bytes); err != nil {
			return err
		} else if n != len(chunk.Bytes) {
			return io.ErrShortWrite
		}
		if chunk.Complete {
			return nil
		}
		continuation = chunk.NextCursor
	}
}
