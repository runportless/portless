// Package replay owns bounded request workspaces, explicit run admission, and response comparison.
package replay

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

const (
	workspaceIdleTimeout = time.Hour
	secretLifetime       = 59 * time.Second
	runDeadline          = 30 * time.Second
	maxWorkspaces        = 32
	maxOriginWorkspaces  = 8
	maxRuns              = 32
	maxConcurrentRuns    = 4
	maxStateBytes        = 1 << 30
	runReservation       = 8 << 20
)

// Target binds public destination policy to an immutable private proxy generation and topology version.
type Target struct {
	Destination     contract.TrafficReplayDestination
	Generation      uint64
	RoutingRevision uint64
	Version         string
}

// Resolver validates one logical edge and returns its current destination without application I/O.
type Resolver func(context.Context, string, string, string, string, string, string) (Target, error)

// Executor executes one admitted request through its source-aware proxy and returns the exact safe exchange and dispatch outcome.
type Executor func(context.Context, string, string, string, contract.TrafficReplayDraft, uint64, uint64, model.TrafficReplay) (model.TrafficExchange, string, error)

type workspaceKey struct {
	project, environment string
	number               int64
}
type originKey struct{ project, environment string }
type workspace struct {
	value               contract.TrafficReplayWorkspace
	target              Target
	runtime             *contract.TrafficReplayDraft
	confirmations       map[int64]bool
	bytes               int
	running             bool
	disposed            bool
	idleUntil           time.Time
	receiptDestinations []string
}

// Manager retains bounded ephemeral workspaces and suppresses duplicate dispatch for every admitted run.
type Manager struct {
	mu          sync.Mutex
	resolver    Resolver
	executor    Executor
	startedAt   time.Time
	lastCreated time.Time
	workspaces  map[workspaceKey]*workspace
	numbers     map[originKey]int64
	bytes       int
	running     int
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	workers     sync.WaitGroup
	sweeperDone chan struct{}
	now         func() time.Time
}

// New starts a bounded workspace manager using injected policy resolution and proxy execution.
func New(resolver Resolver, executor Executor) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{resolver: resolver, executor: executor, startedAt: time.Now().UTC(), workspaces: map[workspaceKey]*workspace{}, numbers: map[originKey]int64{}, ctx: ctx, cancel: cancel, sweeperDone: make(chan struct{}), now: time.Now}
	go m.sweep()
	return m
}

// StartedAt identifies this manager's daemon lifetime for stale receipt detection.
func (m *Manager) StartedAt() time.Time { return m.startedAt }

func (m *Manager) sweep() {
	defer close(m.sweeperDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			m.expireLocked(m.now())
			m.mu.Unlock()
		}
	}
}

func (m *Manager) expireLocked(now time.Time) {
	for key, w := range m.workspaces {
		if w.runtime != nil && !now.Before(w.value.PreparedUntil) {
			w.runtime = nil
			m.recountLocked(w)
		}
		if !now.Before(w.idleUntil) && !w.running {
			m.bytes -= w.bytes
			delete(m.workspaces, key)
		}
	}
}

func (m *Manager) findLocked(project, origin string, number int64, expected contract.TrafficReplayIdentity, required, allowDisposed bool) (*workspace, error) {
	if m.closed {
		return nil, failure("replay_unavailable", "The replay manager is shutting down.", 503)
	}
	m.expireLocked(m.now())
	if required && (expected.CreatedAt.IsZero() || expected.DaemonStartedAt.IsZero()) || expected.CreatedAt.IsZero() != expected.DaemonStartedAt.IsZero() {
		return nil, failure("replay_identity_required", "Both workspace creation and daemon start timestamps are required.", 400)
	}
	if !expected.DaemonStartedAt.IsZero() && !expected.DaemonStartedAt.Equal(m.startedAt) {
		return nil, failure("replay_identity_changed", "The daemon changed; this replay receipt cannot address its workspaces.", 410)
	}
	w := m.workspaces[workspaceKey{project, origin, number}]
	if w == nil {
		if number > 0 && number <= m.numbers[originKey{project, origin}] {
			return nil, failure("replay_expired", "The replay workspace expired or was released.", 410)
		}
		return nil, failure("replay_not_found", "The replay workspace is unavailable.", 404)
	}
	if !expected.CreatedAt.IsZero() && !expected.CreatedAt.Equal(w.value.CreatedAt) {
		return nil, failure("replay_identity_changed", "The workspace identity no longer matches this receipt.", 410)
	}
	if w.disposed && !allowDisposed || !m.now().Before(w.idleUntil) && !allowDisposed {
		return nil, failure("replay_expired", "The replay workspace expired or was released.", 410)
	}
	return w, nil
}

