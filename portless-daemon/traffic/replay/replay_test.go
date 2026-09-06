package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

func captured(body string) *model.HTTPCapture {
	state := "complete"
	if body == "" {
		state = "empty"
	}
	return &model.HTTPCapture{State: state, ObservedBytes: int64(len(body)), CapturedBytes: int64(len(body)), Encoding: "identity", Exact: true}
}

func original() model.TrafficExchange {
	return model.TrafficExchange{Project: "store", Environment: "local", Protocol: model.ProtocolHTTP, Sequence: 142, Source: "checkout", Target: "orders", StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(), Method: "POST", Path: "/orders", RequestTarget: "/orders/a%2Fb?tag=a&tag=&space=+&space=%20", Status: 200, DurationMS: 10, RequestBody: `{"sku":"coffee"}`, ResponseBody: `{"available":true}`, RequestHeaders: map[string][]string{"Content-Type": {"application/json"}, "X-Repeat": {"first", "second"}}, ResponseHeaders: map[string][]string{"Content-Type": {"application/json"}}, RequestCapture: captured(`{"sku":"coffee"}`), ResponseCapture: captured(`{"available":true}`)}
}

func resolver(_ context.Context, _, environment, _, _ string) (Target, error) {
	return Target{Generation: 1, Version: "first", Destination: contract.TrafficReplayDestination{Environment: environment, Provider: "local", URL: "http://orders." + environment + ".store.localhost"}}, nil
}

func manager(t *testing.T, resolve Resolver, execute Executor) *Manager {
	t.Helper()
	if resolve == nil {
		resolve = resolver
	}
	if execute == nil {
		execute = func(context.Context, string, string, string, contract.TrafficReplayDraft, uint64, model.TrafficReplay) (model.TrafficExchange, string, error) {
			result := original()
			result.Sequence = 143
			return result, "response-received", nil
		}
	}
	m := New(resolve, execute)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := m.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return m
}

func prepare(t *testing.T, m *Manager, baseline model.TrafficExchange) contract.TrafficReplayWorkspace {
	t.Helper()
	w, err := m.Create(context.Background(), baseline)
	if err != nil {
		t.Fatal(err)
	}
	w, err = m.Update(context.Background(), w.Project, w.Environment, w.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, Draft: *w.Draft})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func runInput(w contract.TrafficReplayWorkspace) contract.RunTrafficReplayRequest {
	return contract.RunTrafficReplayRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, RunNumber: w.NextRunNumber}
}

