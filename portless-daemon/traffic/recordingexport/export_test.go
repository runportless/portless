package recordingexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/runportless/portless/portless-daemon/model"
	"io"
	"strings"
	"testing"
	"time"
)

type exportSource struct {
	count   int64
	started time.Time
	large   bool
}

// RecordingExportSnapshot freezes the test recording's event count and sequence watermark.
func (s *exportSource) RecordingExportSnapshot(_ context.Context, project, environment, name string) (model.RecordingExportSnapshot, error) {
	return model.RecordingExportSnapshot{Project: project, Environment: environment, Recording: name, RecordingStartedAt: s.started, EnvironmentCreatedAt: s.started, EventCount: s.count, ThroughSequence: s.count * 2}, nil
}

// ValidateRecordingExportSnapshot models deletion and name reuse during an export.
func (s *exportSource) ValidateRecordingExportSnapshot(_ context.Context, snapshot model.RecordingExportSnapshot) error {
	if !snapshot.RecordingStartedAt.Equal(s.started) {
		return errors.New("recording identity changed")
	}
	return nil
}

// RecordingExportEvent supplies an indexed event with intentionally non-contiguous sequences.
func (s *exportSource) RecordingExportEvent(_ context.Context, snapshot model.RecordingExportSnapshot, before, sequence int64) (model.RecordingExportEvent, error) {
	if sequence == 0 {
		sequence = snapshot.ThroughSequence
		if before > 0 {
			sequence = before - 2
		}
	}
	if sequence < 2 || sequence > snapshot.ThroughSequence {
		return model.RecordingExportEvent{}, errors.New("event missing")
	}
	body := strings.Repeat("x", 2048)
	if s.large && sequence == snapshot.ThroughSequence {
		body = strings.Repeat("\x00☕", 40000)
	}
	encoded, err := json.Marshal(model.TrafficExchange{Project: snapshot.Project, Environment: snapshot.Environment, Sequence: sequence, ResponseBody: body})
	return model.RecordingExportEvent{Sequence: sequence, JSON: encoded}, err
}

func TestChunksAndStreamExportMoreThanTenThousandEventsAndSixteenMiB(t *testing.T) {
	source := &exportSource{count: 10007, started: time.Now().UTC(), large: true}
	exporter := New(source)
	var assembled bytes.Buffer
	continuation := ""
	chunks := 0
	for {
		maximum := MaximumChunkBytes
		if chunks == 0 {
			maximum = 23
		}
		chunk, err := exporter.Read(t.Context(), "shop", "local", "capture", continuation, maximum)
		if err != nil {
			t.Fatal(err)
		}
		if len(chunk.Bytes) > maximum || chunk.Snapshot.EventCount != 10007 {
			t.Fatalf("bad chunk: len=%d snapshot=%#v", len(chunk.Bytes), chunk.Snapshot)
		}
		assembled.Write(chunk.Bytes)
		chunks++
		if chunk.Complete {
			if chunk.NextCursor != "" {
				t.Fatal("completed export has a continuation")
			}
			break
		}
		if chunk.NextCursor == "" {
			t.Fatal("incomplete export has no continuation")
		}
		continuation = chunk.NextCursor
	}
	if assembled.Len() <= 16<<20 || chunks < 100 {
		t.Fatalf("export too small to cover the old cutoffs: bytes=%d chunks=%d", assembled.Len(), chunks)
	}
	var result struct {
		SchemaVersion int                     `json:"schemaVersion"`
		Exchanges     []model.TrafficExchange `json:"exchanges"`
	}
	if err := json.Unmarshal(assembled.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 5 || len(result.Exchanges) != 10007 || result.Exchanges[0].Sequence != 20014 || result.Exchanges[10006].Sequence != 2 || result.Exchanges[0].ResponseBody != strings.Repeat("\x00☕", 40000) {
		t.Fatal("export lost events, ordering, or a split escaped payload")
	}
	var streamed bytes.Buffer
	if err := exporter.Write(t.Context(), "shop", "local", "capture", &streamed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(assembled.Bytes(), streamed.Bytes()) {
		t.Fatal("stream and chunk encoders produced different documents")
	}
}

func TestCursorBindsSnapshotIdentityScopeAndPosition(t *testing.T) {
	source := &exportSource{count: 2, started: time.Now().UTC()}
	exporter := New(source)
	first, err := exporter.Read(t.Context(), "shop", "local", "capture", "", 16)
	if err != nil {
		t.Fatal(err)
	}
	source.count = 3
	var content bytes.Buffer
	content.Write(first.Bytes)
	cursor := first.NextCursor
	for {
		chunk, err := exporter.Read(t.Context(), "shop", "local", "capture", cursor, MaximumChunkBytes)
		if err != nil {
			t.Fatal(err)
		}
		content.Write(chunk.Bytes)
		if chunk.Complete {
			break
		}
		cursor = chunk.NextCursor
	}
	var result struct {
		Exchanges []model.TrafficExchange `json:"exchanges"`
	}
	if err := json.Unmarshal(content.Bytes(), &result); err != nil || len(result.Exchanges) != 2 {
		t.Fatalf("active snapshot included newer traffic: %d %v", len(result.Exchanges), err)
	}
	for _, bad := range []string{first.NextCursor + "x", strings.Repeat("a", 5000)} {
		if _, err := exporter.Read(t.Context(), "shop", "local", "capture", bad, 16); err == nil {
			t.Fatal("accepted modified cursor")
		}
	}
	if _, err := exporter.Read(t.Context(), "shop", "other", "capture", first.NextCursor, 16); err == nil {
		t.Fatal("cursor widened scope")
	}
	if _, err := New(source).Read(t.Context(), "shop", "local", "capture", first.NextCursor, 16); err == nil {
		t.Fatal("cursor survived signer replacement")
	}
	source.started = source.started.Add(time.Nanosecond)
	if _, err := exporter.Read(t.Context(), "shop", "local", "capture", first.NextCursor, 16); err == nil {
		t.Fatal("cursor survived recording name reuse")
	}
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }

func TestEmptyRecordingAndInterruptedWriter(t *testing.T) {
	exporter := New(&exportSource{started: time.Now().UTC()})
	chunk, err := exporter.Read(t.Context(), "shop", "local", "empty", "", 0)
	if err != nil || !chunk.Complete || !bytes.HasSuffix(chunk.Bytes, []byte("\"exchanges\":[]}\n")) {
		t.Fatalf("empty export=%q %v", chunk.Bytes, err)
	}
	if err := exporter.Write(t.Context(), "shop", "local", "empty", shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := exporter.Read(ctx, "shop", "local", "empty", "", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled export=%v", err)
	}
}