func cloneExchange(input model.TrafficExchange) model.TrafficExchange {
	input.RequestHeaders = cloneHeaders(input.RequestHeaders)
	input.ResponseHeaders = cloneHeaders(input.ResponseHeaders)
	if input.RequestCapture != nil {
		value := *input.RequestCapture
		input.RequestCapture = &value
	}
	if input.ResponseCapture != nil {
		value := *input.ResponseCapture
		input.ResponseCapture = &value
	}
	if input.Replay != nil {
		value := *input.Replay
		input.Replay = &value
	}
	return input
}

func scrubExchange(input model.TrafficExchange, secrets []string) model.TrafficExchange {
	input = cloneExchange(input)
	for _, headers := range []map[string][]string{input.RequestHeaders, input.ResponseHeaders} {
		for name, values := range headers {
			if sensitiveHeader(name) {
				headers[name] = []string{redacted}
				continue
			}
			for i := range values {
				values[i] = redactValues(values[i], secrets)
			}
		}
	}
	for _, body := range []struct {
		text    *string
		capture **model.HTTPCapture
	}{{&input.RequestBody, &input.RequestCapture}, {&input.ResponseBody, &input.ResponseCapture}} {
		original := *body.text
		*body.text = redactValues(original, secrets)
		if *body.text != original && *body.capture != nil {
			(*body.capture).Exact = false
			(*body.capture).State = "omitted"
			(*body.capture).CapturedBytes = int64(len(*body.text))
		}
	}
	input.RequestTarget = redactValues(input.RequestTarget, secrets)
	input.Path = redactValues(input.Path, secrets)
	// Proxy errors are classified separately; never retain arbitrary executor errors.
	if input.Error != "" {
		input.Error = "The application exchange did not finish successfully."
	}
	return input
}

func snapshot(w *workspace, full bool) contract.TrafficReplayWorkspace {
	value := w.value
	value.Receipts = append([]contract.TrafficReplayRun(nil), value.Receipts...)
	value.Limitations = append([]contract.TrafficReplayLimitation(nil), value.Limitations...)
	if value.Run != nil {
		run := *value.Run
		value.Run = &run
	}
	if value.Destination != nil {
		destination := *value.Destination
		value.Destination = &destination
	}
	if !full {
		value.Baseline = nil
		value.Draft = nil
		value.Result = nil
		return value
	}
	if value.Baseline != nil {
		baseline := cloneExchange(*value.Baseline)
		value.Baseline = &baseline
	}
	if value.Draft != nil {
		draft := cloneDraft(*value.Draft)
		value.Draft = &draft
	}
	if value.Result != nil {
		result := *value.Result
		result.Request = cloneDraft(result.Request)
		result.Limitations = append([]contract.TrafficReplayLimitation(nil), result.Limitations...)
		result.Comparison.Body.Changes = append([]contract.TrafficReplayChange(nil), result.Comparison.Body.Changes...)
		result.Comparison.Headers.Changes = append([]contract.TrafficReplayChange(nil), result.Comparison.Headers.Changes...)
		if result.Exchange != nil {
			exchange := cloneExchange(*result.Exchange)
			result.Exchange = &exchange
		}
		value.Result = &result
	}
	return value
}

func sizeOf(w *workspace) int {
	encoded, _ := json.Marshal(w.value)
	size := len(encoded)*2 + 4096 + len(w.confirmations)*64
	if w.runtime != nil {
		runtime, _ := json.Marshal(w.runtime)
		size += len(runtime) * 2
	}
	if w.running {
		size += runReservation
	}
	return size
}

func (m *Manager) recountLocked(w *workspace) {
	size := sizeOf(w)
	m.bytes += size - w.bytes
	w.bytes = size
}

func safeFailure(err error, fallback string) error {
	var classified *Error
	if errors.As(err, &classified) {
		return classified
	}
	return failure("replay_destination_unavailable", fallback, 503)
}