func awaitRun(t *testing.T, m *Manager, w contract.TrafficReplayWorkspace) contract.TrafficReplayWorkspace {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, err := m.Get(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity, true)
		if err != nil {
			t.Fatal(err)
		}
		if current.Run != nil && current.Run.State != "running" {
			return current
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("replay worker did not finish")
	return contract.TrafficReplayWorkspace{}
}

func expectStatus(t *testing.T, err error, status int) {
	t.Helper()
	var classified *Error
	if !errors.As(err, &classified) || classified.Status != status {
		t.Fatalf("error = %v, want status %d", err, status)
	}
}

func TestPrepareNeverExecutesAndPreservesBaselineFidelity(t *testing.T) {
	var calls atomic.Int32
	m := manager(t, nil, func(context.Context, string, string, string, contract.TrafficReplayDraft, uint64, model.TrafficReplay) (model.TrafficExchange, string, error) {
		calls.Add(1)
		return original(), "response-received", nil
	})
	baseline := original()
	baseline.RequestHeaders["Connection"] = []string{"X-Internal"}
	baseline.RequestHeaders["X-Internal"] = []string{"do not copy"}
	baseline.RequestHeaders["Host"] = []string{"private:1234"}
	baseline.RequestHeaders["Traceparent"] = []string{"old"}
	baseline.RequestHeaders["Origin"] = []string{"http://browser"}
	w := prepare(t, m, baseline)
	if calls.Load() != 0 {
		t.Fatal("preparation sent application traffic")
	}
	if w.Draft.RequestTarget != baseline.RequestTarget || w.Draft.Body != baseline.RequestBody {
		t.Fatal("preparation changed raw request bytes")
	}
	for _, name := range []string{"Connection", "X-Internal", "Host", "Traceparent", "Origin"} {
		if _, ok := w.Draft.Headers[name]; ok {
			t.Errorf("copied excluded header %s", name)
		}
	}
	if fmt.Sprint(w.Draft.Headers["X-Repeat"]) != "[first second]" {
		t.Fatal("repeated values changed")
	}
	w.Baseline.ResponseHeaders["Content-Type"][0] = "changed"
	baseline.RequestHeaders["X-Repeat"][0] = "changed"
	copy, err := m.Get(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity, true)
	if err != nil {
		t.Fatal(err)
	}
	if copy.Baseline.RequestHeaders["X-Repeat"][0] != "first" || copy.Baseline.ResponseHeaders["Content-Type"][0] != "application/json" {
		t.Fatal("workspace aliases caller-owned baseline headers")
	}
}

func TestDraftValidationRejectsUnsafeTargetsAndHeaders(t *testing.T) {
	baseline := original()
	draft, _ := initialDraft(baseline)
	for _, target := range []string{"https://example.test/a", "//example.test/a", "/a#fragment", "/a%gg", "/a?q=%zz", "/a\r\nb", "/a b", "/a\\b", "/" + strings.Repeat("a", maxPathBytes)} {
		t.Run(target[:min(len(target), 30)], func(t *testing.T) {
			input := cloneDraft(draft)
			input.RequestTarget = target
			_, err := validateDraft(input, baseline)
			expectStatus(t, err, 400)
		})
	}
	for _, header := range []string{"Host", "Connection", "Content-Length", "Transfer-Encoding", "Traceparent", "Baggage", "X-Forwarded-Host", "X-Portless-Test", "X-Csrf-Token", "Upgrade", "Expect", "Proxy-Authorization"} {
		t.Run(header, func(t *testing.T) {
			input := cloneDraft(draft)
			input.Headers[header] = []string{"bad"}
			_, err := validateDraft(input, baseline)
			expectStatus(t, err, 400)
		})
	}
	for _, headers := range []map[string][]string{
		{"X-Value": {"bad\r\nHeader: injected"}}, {"X-Value": {redacted}}, {"Cookie": {"portless_session=secret"}}, {"Content-Encoding": {"gzip"}}, {"Accept-Encoding": {"gzip"}}, {"Content-Type": {"multipart/form-data"}}, {"X-Value": {"a"}, "x-value": {"b"}},
	} {
		input := cloneDraft(draft)
		input.Headers = headers
		_, err := validateDraft(input, baseline)
		expectStatus(t, err, 400)
	}
	input := cloneDraft(draft)
	input.RequestTarget = "/orders?value=" + redacted
	_, err := validateDraft(input, baseline)
	expectStatus(t, err, 400)
	input = cloneDraft(draft)
	input.Headers["Origin"] = []string{"https://app.test"}
	input.Headers["Referer"] = []string{"https://app.test/order"}
	if _, err := validateDraft(input, baseline); err != nil {
		t.Fatalf("intentional application browser metadata rejected: %v", err)
	}
}

func TestCaptureRequiresExplicitReplacementOrEmptyBody(t *testing.T) {
	for _, state := range []string{"truncated", "omitted", "unsupported", "incomplete", ""} {
		t.Run(state, func(t *testing.T) {
			baseline := original()
			baseline.RequestCapture.State = state
			draft, limitations := initialDraft(baseline)
			if len(limitations) == 0 {
				t.Fatal("missing body limitation")
			}
			_, err := validateDraft(draft, baseline)
			expectStatus(t, err, 400)
			draft.BodyMode = "replacement"
			draft.Body = `{"replacement":true}`
			if _, err := validateDraft(draft, baseline); err != nil {
				t.Fatal(err)
			}
			draft.BodyMode = "empty"
			draft.Body = ""
			if _, err := validateDraft(draft, baseline); err != nil {
				t.Fatal(err)
			}
		})
	}
	baseline := original()
	baseline.RequestBody = ""
	baseline.RequestCapture = nil
	draft, _ := initialDraft(baseline)
	_, err := validateDraft(draft, baseline)
	expectStatus(t, err, 400)
	baseline.RequestCapture = captured("")
	draft, _ = initialDraft(baseline)
	if _, err := validateDraft(draft, baseline); err != nil {
		t.Fatal(err)
	}
	baseline = original()
	draft, _ = initialDraft(baseline)
	draft.Body += "edited"
	_, err = validateDraft(draft, baseline)
	expectStatus(t, err, 400)
}

func TestCredentialsAreExplicitAndNeverReturnedOrEchoed(t *testing.T) {
	const secret = "only-for-application-credential"
	baseline := original()
	baseline.RequestHeaders["Authorization"] = []string{redacted}
	var sent atomic.Bool
	m := manager(t, nil, func(_ context.Context, _, _, _ string, draft contract.TrafficReplayDraft, _ uint64, _ model.TrafficReplay) (model.TrafficExchange, string, error) {
		sent.Store(draft.Headers["Authorization"][0] == "Bearer "+secret)
		exchange := original()
		exchange.ResponseBody = secret
		exchange.ResponseHeaders["X-Echo"] = []string{secret}
		exchange.ResponseCapture = captured(secret)
		return exchange, "response-received", nil
	})
	w, err := m.Create(context.Background(), baseline)
	if err != nil {
		t.Fatal(err)
	}
	input := contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, Draft: *w.Draft}
	_, err = m.Update(context.Background(), w.Project, w.Environment, w.Number, input)
	expectStatus(t, err, 400)
	input.Draft.Headers["Authorization"] = []string{"Bearer " + secret}
	w, err = m.Update(context.Background(), w.Project, w.Environment, w.Number, input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(w)
	if strings.Contains(string(encoded), secret) {
		t.Fatal("prepared response leaked credentials")
	}
	if _, err = m.Run(context.Background(), w.Project, w.Environment, w.Number, runInput(w)); err != nil {
		t.Fatal(err)
	}
	result := awaitRun(t, m, w)
	encoded, _ = json.Marshal(result)
	if !sent.Load() {
		t.Fatal("explicit runtime credential not dispatched")
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatal("run result retained an echoed credential")
	}
	if result.Result.Comparison.Body.State != "partial" {
		t.Fatal("redacted response claimed complete comparison")
	}
	_, err = m.Run(context.Background(), w.Project, w.Environment, w.Number, contract.RunTrafficReplayRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, RunNumber: 2})
	expectStatus(t, err, 409)
	w2, err := m.Create(context.Background(), baseline)
	if err != nil {
		t.Fatal(err)
	}
	draft := *w2.Draft
	delete(draft.Headers, "Authorization")
	draft.OmittedHeaders = []string{"Authorization"}
	if _, err = m.Update(context.Background(), w2.Project, w2.Environment, w2.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: w2.TrafficReplayIdentity, Revision: w2.Revision, Draft: draft}); err != nil {
		t.Fatalf("explicit credential omission failed: %v", err)
	}
}

