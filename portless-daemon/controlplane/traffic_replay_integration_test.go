package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/replay"
)

func replayIntegrationService(t *testing.T) (*Service, *database.Store, *atomic.Int64, *httptest.Server) {
	t.Helper()
	data, root := t.TempDir(), t.TempDir()
	db, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Connections: []model.Connection{{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Environment: "ORDERS_URL"}}}
	for _, name := range []string{"checkout", "orders"} {
		definition.Services = append(definition.Services, model.ServiceDefinition{Name: name, Kind: model.ServiceProcess, Command: []string{"echo", name}, WorkingDirectory: root, ServiceDirectory: root})
	}
	if _, err := db.CreateProject(t.Context(), "billing", definition, []model.ProjectSource{{Name: "app", Services: []string{"checkout", "orders"}}}); err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{"local", "fix"} {
		if _, err := db.CreateEnvironment(t.Context(), "billing", environment, definition, []model.SourceBinding{{Name: "app", Path: root, Status: "ready", Definition: definition}}, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "app"}, {Service: "orders", Provider: model.ProviderLocal, Source: "app"}}); err != nil {
			t.Fatal(err)
		}
		if err := db.SetServiceStatus(t.Context(), "billing/"+environment, "orders", model.ServiceReady, ""); err != nil {
			t.Fatal(err)
		}
		if err := db.SetEnvironmentStatus(t.Context(), "billing", environment, model.EnvironmentHealthy, ""); err != nil {
			t.Fatal(err)
		}
	}
	app := New(db, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "replay-integration"})
	calls := new(atomic.Int64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"version":2}`)
	}))
	t.Cleanup(upstream.Close)
	parsed, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(parsed.Port())
	for _, environment := range []string{"local", "fix"} {
		app.proxy.SetTarget("billing/"+environment, "orders", port)
	}
	t.Cleanup(func() { app.Close(context.Background()) })
	return app, db, calls, upstream
}

func replayIntegrationBaseline(app *Service, source string) model.TrafficExchange {
	started := time.Now().UTC().Add(-time.Second)
	return app.AddTrafficExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Source: source, Target: "orders", Method: "GET", Path: "/orders", RequestTarget: "/orders?version=1", Status: 200, StartedAt: started, CompletedAt: started.Add(time.Millisecond), RequestCapture: &model.HTTPCapture{State: "empty", Encoding: "identity", Exact: true}, ResponseHeaders: map[string][]string{"Content-Type": {"application/json"}}, ResponseBody: `{"version":1}`, ResponseCapture: &model.HTTPCapture{State: "complete", Encoding: "identity", Exact: true, ObservedBytes: 13, CapturedBytes: 13}})
}

func replayErrorStatus(t *testing.T, err error, want int) {
	t.Helper()
	var classified *replay.Error
	if !errors.As(err, &classified) || classified.Status != want {
		t.Fatalf("error=%v want replay status %d", err, want)
	}
}

func TestTrafficReplayPreparationRequiresExactLiveBaseline(t *testing.T) {
	app, db, calls, _ := replayIntegrationService(t)
	baseline := replayIntegrationBaseline(app, "external")
	_, err := app.PrepareTrafficReplay(t.Context(), "billing", "local", contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt.Add(time.Nanosecond)})
	replayErrorStatus(t, err, 409)
	workspace, err := app.PrepareTrafficReplay(t.Context(), "billing", "local", contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt})
	if err != nil || workspace.Baseline == nil || calls.Load() != 0 {
		t.Fatalf("prepare=%#v calls=%d err=%v", workspace, calls.Load(), err)
	}
	_, err = db.CreateRecording(t.Context(), model.Recording{Project: "billing", Environment: "local", Name: "retained", CapturePayloads: true, MaxPayloadBytes: 65536})
	if err != nil {
		t.Fatal(err)
	}
	baseline.Recording = "retained"
	if err = db.PersistTraffic(t.Context(), baseline); err != nil {
		t.Fatal(err)
	}
	app.ClearTraffic("billing", "local")
	if inspected, err := app.TrafficExchange(t.Context(), "billing", "local", baseline.Sequence); err != nil || inspected.Sequence != baseline.Sequence {
		t.Fatalf("retained inspection=%#v err=%v", inspected, err)
	}
	_, err = app.PrepareTrafficReplay(t.Context(), "billing", "local", contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt})
	replayErrorStatus(t, err, 404)
	if calls.Load() != 0 {
		t.Fatalf("preparation dispatched %d requests", calls.Load())
	}
}

func TestTrafficReplayRunsSameEdgeInAnotherEnvironmentAndReconcilesDuplicate(t *testing.T) {
	app, _, calls, _ := replayIntegrationService(t)
	baseline := replayIntegrationBaseline(app, "checkout")
	workspace, err := app.PrepareTrafficReplay(t.Context(), "billing", "local", contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt})
	if err != nil {
		t.Fatal(err)
	}
	draft := *workspace.Draft
	draft.Environment = "fix"
	draft.Method = "POST"
	draft.BodyMode = "replacement"
	draft.Body = `{"edited":true}`
	draft.Headers = map[string][]string{"Content-Type": {"application/json"}}
	workspace, err = app.UpdateTrafficReplay(t.Context(), "billing", "local", workspace.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, Draft: draft})
	if err != nil || calls.Load() != 0 {
		t.Fatalf("draft calls=%d err=%v", calls.Load(), err)
	}
	input := contract.RunTrafficReplayRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, RunNumber: 1}
	admitted, err := app.RunTrafficReplay(t.Context(), "billing", "local", workspace.Number, input)
	if err != nil || admitted.Run == nil {
		t.Fatalf("admission=%#v err=%v", admitted, err)
	}
	duplicate, err := app.RunTrafficReplay(t.Context(), "billing", "local", workspace.Number, input)
	if err != nil || duplicate.Run == nil || duplicate.Run.Number != 1 {
		t.Fatalf("duplicate=%#v err=%v", duplicate, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		workspace, err = app.TrafficReplay("billing", "local", workspace.Number, input.TrafficReplayIdentity, true)
		if err != nil {
			t.Fatal(err)
		}
		if workspace.Run != nil && workspace.Run.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("replay did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 || workspace.Run.State != "completed" || workspace.Result == nil || workspace.Result.Exchange == nil {
		t.Fatalf("finished=%#v calls=%d", workspace, calls.Load())
	}
	result := workspace.Result.Exchange
	if result.Environment != "fix" || result.Source != "checkout" || result.Target != "orders" || result.Replay == nil || result.Replay.Environment != "local" || result.Replay.Sequence != baseline.Sequence || workspace.Result.Comparison.Body.State != "different" {
		t.Fatalf("result=%#v comparison=%#v", result, workspace.Result.Comparison)
	}
	original, err := app.TrafficExchange(t.Context(), "billing", "local", baseline.Sequence)
	if err != nil || original.Status != 200 || original.ResponseBody != `{"version":1}` {
		t.Fatalf("baseline mutated=%#v err=%v", original, err)
	}
	metadata, err := app.TrafficReplay("billing", "local", workspace.Number, input.TrafficReplayIdentity, false)
	if err != nil || metadata.Baseline != nil || metadata.Draft != nil || metadata.Result != nil || len(metadata.Receipts) != 1 {
		t.Fatalf("metadata=%#v err=%v", metadata, err)
	}
}

func TestTrafficReplayRejectsStaleRuntimeGenerationAndIdentity(t *testing.T) {
	app, _, calls, _ := replayIntegrationService(t)
	baseline := replayIntegrationBaseline(app, "external")
	workspace, err := app.PrepareTrafficReplay(t.Context(), "billing", "local", contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = app.UpdateTrafficReplay(t.Context(), "billing", "local", workspace.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, Draft: *workspace.Draft})
	if err != nil {
		t.Fatal(err)
	}
	app.proxy.SetTarget("billing/local", "orders", 9)
	_, err = app.RunTrafficReplay(t.Context(), "billing", "local", workspace.Number, contract.RunTrafficReplayRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, RunNumber: 1})
	replayErrorStatus(t, err, 409)
	stale := workspace.TrafficReplayIdentity
	stale.DaemonStartedAt = stale.DaemonStartedAt.Add(time.Nanosecond)
	_, err = app.TrafficReplay("billing", "local", workspace.Number, stale, true)
	replayErrorStatus(t, err, 410)
	if calls.Load() != 0 {
		t.Fatalf("stale requests dispatched %d calls", calls.Load())
	}
}

func TestTrafficReplayRejectsMixedBindingAndEffectiveProviderSnapshot(t *testing.T) {
	app, db, calls, upstream := replayIntegrationService(t)
	remote := model.RemoteTarget{URL: upstream.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadWrite}
	// Model the handoff interval after a remote-to-local binding is saved but
	// before the previously ready remote target has been replaced.
	if err := app.proxy.SetRemoteTarget("billing/local", "orders", remote); err != nil {
		t.Fatal(err)
	}
	current, err := db.Environment(t.Context(), "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	if bindingForEnvironment(current, "orders").Provider != model.ProviderLocal {
		t.Fatal("fixture needs a saved local binding")
	}
	if _, err := app.resolveReplayTarget(t.Context(), "billing", "local", "external", "orders", "GET", "/"); err == nil {
		t.Fatal("mixed local binding and effective remote target was accepted without remote policy")
	}
	if calls.Load() != 0 {
		t.Fatalf("resolution sent %d application calls", calls.Load())
	}
}

func TestPartialReplayUsesActualRemoteDecisionAndInvalidatesRouteEdits(t *testing.T) {
	for _, policy := range []model.WritePolicy{model.WriteReadOnly, model.WriteReadWrite} {
		t.Run(string(policy), func(t *testing.T) {
			app, db, calls, upstream := replayIntegrationService(t)
			remote := model.RemoteTarget{URL: upstream.URL, Classification: model.RemoteQA, WritePolicy: policy}
			definition, err := db.EnvironmentModel(t.Context(), "billing", "local")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ApplyActiveBindingConfiguration(t.Context(), "billing", "local", 0, definition, model.ComponentBinding{Service: "orders", Provider: model.ProviderRemote, Remote: &remote}); err != nil {
				t.Fatal(err)
			}
			if err := app.proxy.SetRemoteTarget("billing/local", "orders", remote); err != nil {
				t.Fatal(err)
			}
			if _, err := app.CreateMockScenario(t.Context(), "billing", "local", model.MockScenario{Name: "partial", UnmatchedRequests: model.MockUnmatchedForward}, "test"); err != nil {
				t.Fatal(err)
			}
			route := model.MockRoute{Name: "create", Service: "orders", Method: "POST", Path: "/orders", Status: 202, Enabled: true}
			if _, err := app.PutMockRoute(t.Context(), "billing", "local", "partial", route.Name, route, "test"); err != nil {
				t.Fatal(err)
			}
			op, err := app.SetMockScenarioEnabled(t.Context(), "billing", "local", "partial", true, "test", "enable")
			if err != nil {
				t.Fatal(err)
			}
			if op = waitForOperation(t, app, op); op.State != "succeeded" {
				t.Fatalf("enable=%#v", op)
			}
			baseline := replayIntegrationBaseline(app, "checkout")
			workspace, err := app.PrepareTrafficReplay(t.Context(), "billing", "local", contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt})
			if err != nil {
				t.Fatal(err)
			}
			draft := *workspace.Draft
			draft.Method = "POST"
			workspace, err = app.UpdateTrafficReplay(t.Context(), "billing", "local", workspace.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, Draft: draft})
			if err != nil || workspace.Destination.Provider != "mock" || workspace.Destination.MockScenario != "partial" || workspace.Destination.MockRoute != "create" || workspace.Destination.RequiresConfirmation || calls.Load() != 0 {
				t.Fatalf("mock preparation=%#v %v", workspace.Destination, err)
			}
			route.Enabled = false
			if _, err := app.PutMockRoute(t.Context(), "billing", "local", "partial", route.Name, route, "test"); err != nil {
				t.Fatal(err)
			}
			_, err = app.RunTrafficReplay(t.Context(), "billing", "local", workspace.Number, contract.RunTrafficReplayRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, RunNumber: 1})
			replayErrorStatus(t, err, 409)
			prepared, err := app.UpdateTrafficReplay(t.Context(), "billing", "local", workspace.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, Draft: draft})
			if policy == model.WriteReadOnly {
				replayErrorStatus(t, err, 403)
			} else {
				if err != nil || prepared.Destination.Provider != "remote" || !prepared.Destination.RequiresConfirmation {
					t.Fatalf("forward preparation=%#v %v", prepared.Destination, err)
				}
				_, err = app.RunTrafficReplay(t.Context(), "billing", "local", prepared.Number, contract.RunTrafficReplayRequest{TrafficReplayIdentity: prepared.TrafficReplayIdentity, Revision: prepared.Revision, RunNumber: 1})
				if err == nil {
					t.Fatal("unconfirmed remote write was admitted")
				}
			}
			if calls.Load() != 0 {
				t.Fatal("preparation or stale/blocked replay dispatched upstream")
			}
		})
	}
}