// Create freezes one live HTTP baseline without sending traffic or probing its destination.
func (m *Manager) Create(ctx context.Context, baseline model.TrafficExchange) (contract.TrafficReplayWorkspace, error) {
	if baseline.Protocol != model.ProtocolHTTP || baseline.Status == 101 || baseline.RequestKind == "websocket" || !validMethod(baseline.Method) {
		return contract.TrafficReplayWorkspace{}, failure("replay_unsupported_exchange", "Only supported HTTP requests can be replayed; upgrades and tunnelling are excluded.", 400)
	}
	if !headerBudget(baseline.RequestHeaders, maxHeaderBytes, true) || !headerBudget(baseline.ResponseHeaders, maxResponseHeaderBytes, false) || len(baseline.RequestBody) > maxBodyBytes || len(baseline.ResponseBody) > maxBodyBytes {
		return contract.TrafficReplayWorkspace{}, failure("replay_capture_limit", "The original request or response exceeds the replay capture budget.", 413)
	}
	if streamingHeaders(baseline.RequestHeaders) || streamingHeaders(baseline.ResponseHeaders) {
		return contract.TrafficReplayWorkspace{}, failure("replay_unsupported_stream", "gRPC and explicitly identified event streams are not supported for request replay.", 400)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return contract.TrafficReplayWorkspace{}, failure("replay_unavailable", "The replay manager is shutting down.", 503)
	}
	if err := ctx.Err(); err != nil {
		return contract.TrafficReplayWorkspace{}, failure("replay_unavailable", "Replay preparation was interrupted.", 503)
	}
	m.expireLocked(m.now())
	origin := originKey{baseline.Project, baseline.Environment}
	count, active := 0, 0
	for key, existing := range m.workspaces {
		if existing.disposed {
			continue
		}
		active++
		if key.project == origin.project && key.environment == origin.environment {
			count++
		}
	}
	if active >= maxWorkspaces || count >= maxOriginWorkspaces {
		return contract.TrafficReplayWorkspace{}, failure("replay_capacity", "Replay workspace capacity is reached; close an existing replay or wait for inactive sessions to be cleaned up.", 429)
	}
	baseline = scrubExchange(baseline, nil)
	draft, limitations := initialDraft(baseline)
	created := m.now().UTC()
	if !created.After(m.lastCreated) {
		created = m.lastCreated.Add(time.Nanosecond)
	}
	w := &workspace{confirmations: map[int64]bool{}, idleUntil: created.Add(workspaceIdleTimeout), value: contract.TrafficReplayWorkspace{
		TrafficReplayIdentity: contract.TrafficReplayIdentity{CreatedAt: created, DaemonStartedAt: m.startedAt},
		Project:               baseline.Project, Environment: baseline.Environment, Number: m.numbers[origin] + 1, Revision: 1, NextRunNumber: 1,
		Baseline: &baseline, Draft: &draft, Limitations: limitations,
	}}
	w.bytes = sizeOf(w)
	originBytes := 0
	if _, known := m.numbers[origin]; !known {
		originBytes = len(origin.project) + len(origin.environment) + 128
	}
	if m.bytes+w.bytes+originBytes > maxStateBytes {
		return contract.TrafficReplayWorkspace{}, failure("replay_capacity", "Replay memory capacity is reached.", 429)
	}
	m.workspaces[workspaceKey{baseline.Project, baseline.Environment, w.value.Number}] = w
	m.numbers[origin] = w.value.Number
	m.lastCreated = created
	m.bytes += w.bytes + originBytes
	return snapshot(w, true), nil
}