func TestConcurrentDuplicateRunsHaveOneDispatchAndTombstones(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	finish := make(chan struct{})
	m := manager(t, nil, func(ctx context.Context, scope, source, target string, _ contract.TrafficReplayDraft, _ uint64, provenance model.TrafficReplay) (model.TrafficExchange, string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		if scope != "store/local" || source != "checkout" || target != "orders" || provenance.Sequence != 142 {
			t.Error("logical edge or provenance changed")
		}
		select {
		case <-ctx.Done():
			return model.TrafficExchange{}, "unknown", ctx.Err()
		case <-finish:
			return original(), "response-received", nil
		}
	})
	w := prepare(t, m, original())
	input := runInput(w)
	var clients sync.WaitGroup
	for range 20 {
		clients.Add(1)
		go func() {
			defer clients.Done()
			_, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, input)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	clients.Wait()
	<-started
	if calls.Load() != 1 {
		t.Fatalf("got %d dispatches", calls.Load())
	}
	if err := m.Delete(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity); err != nil {
		t.Fatal(err)
	}
	closed, err := m.Get(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity, true)
	if err != nil || closed.Baseline != nil || closed.Draft != nil || closed.Result != nil || closed.Run.State != "running" {
		t.Fatalf("closing an active session did not release its payloads: %+v, %v", closed, err)
	}
	expectStatus(t, m.Touch(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity), 410)
	conflict := input
	conflict.ConfirmRemoteWrite = true
	_, err = m.Run(context.Background(), w.Project, w.Environment, w.Number, conflict)
	expectStatus(t, err, 409)
	close(finish)
	complete := awaitRun(t, m, w)
	if complete.Run.State != "completed" || complete.Run.Outcome != "response-received" {
		t.Fatalf("bad receipt: %+v", complete.Run)
	}
	if err = m.Delete(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity); err != nil {
		t.Fatal(err)
	}
	deleted, err := m.Get(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity, true)
	if err != nil || deleted.Baseline != nil || deleted.Result != nil || len(deleted.Receipts) != 1 {
		t.Fatalf("deleted workspace did not retain safe receipt inspection: %+v, %v", deleted, err)
	}
	receipt, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, input)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Baseline != nil || receipt.Result != nil || len(receipt.Receipts) != 1 || calls.Load() != 1 {
		t.Fatal("disposed duplicate lost receipt or redispatched")
	}
}

