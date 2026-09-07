package database

import (
	"encoding/json"
	"errors"
	"github.com/runportless/portless/portless-daemon/model"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordingExportSnapshotAndIndexedReadsHaveNoTenThousandEventCutoff(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	definition := model.ProjectModel{SuggestedName: "shop"}
	if _, err := store.CreateProject(t.Context(), "shop", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEnvironment(t.Context(), "shop", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	started := time.Now().In(time.FixedZone("test", 7200))
	if _, err := store.CreateRecording(t.Context(), model.Recording{Project: "shop", Environment: "local", Name: "capture", MaxEvents: 100000, StartedAt: started}); err != nil {
		t.Fatal(err)
	}
	key, err := store.PrivateEnvironmentKey(t.Context(), "shop", "local")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(t.Context(), `INSERT INTO traffic_events(environment_key,sequence,recording_name,event_json) VALUES(?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10003; i++ {
		encoded, err := json.Marshal(model.TrafficExchange{Project: "shop", Environment: "local", Sequence: int64(i * 3), ResponseBody: "retained payload"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := statement.ExecContext(t.Context(), key, i*3, "capture", encoded); err != nil {
			t.Fatal(err)
		}
	}
	statement.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.RecordingExportSnapshot(t.Context(), "shop", "local", "capture")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EventCount != 10003 || snapshot.ThroughSequence != 30009 || !snapshot.RecordingStartedAt.Equal(started) {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if err := store.ValidateRecordingExportSnapshot(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	first, err := store.RecordingExportEvent(t.Context(), snapshot, 0, 0)
	if err != nil || first.Sequence != 30009 {
		t.Fatalf("first=%#v %v", first, err)
	}
	next, err := store.RecordingExportEvent(t.Context(), snapshot, first.Sequence, 0)
	if err != nil || next.Sequence != 30006 {
		t.Fatalf("next=%#v %v", next, err)
	}
	last, err := store.RecordingExportEvent(t.Context(), snapshot, 0, 3)
	if err != nil || last.Sequence != 3 {
		t.Fatalf("exact last=%#v %v", last, err)
	}
	if err := store.PersistTraffic(t.Context(), model.TrafficExchange{Project: "shop", Environment: "local", Recording: "capture", Sequence: 30012}); err != nil {
		t.Fatal(err)
	}
	if first, err := store.RecordingExportEvent(t.Context(), snapshot, 0, 0); err != nil || first.Sequence != 30009 {
		t.Fatal("active snapshot included an event beyond its watermark")
	}
	if err := store.StopRecording(t.Context(), "shop/local", "capture", "stopped"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRecording(t.Context(), "shop/local", "capture", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRecording(t.Context(), model.Recording{Project: "shop", Environment: "local", Name: "capture"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateRecordingExportSnapshot(t.Context(), snapshot); !errors.Is(err, ErrConflict) {
		t.Fatalf("replacement snapshot=%v", err)
	}
	if _, err := store.RecordingExportEvent(t.Context(), snapshot, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacement event lookup=%v", err)
	}
}