// Update validates and prepares a complete edited request against the current destination generation.
func (m *Manager) Update(ctx context.Context, project, origin string, number int64, input contract.UpdateTrafficReplayDraftRequest) (contract.TrafficReplayWorkspace, error) {
	m.mu.Lock()
	w, err := m.findLocked(project, origin, number, input.TrafficReplayIdentity, true, false)
	if err != nil {
		m.mu.Unlock()
		return contract.TrafficReplayWorkspace{}, err
	}
	if w.running || input.Revision != w.value.Revision {
		m.mu.Unlock()
		return contract.TrafficReplayWorkspace{}, failure("replay_revision_conflict", "The draft changed or a replay run is active.", 409)
	}
	baseline := cloneExchange(*w.value.Baseline)
	m.mu.Unlock()
	draft, err := validateDraft(input.Draft, baseline)
	if err != nil {
		return contract.TrafficReplayWorkspace{}, err
	}
	if m.resolver == nil {
		return contract.TrafficReplayWorkspace{}, failure("replay_unavailable", "Replay destination resolution is unavailable.", 503)
	}
	target, err := m.resolver(ctx, project, draft.Environment, baseline.Source, baseline.Target, draft.Method, draft.RequestTarget)
	if err != nil {
		return contract.TrafficReplayWorkspace{}, safeFailure(err, "The destination is unavailable for replay.")
	}
	if target.Destination.Provider == "remote" && mutatingMethod(draft.Method) {
		if target.Destination.WritePolicy != "read-write" {
			return contract.TrafficReplayWorkspace{}, failure("replay_remote_read_only", "The destination remote policy blocks this HTTP method.", 403)
		}
		target.Destination.RequiresConfirmation = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w, err = m.findLocked(project, origin, number, input.TrafficReplayIdentity, true, false)
	if err != nil {
		return contract.TrafficReplayWorkspace{}, err
	}
	if w.running || input.Revision != w.value.Revision {
		return contract.TrafficReplayWorkspace{}, failure("replay_revision_conflict", "The draft changed or a replay run is active.", 409)
	}
	updated := *w
	updated.value = w.value
	updated.value.Revision++
	updated.idleUntil = m.now().Add(workspaceIdleTimeout)
	updated.value.PreparedUntil = m.now().UTC().Add(secretLifetime)
	updated.value.Destination = &target.Destination
	safe := safeDraft(draft)
	updated.value.Draft = &safe
	updated.value.Limitations = nil
	// Newly supplied credentials can already appear in an earlier capture or run.
	// Scrubbing the entire retained payload is monotonic across draft revisions.
	secrets := draftSecrets(draft)
	scrubbedBaseline := scrubExchange(*w.value.Baseline, secrets)
	updated.value.Baseline = &scrubbedBaseline
	if w.value.Result != nil {
		previous := *w.value.Result
		previous.Request = scrubDraft(previous.Request, secrets)
		var previousExchange model.TrafficExchange
		if previous.Exchange != nil {
			previousExchange = scrubExchange(*previous.Exchange, secrets)
			previous.Exchange = &previousExchange
		}
		previous.Comparison = Compare(scrubbedBaseline, previousExchange)
		updated.value.Result = &previous
	}
	updated.runtime = &draft
	updated.target = target
	updated.bytes = sizeOf(&updated)
	if m.bytes-w.bytes+updated.bytes > maxStateBytes {
		return contract.TrafficReplayWorkspace{}, failure("replay_capacity", "Replay memory capacity is reached.", 429)
	}
	m.bytes += updated.bytes - w.bytes
	*w = updated
	return snapshot(w, true), nil
}

// Run atomically admits a numbered request and returns existing receipts for identical duplicate submissions.
func (m *Manager) Run(ctx context.Context, project, origin string, number int64, input contract.RunTrafficReplayRequest) (contract.TrafficReplayWorkspace, error) {
	m.mu.Lock()
	w, err := m.findLocked(project, origin, number, input.TrafficReplayIdentity, true, true)
	if err != nil {
		m.mu.Unlock()
		return contract.TrafficReplayWorkspace{}, err
	}
	if receipt, ok := admitted(w, input.RunNumber); ok {
		if receipt.Revision != input.Revision || w.confirmations[input.RunNumber] != input.ConfirmRemoteWrite {
			m.mu.Unlock()
			return contract.TrafficReplayWorkspace{}, failure("replay_run_conflict", "This run number was already admitted with different arguments.", 409)
		}
		value := snapshot(w, false)
		value.Run = &receipt
		m.mu.Unlock()
		return value, nil
	}
	if err = m.canRunLocked(w, input); err != nil {
		m.mu.Unlock()
		return contract.TrafficReplayWorkspace{}, err
	}
	target := w.target
	draft := cloneDraft(*w.runtime)
	baseline := cloneExchange(*w.value.Baseline)
	m.mu.Unlock()
	current, err := m.resolver(ctx, project, draft.Environment, baseline.Source, baseline.Target, draft.Method, draft.RequestTarget)
	if err != nil {
		return contract.TrafficReplayWorkspace{}, safeFailure(err, "The destination is no longer available for replay.")
	}
	current.Destination.RequiresConfirmation = target.Destination.RequiresConfirmation
	if current.Generation != target.Generation || current.RoutingRevision != target.RoutingRevision || current.Version != target.Version || current.Destination != target.Destination {
		return contract.TrafficReplayWorkspace{}, failure("replay_destination_changed", "The destination changed; prepare and review this request again.", 409)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w, err = m.findLocked(project, origin, number, input.TrafficReplayIdentity, true, true)
	if err != nil {
		return contract.TrafficReplayWorkspace{}, err
	}
	if receipt, ok := admitted(w, input.RunNumber); ok {
		if receipt.Revision != input.Revision || w.confirmations[input.RunNumber] != input.ConfirmRemoteWrite {
			return contract.TrafficReplayWorkspace{}, failure("replay_run_conflict", "This run number was already admitted with different arguments.", 409)
		}
		value := snapshot(w, false)
		value.Run = &receipt
		return value, nil
	}
	if err = m.canRunLocked(w, input); err != nil {
		return contract.TrafficReplayWorkspace{}, err
	}
	if m.executor == nil {
		return contract.TrafficReplayWorkspace{}, failure("replay_unavailable", "Replay execution is unavailable.", 503)
	}
	if m.running >= maxConcurrentRuns || m.bytes+runReservation > maxStateBytes {
		return contract.TrafficReplayWorkspace{}, failure("replay_capacity", "Concurrent replay capacity is reached; wait for an active run to finish.", 429)
	}
	started := m.now().UTC()
	run := contract.TrafficReplayRun{Number: input.RunNumber, Revision: input.Revision, State: "running", Outcome: "not-sent", StartedAt: started, Deadline: started.Add(runDeadline)}
	w.value.Run = &run
	w.idleUntil = started.Add(workspaceIdleTimeout)
	w.value.Receipts = append(w.value.Receipts, run)
	if !slices.Contains(w.receiptDestinations, draft.Environment) {
		w.receiptDestinations = append(w.receiptDestinations, draft.Environment)
	}
	w.confirmations[input.RunNumber] = input.ConfirmRemoteWrite
	w.value.NextRunNumber++
	w.runtime = nil
	w.running = true
	m.running++
	m.recountLocked(w)
	workerContext, cancel := context.WithTimeout(m.ctx, runDeadline)
	m.workers.Add(1)
	go m.execute(workerContext, cancel, workspaceKey{project, origin, number}, baseline, draft, target, run)
	return snapshot(w, false), nil
}

func admitted(w *workspace, number int64) (contract.TrafficReplayRun, bool) {
	for _, receipt := range w.value.Receipts {
		if receipt.Number == number {
			return receipt, true
		}
	}
	return contract.TrafficReplayRun{}, false
}

func (m *Manager) canRunLocked(w *workspace, input contract.RunTrafficReplayRequest) error {
	if w.disposed || !m.now().Before(w.idleUntil) {
		return failure("replay_expired", "The replay workspace expired or was released.", 410)
	}
	if w.running || input.RunNumber != w.value.NextRunNumber || input.Revision != w.value.Revision {
		return failure("replay_run_conflict", "The run number or prepared revision is stale, or a run is active.", 409)
	}
	if input.RunNumber > maxRuns {
		return failure("replay_run_limit", "This workspace reached its maximum of 32 admitted runs.", 429)
	}
	if w.runtime == nil || !m.now().Before(w.value.PreparedUntil) {
		return failure("replay_preparation_expired", "Prepare the complete request again before sending; prepared credentials are no longer retained.", 409)
	}
	if w.target.Destination.RequiresConfirmation && !input.ConfirmRemoteWrite {
		return failure("replay_confirmation_required", "Confirm the reviewed remote write before sending this prepared revision.", 403)
	}
	return nil
}

func (m *Manager) execute(ctx context.Context, cancel context.CancelFunc, key workspaceKey, baseline model.TrafficExchange, draft contract.TrafficReplayDraft, target Target, run contract.TrafficReplayRun) {
	defer m.workers.Done()
	defer cancel()
	provenance := model.TrafficReplay{Project: key.project, Environment: key.environment, Sequence: baseline.Sequence, StartedAt: baseline.StartedAt, Workspace: key.number, Run: run.Number}
	exchange, outcome, err := m.executor(ctx, key.project+"/"+draft.Environment, baseline.Source, baseline.Target, draft, target.Generation, target.RoutingRevision, provenance)
	if outcome != "not-sent" && outcome != "response-received" && outcome != "unknown" {
		outcome = "unknown"
	}
	if exchange.Status > 0 {
		outcome = "response-received"
	}
	run.Outcome = outcome
	run.CompletedAt = m.now().UTC()
	run.State = "completed"
	if err != nil || exchange.Status == 0 {
		run.State = "failed"
		run.Error = "The replay did not complete successfully."
		if errors.Is(ctx.Err(), context.Canceled) {
			run.State = "interrupted"
			run.Error = "Replay was interrupted; inspect the dispatch outcome before sending another request."
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			run.Error = "Replay reached its 30-second deadline; inspect the dispatch outcome before sending another request."
		}
	}
	exchange, limits := boundExchange(exchange)
	exchange = scrubExchange(exchange, draftSecrets(draft))
	result := contract.TrafficReplayResult{RunNumber: run.Number, Request: safeDraft(draft), Destination: target.Destination, Comparison: Compare(baseline, exchange), Limitations: limits}
	for _, limitation := range limits {
		if limitation.Field == "responseHeaders" {
			result.Comparison.Headers = contract.TrafficComparisonSection{State: "unavailable", Reason: limitation.Message}
			if result.Comparison.State != "unavailable" {
				result.Comparison.State = "partial"
			}
		}
	}
	if exchange.Status > 0 || exchange.Sequence > 0 {
		result.Exchange = &exchange
	}
	if outcome == "unknown" {
		result.Limitations = append(result.Limitations, contract.TrafficReplayLimitation{Field: "outcome", Code: "delivery-unknown", Message: "The application may have received the request; Portless does not retry it."})
	}
	if exchange.ResponseCapture != nil && !completeBody(exchange.ResponseCapture, exchange.ResponseBody) {
		result.Limitations = append(result.Limitations, contract.TrafficReplayLimitation{Field: "response", Code: "response-incomplete", Message: "The retained response body is incomplete or unavailable."})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.workspaces[key]
	if w == nil {
		return
	}
	w.running = false
	m.running--
	w.value.Run = &run
	for i := range w.value.Receipts {
		if w.value.Receipts[i].Number == run.Number {
			w.value.Receipts[i] = run
		}
	}
	if !w.disposed && !m.closed && m.now().Before(w.idleUntil) {
		w.value.Result = &result
	}
	if m.bytes-w.bytes+sizeOf(w) > maxStateBytes {
		w.value.Result = nil
		w.value.Limitations = []contract.TrafficReplayLimitation{{Field: "result", Code: "result-capacity", Message: "The run completed, but its full result exceeded replay retention capacity. Its receipt remains available."}}
	}
	m.recountLocked(w)
	m.expireLocked(m.now())
}

func boundExchange(exchange model.TrafficExchange) (model.TrafficExchange, []contract.TrafficReplayLimitation) {
	var limitations []contract.TrafficReplayLimitation
	if !headerBudget(exchange.RequestHeaders, maxHeaderBytes, true) {
		exchange.RequestHeaders = nil
		limitations = append(limitations, contract.TrafficReplayLimitation{Field: "requestHeaders", Code: "header-limit", Message: "Request headers exceeded the replay retention budget."})
	}
	if !headerBudget(exchange.ResponseHeaders, maxResponseHeaderBytes, false) {
		exchange.ResponseHeaders = nil
		limitations = append(limitations, contract.TrafficReplayLimitation{Field: "responseHeaders", Code: "header-limit", Message: "Response headers exceeded the replay retention budget; complete header comparison is unavailable."})
	}
	for _, body := range []struct {
		text    *string
		capture **model.HTTPCapture
		field   string
	}{{&exchange.RequestBody, &exchange.RequestCapture, "requestBody"}, {&exchange.ResponseBody, &exchange.ResponseCapture, "responseBody"}} {
		if len(*body.text) <= maxBodyBytes {
			continue
		}
		originalLength := len(*body.text)
		*body.text = strings.Clone(strings.ToValidUTF8((*body.text)[:maxBodyBytes], ""))
		capture := model.HTTPCapture{ObservedBytes: int64(originalLength), Encoding: "identity"}
		if *body.capture != nil {
			capture = **body.capture
		}
		capture.State, capture.Exact, capture.CapturedBytes = "truncated", false, int64(len(*body.text))
		*body.capture = &capture
		limitations = append(limitations, contract.TrafficReplayLimitation{Field: body.field, Code: "body-limit", Message: "Only the bounded body prefix is retained."})
	}
	return exchange, limitations
}

// Get reads safe workspace metadata and optionally its frozen baseline and latest full result.
func (m *Manager) Get(project, origin string, number int64, expected contract.TrafficReplayIdentity, includeResult bool) (contract.TrafficReplayWorkspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, err := m.findLocked(project, origin, number, expected, false, true)
	if err != nil {
		return contract.TrafficReplayWorkspace{}, err
	}
	return snapshot(w, includeResult && !w.disposed && m.now().Before(w.idleUntil)), nil
}

// Status returns payload-free state and every environment addressed by retained receipts.
func (m *Manager) Status(project, origin string, number int64, expected contract.TrafficReplayIdentity) (contract.TrafficReplayStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, err := m.findLocked(project, origin, number, expected, true, true)
	if err != nil {
		return contract.TrafficReplayStatus{}, err
	}
	value := snapshot(w, false)
	destinations := append([]string{}, w.receiptDestinations...)
	if value.Destination != nil && !slices.Contains(destinations, value.Destination.Environment) {
		destinations = append(destinations, value.Destination.Environment)
	}
	if w.value.Draft != nil && !slices.Contains(destinations, w.value.Draft.Environment) {
		destinations = append(destinations, w.value.Draft.Environment)
	}
	if w.value.Result != nil && !slices.Contains(destinations, w.value.Result.Destination.Environment) {
		destinations = append(destinations, w.value.Result.Destination.Environment)
	}
	slices.Sort(destinations)
	return contract.TrafficReplayStatus{TrafficReplayIdentity: value.TrafficReplayIdentity, Project: project, Environment: origin, Number: number, Revision: value.Revision, NextRunNumber: value.NextRunNumber, Closed: w.disposed, Destinations: destinations, Run: value.Run, Receipts: append([]contract.TrafficReplayRun{}, value.Receipts...)}, nil
}

// Touch renews the idle timeout for an existing session without extending prepared credential retention.
func (m *Manager) Touch(project, origin string, number int64, expected contract.TrafficReplayIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, err := m.findLocked(project, origin, number, expected, true, false)
	if err != nil {
		return err
	}
	w.idleUntil = m.now().Add(workspaceIdleTimeout)
	return nil
}

func release(w *workspace) {
	w.disposed = true
	w.runtime = nil
	w.value.Baseline = nil
	w.value.Draft = nil
	w.value.Destination = nil
	w.value.Result = nil
	w.value.Limitations = nil
}

// Delete releases workspace payloads while allowing admitted runs to finish and retaining their receipts for duplicate suppression.
func (m *Manager) Delete(project, origin string, number int64, expected contract.TrafficReplayIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, err := m.findLocked(project, origin, number, expected, true, true)
	if err != nil {
		return err
	}
	release(w)
	if len(w.value.Receipts) == 0 {
		m.bytes -= w.bytes
		delete(m.workspaces, workspaceKey{project, origin, number})
	} else {
		m.recountLocked(w)
	}
	return nil
}

// Clear discards origin payloads while allowing admitted runs to finish without republishing their results.
func (m *Manager) Clear(project, origin string) { m.ClearThrough(project, origin, 1<<63-1) }

// ClearThrough disposes origin workspaces whose baseline lies at or below the reviewed watermark.
func (m *Manager) ClearThrough(project, origin string, through int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, w := range m.workspaces {
		if key.project == project && key.environment == origin && w.value.Baseline != nil && w.value.Baseline.Sequence <= through {
			release(w)
			if len(w.value.Receipts) == 0 {
				m.bytes -= w.bytes
				delete(m.workspaces, key)
			} else {
				m.recountLocked(w)
			}
		}
	}
}

// Close cancels and drains workers within the caller's shutdown deadline and releases all draft secrets.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	for _, w := range m.workspaces {
		release(w)
		m.recountLocked(w)
	}
	m.cancel()
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.workers.Wait(); close(done) }()
	select {
	case <-done:
		<-m.sweeperDone
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