func TestOldRunCannotDispatchAfterNewResult(t *testing.T) {
	var calls atomic.Int32
	m := manager(t, nil, func(context.Context, string, string, string, contract.TrafficReplayDraft, uint64, model.TrafficReplay) (model.TrafficExchange, string, error) {
		calls.Add(1)
		return original(), "response-received", nil
	})
	w := prepare(t, m, original())
	first := runInput(w)
	if _, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, first); err != nil {
		t.Fatal(err)
	}
	w = awaitRun(t, m, w)
	var err error
	w, err = m.Update(context.Background(), w.Project, w.Environment, w.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, Draft: *w.Draft})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Run(context.Background(), w.Project, w.Environment, w.Number, runInput(w)); err != nil {
		t.Fatal(err)
	}
	w = awaitRun(t, m, w)
	if w.Result.RunNumber != 2 || len(w.Receipts) != 2 {
		t.Fatal("result/receipt history incorrect")
	}
	old, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, first)
	if err != nil {
		t.Fatal(err)
	}
	if old.Run.Number != 1 {
		t.Fatal("duplicate old submission returned the latest run's identity")
	}
	if calls.Load() != 2 {
		t.Fatal("old receipt redispatched")
	}
}

func TestRemotePolicyAndDestinationRevision(t *testing.T) {
	var generation atomic.Uint64
	generation.Store(1)
	policy := "read-only"
	resolve := func(ctx context.Context, project, environment, source, target string) (Target, error) {
		value, _ := resolver(ctx, project, environment, source, target)
		value.Generation = generation.Load()
		value.Destination.Provider = "remote"
		value.Destination.WritePolicy = policy
		value.Destination.Classification = "qa"
		return value, nil
	}
	m := manager(t, resolve, nil)
	w, err := m.Create(context.Background(), original())
	if err != nil {
		t.Fatal(err)
	}
	input := contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, Draft: *w.Draft}
	_, err = m.Update(context.Background(), w.Project, w.Environment, w.Number, input)
	expectStatus(t, err, 403)
	policy = "read-write"
	w, err = m.Update(context.Background(), w.Project, w.Environment, w.Number, input)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Destination.RequiresConfirmation {
		t.Fatal("remote mutation was not bound to confirmation")
	}
	run := runInput(w)
	_, err = m.Run(context.Background(), w.Project, w.Environment, w.Number, run)
	expectStatus(t, err, 403)
	run.ConfirmRemoteWrite = true
	generation.Store(2)
	_, err = m.Run(context.Background(), w.Project, w.Environment, w.Number, run)
	expectStatus(t, err, 409)
	generation.Store(1)
	if _, err = m.Run(context.Background(), w.Project, w.Environment, w.Number, run); err != nil {
		t.Fatal(err)
	}
	awaitRun(t, m, w)
}

func TestExpiryIdentityAndCapacity(t *testing.T) {
	m := manager(t, nil, nil)
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	m.mu.Lock()
	m.now = func() time.Time { return time.Unix(0, clock.Load()) }
	m.mu.Unlock()
	w := prepare(t, m, original())
	wrong := w.TrafficReplayIdentity
	wrong.DaemonStartedAt = wrong.DaemonStartedAt.Add(-time.Second)
	_, err := m.Get(w.Project, w.Environment, w.Number, wrong, true)
	expectStatus(t, err, 410)
	_, err = m.Get(w.Project, w.Environment, w.Number, contract.TrafficReplayIdentity{CreatedAt: w.CreatedAt}, true)
	expectStatus(t, err, 400)
	clock.Add(int64(time.Minute))
	_, err = m.Run(context.Background(), w.Project, w.Environment, w.Number, runInput(w))
	expectStatus(t, err, 409)
	m.mu.Lock()
	if m.workspaces[workspaceKey{w.Project, w.Environment, w.Number}].runtime != nil {
		t.Error("expired runtime secret preparation retained")
	}
	m.mu.Unlock()
	for range maxOriginWorkspaces - 1 {
		if _, err = m.Create(context.Background(), original()); err != nil {
			t.Fatal(err)
		}
	}
	_, err = m.Create(context.Background(), original())
	expectStatus(t, err, 429)
	clock.Add(int64(workspaceIdleTimeout))
	_, err = m.Get(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity, true)
	expectStatus(t, err, 410)
	if _, err = m.Create(context.Background(), original()); err != nil {
		t.Fatal(err)
	}
}

func TestClearDoesNotRepublishRunningResult(t *testing.T) {
	finish := make(chan struct{})
	m := manager(t, nil, func(ctx context.Context, _, _, _ string, _ contract.TrafficReplayDraft, _ uint64, _ model.TrafficReplay) (model.TrafficExchange, string, error) {
		select {
		case <-finish:
			return original(), "response-received", nil
		case <-ctx.Done():
			return model.TrafficExchange{}, "unknown", ctx.Err()
		}
	})
	w := prepare(t, m, original())
	input := runInput(w)
	if _, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, input); err != nil {
		t.Fatal(err)
	}
	m.Clear(w.Project, w.Environment)
	close(finish)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		receipt, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, input)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Run.State != "running" {
			if receipt.Result != nil || receipt.Baseline != nil {
				t.Fatal("clear republished payload")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cleared run did not finish")
}

func TestRunAdmissionLosesRaceToDraftChangeOrClear(t *testing.T) {
	for _, action := range []string{"update", "clear"} {
		t.Run(action, func(t *testing.T) {
			resolving, releaseResolver := make(chan struct{}), make(chan struct{})
			var resolves, calls atomic.Int32
			resolve := func(ctx context.Context, project, environment, source, target string) (Target, error) {
				if resolves.Add(1) == 2 {
					close(resolving)
					<-releaseResolver
				}
				return resolver(ctx, project, environment, source, target)
			}
			m := manager(t, resolve, func(context.Context, string, string, string, contract.TrafficReplayDraft, uint64, model.TrafficReplay) (model.TrafficExchange, string, error) {
				calls.Add(1)
				return original(), "response-received", nil
			})
			w := prepare(t, m, original())
			result := make(chan error, 1)
			go func() {
				_, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, runInput(w))
				result <- err
			}()
			<-resolving
			status := 409
			if action == "update" {
				draft := cloneDraft(*w.Draft)
				draft.RequestTarget = "/changed"
				if _, err := m.Update(context.Background(), w.Project, w.Environment, w.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, Draft: draft}); err != nil {
					t.Fatal(err)
				}
			} else {
				m.Clear(w.Project, w.Environment)
				status = 410
			}
			close(releaseResolver)
			expectStatus(t, <-result, status)
			if calls.Load() != 0 {
				t.Fatal("stale admission dispatched after draft replacement or Clear")
			}
		})
	}
}

func TestConcurrentCapacityAndShutdownCancelWorkers(t *testing.T) {
	var calls atomic.Int32
	m := manager(t, nil, func(ctx context.Context, _, _, _ string, _ contract.TrafficReplayDraft, _ uint64, _ model.TrafficReplay) (model.TrafficExchange, string, error) {
		calls.Add(1)
		<-ctx.Done()
		return model.TrafficExchange{}, "unknown", ctx.Err()
	})
	for range maxConcurrentRuns {
		w := prepare(t, m, original())
		if _, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, runInput(w)); err != nil {
			t.Fatal(err)
		}
	}
	extra := prepare(t, m, original())
	_, err := m.Run(context.Background(), extra.Project, extra.Environment, extra.Number, runInput(extra))
	expectStatus(t, err, 429)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != maxConcurrentRuns {
		t.Fatal("excess run was queued or executed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, workspace := range m.workspaces {
		if workspace.runtime != nil || workspace.value.Baseline != nil || workspace.value.Result != nil {
			t.Fatal("shutdown retained draft credentials or payloads")
		}
		if workspace.value.Run != nil && workspace.value.Run.State != "interrupted" {
			t.Fatalf("shutdown receipt = %+v", workspace.value.Run)
		}
	}
}

func TestWorkspaceAndInputBudgets(t *testing.T) {
	m := manager(t, nil, nil)
	for _, change := range []func(*model.TrafficExchange){
		func(exchange *model.TrafficExchange) {
			exchange.RequestHeaders["X-Large"] = []string{strings.Repeat("x", maxHeaderBytes)}
		},
		func(exchange *model.TrafficExchange) {
			exchange.ResponseHeaders["X-Large"] = []string{strings.Repeat("x", maxResponseHeaderBytes)}
		},
		func(exchange *model.TrafficExchange) { exchange.ResponseBody = strings.Repeat("x", maxBodyBytes+1) },
	} {
		baseline := original()
		change(&baseline)
		_, err := m.Create(context.Background(), baseline)
		expectStatus(t, err, 413)
	}
	baseline := original()
	draft, _ := initialDraft(baseline)
	draft.BodyMode, draft.Body = "replacement", strings.Repeat("x", contract.TrafficReplayMaxBodyBytes+1)
	_, err := validateDraft(draft, baseline)
	expectStatus(t, err, 413)
	for i := range maxWorkspaces {
		baseline := original()
		baseline.Environment = fmt.Sprintf("env-%d", i/maxOriginWorkspaces)
		if _, err := m.Create(context.Background(), baseline); err != nil {
			t.Fatal(err)
		}
	}
	baseline.Environment = "extra"
	_, err = m.Create(context.Background(), baseline)
	expectStatus(t, err, 429)
}

func TestKnownStreamingAndNonTextRequestsAreExcluded(t *testing.T) {
	m := manager(t, nil, nil)
	for _, contentType := range []string{"text/event-stream", "text/event-stream; charset=utf-8", "application/grpc", "application/grpc+json", "application/grpc-web+proto"} {
		t.Run(contentType, func(t *testing.T) {
			for _, response := range []bool{false, true} {
				baseline := original()
				headers := baseline.RequestHeaders
				if response {
					headers = baseline.ResponseHeaders
				}
				headers["Content-Type"] = []string{contentType}
				_, err := m.Create(context.Background(), baseline)
				expectStatus(t, err, 400)
			}
			baseline := original()
			draft, _ := initialDraft(baseline)
			draft.BodyMode, draft.Body = "empty", ""
			draft.Headers["Content-Type"] = []string{contentType}
			_, err := validateDraft(draft, baseline)
			expectStatus(t, err, 400)
		})
	}
	baseline := original()
	baseline.RequestHeaders["Accept"] = []string{"application/json, text/event-stream; q=0.9"}
	_, err := m.Create(context.Background(), baseline)
	expectStatus(t, err, 400)
	baseline = original()
	draft, _ := initialDraft(baseline)
	draft.Headers["Accept"] = []string{"text/event-stream"}
	_, err = validateDraft(draft, baseline)
	expectStatus(t, err, 400)
	for _, contentType := range []string{"application/protobuf", "application/x-protobuf", "application/octet-stream", "multipart/form-data"} {
		draft, _ := initialDraft(baseline)
		draft.Headers["Content-Type"] = []string{contentType}
		_, err := validateDraft(draft, baseline)
		expectStatus(t, err, 400)
	}
}

func TestResultRetentionBoundsInjectedExecutor(t *testing.T) {
	m := manager(t, nil, func(context.Context, string, string, string, contract.TrafficReplayDraft, uint64, model.TrafficReplay) (model.TrafficExchange, string, error) {
		exchange := original()
		exchange.ResponseBody = strings.Repeat("x", maxBodyBytes+10)
		exchange.ResponseCapture = captured(exchange.ResponseBody)
		exchange.ResponseHeaders["X-Large"] = []string{strings.Repeat("x", maxResponseHeaderBytes+1)}
		return exchange, "response-received", nil
	})
	w := prepare(t, m, original())
	if _, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, runInput(w)); err != nil {
		t.Fatal(err)
	}
	w = awaitRun(t, m, w)
	if len(w.Result.Exchange.ResponseBody) != maxBodyBytes || w.Result.Exchange.ResponseCapture.State != "truncated" || len(w.Result.Exchange.ResponseHeaders) != 0 || w.Result.Comparison.Headers.State != "unavailable" {
		t.Fatalf("retention budgets not enforced: %+v", w.Result.Limitations)
	}
}

func TestNewCredentialScrubsBaselineAndPreviousResultMonotonically(t *testing.T) {
	const secret = "newly-supplied-credential"
	baseline := original()
	baseline.RequestTarget = "/orders?value=" + secret
	baseline.ResponseBody = `{"echo":"` + secret + `"}`
	baseline.ResponseCapture = captured(baseline.ResponseBody)
	baseline.ResponseHeaders["X-Echo"] = []string{secret}
	m := manager(t, nil, func(context.Context, string, string, string, contract.TrafficReplayDraft, uint64, model.TrafficReplay) (model.TrafficExchange, string, error) {
		return baseline, "response-received", nil
	})
	w := prepare(t, m, baseline)
	if _, err := m.Run(context.Background(), w.Project, w.Environment, w.Number, runInput(w)); err != nil {
		t.Fatal(err)
	}
	w = awaitRun(t, m, w)
	draft := cloneDraft(*w.Draft)
	draft.Headers["Authorization"] = []string{"Bearer " + secret}
	var err error
	w, err = m.Update(context.Background(), w.Project, w.Environment, w.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, Draft: draft})
	if err != nil {
		t.Fatal(err)
	}
	assertSafe := func(value contract.TrafficReplayWorkspace) {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil || strings.Contains(string(encoded), secret) {
			t.Fatal("new credential remained in baseline, previous request, exchange, or comparison")
		}
		if value.Baseline.ResponseCapture.Exact {
			t.Fatal("redacted baseline body still claims exact capture")
		}
	}
	assertSafe(w)
	w, err = m.Get(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity, true)
	if err != nil {
		t.Fatal(err)
	}
	assertSafe(w)
	if _, err = m.Run(context.Background(), w.Project, w.Environment, w.Number, runInput(w)); err != nil {
		t.Fatal(err)
	}
	assertSafe(awaitRun(t, m, w))
}

func TestComparisonPreservesNumbersTypesAndJSONAmbiguity(t *testing.T) {
	tests := []struct{ name, before, after, state, format, path string }{
		{"object-order", `{"b":2,"a":1}`, `{ "a":1,"b":2 }`, "equal", "json", ""},
		{"large-integer", `{"n":9007199254740992}`, `{"n":9007199254740993}`, "different", "json", "/n"},
		{"numeric-spelling", `{"n":1}`, `{"n":1.0}`, "different", "json", "/n"},
		{"missing-null", `{}`, `{"n":null}`, "different", "json", "/n"},
		{"type", `{"n":1}`, `{"n":"1"}`, "different", "json", "/n"},
		{"array-order", `[1,2]`, `[2,1]`, "different", "json", "/0"},
		{"pointer-escape", `{"a/b~c":1}`, `{"a/b~c":2}`, "different", "json", "/a~1b~0c"},
		{"duplicate", `{"n":1,"n":2}`, `{"n":2}`, "different", "text", ""},
		{"trailing", `{} {}`, `{}`, "different", "text", ""},
		{"invalid", `{"n":`, `{}`, "different", "text", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before, after := original(), original()
			before.ResponseBody = test.before
			after.ResponseBody = test.after
			before.ResponseCapture = captured(test.before)
			after.ResponseCapture = captured(test.after)
			comparison := Compare(before, after)
			if comparison.Body.State != test.state || comparison.Body.Format != test.format {
				t.Fatalf("comparison = %+v", comparison.Body)
			}
			if test.path != "" && (len(comparison.Body.Changes) == 0 || comparison.Body.Changes[0].Path != test.path) {
				t.Fatalf("changes = %+v", comparison.Body.Changes)
			}
			if test.name == "large-integer" && comparison.Body.Changes[0].After != "9007199254740993" {
				t.Fatal("integer rounded")
			}
			if test.format == "text" && comparison.Body.Reason == "" {
				t.Fatal("JSON fallback reason missing")
			}
		})
	}
}

func TestComparisonPartialEvidenceAndBounds(t *testing.T) {
	before, after := original(), original()
	before.ResponseHeaders["Set-Cookie"] = []string{redacted}
	after.ResponseHeaders["set-cookie"] = []string{redacted}
	comparison := Compare(before, after)
	if comparison.State != "partial" || comparison.Headers.State != "partial" {
		t.Fatal("redacted values claimed equal")
	}
	before.ResponseCapture.State = "truncated"
	comparison = Compare(before, after)
	if comparison.Body.State != "partial" || comparison.Body.Format != "text" {
		t.Fatal("partial JSON parsed as complete document")
	}
	after.Status = 0
	comparison = Compare(before, after)
	if comparison.State != "unavailable" || comparison.Headers.State != "unavailable" {
		t.Fatal("missing response treated as an ordinary status")
	}
	before, after = original(), original()
	before.ResponseHeaders = map[string][]string{"x-repeat": {"first", "second"}}
	after.ResponseHeaders = map[string][]string{"X-Repeat": {"second", "first"}}
	comparison = Compare(before, after)
	if comparison.Headers.State != "different" {
		t.Fatal("header array order ignored")
	}
	deep := strings.Repeat("[", maxJSONDepth+2) + "0" + strings.Repeat("]", maxJSONDepth+2)
	if _, reason := parseJSON(deep); reason == "" {
		t.Fatal("unbounded JSON depth accepted")
	}
	nodes := "[" + strings.Repeat("0,", maxJSONNodes) + "0]"
	if _, reason := parseJSON(nodes); reason == "" {
		t.Fatal("unbounded JSON nodes accepted")
	}
	text := strings.Repeat("line\n", maxTextLines+1)
	section := compareText(text, text+"changed")
	if section.State != "different" || section.Reason == "" || len(section.Changes) != 0 {
		t.Fatal("text work limit not enforced")
	}
	collector := &changeCollector{}
	for i := range maxChanges + 5 {
		collector.add(fmt.Sprint(i), "changed", "one", "two")
	}
	if len(collector.changes) != maxChanges || !collector.limited {
		t.Fatal("change limit not enforced")
	}
}

func FuzzReplayRequestTarget(f *testing.F) {
	for _, seed := range []string{"/", "/a%2Fb?x=&x=+", "//example.test", "/bad%xy", "/a\r\nb"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, target string) {
		if validRequestTarget(target) && (!strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.ContainsAny(target, "\r\n#\\") || len(target) > maxPathBytes) {
			t.Fatal("unsafe accepted target")
		}
	})
}

func FuzzReplayJSONComparison(f *testing.F) {
	f.Add(`{"n":9007199254740993}`, `{"n":9007199254740994}`)
	f.Add(`{"x":1,"x":2}`, `{"x":2}`)
	f.Fuzz(func(t *testing.T, left, right string) {
		if len(left) > maxBodyBytes || len(right) > maxBodyBytes {
			return
		}
		before, after := original(), original()
		before.ResponseBody = left
		after.ResponseBody = right
		before.ResponseCapture = captured(left)
		after.ResponseCapture = captured(right)
		result := Compare(before, after)
		if len(result.Body.Changes) > maxChanges {
			t.Fatal("unbounded changes")
		}
	})
}

func TestActivityRenewsIdleTimeoutWithoutRenewingSecretsOrChangingTheRequest(t *testing.T) {
	m := manager(t, nil, nil)
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	m.mu.Lock()
	m.now = func() time.Time { return time.Unix(0, clock.Load()) }
	m.mu.Unlock()
	w := prepare(t, m, original())
	for range 3 {
		clock.Add(int64(40 * time.Minute))
		if err := m.Touch(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity); err != nil {
			t.Fatal(err)
		}
		current, err := m.Get(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity, true)
		if err != nil || current.Baseline == nil || current.Revision != w.Revision || current.NextRunNumber != 1 || current.Run != nil || !current.PreparedUntil.Equal(w.PreparedUntil) {
			t.Fatalf("activity changed the request or failed to keep it alive: %+v, %v", current, err)
		}
		m.mu.Lock()
		if m.workspaces[workspaceKey{w.Project, w.Environment, w.Number}].runtime != nil {
			t.Error("activity retained expired prepared credentials")
		}
		m.mu.Unlock()
	}
	clock.Add(int64(time.Hour - time.Nanosecond))
	if _, err := m.Get(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity, true); err != nil {
		t.Fatal(err)
	}
	clock.Add(1)
	_, err := m.Get(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity, true)
	expectStatus(t, err, 410)
	expectStatus(t, m.Touch(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity), 410)
}

func TestDraftAndRunActivityRenewIdleTimeout(t *testing.T) {
	m := manager(t, nil, nil)
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	m.mu.Lock()
	m.now = func() time.Time { return time.Unix(0, clock.Load()) }
	m.mu.Unlock()
	w := prepare(t, m, original())
	clock.Add(int64(59 * time.Minute))
	var err error
	w, err = m.Update(t.Context(), w.Project, w.Environment, w.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, Draft: *w.Draft})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	if !m.workspaces[workspaceKey{w.Project, w.Environment, w.Number}].idleUntil.Equal(m.now().Add(time.Hour)) {
		t.Error("preparing the request did not renew its idle timeout")
	}
	m.mu.Unlock()
	clock.Add(int64(30 * time.Second))
	if _, err = m.Run(t.Context(), w.Project, w.Environment, w.Number, runInput(w)); err != nil {
		t.Fatal(err)
	}
	awaitRun(t, m, w)
	m.mu.Lock()
	if !m.workspaces[workspaceKey{w.Project, w.Environment, w.Number}].idleUntil.Equal(m.now().Add(time.Hour)) {
		t.Error("sending the request did not renew its idle timeout")
	}
	m.mu.Unlock()
}

func TestClosedReceiptsDoNotConsumeActiveWorkspaceSlots(t *testing.T) {
	m := manager(t, nil, nil)
	for range maxOriginWorkspaces + 1 {
		w := prepare(t, m, original())
		if _, err := m.Run(t.Context(), w.Project, w.Environment, w.Number, runInput(w)); err != nil {
			t.Fatal(err)
		}
		awaitRun(t, m, w)
		if err := m.Delete(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity); err != nil {
			t.Fatal(err)
		}
	}
}
