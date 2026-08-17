package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/portless-run/portless/portless-daemon/database"
	"github.com/portless-run/portless/portless-daemon/events"
	"github.com/portless-run/portless/portless-daemon/model"
)

type edge struct {
	scope             string
	source            string
	target            string
	protocol          model.Protocol
	listener          net.Listener
	server            *http.Server
	cancel            context.CancelFunc
	activeConnections atomic.Int64
}

type target struct {
	provider       model.ProviderKind
	address        string
	baseURL        *url.URL
	classification model.RemoteClassification
	writePolicy    model.WritePolicy
	healthPath     string
}

type Manager struct {
	database  *database.Store
	broker    *events.Broker
	mu        sync.RWMutex
	targets   map[string]target
	edges     map[string]*edge
	transport *http.Transport
	closed    atomic.Bool
}

const trafficBodyLimit = 64 << 10

type bodyCapture struct {
	body  []byte
	total int64
}

func (c *bodyCapture) Write(content []byte) (int, error) {
	written := len(content)
	c.total += int64(written)
	if remaining := trafficBodyLimit - len(c.body); remaining > 0 {
		if len(content) > remaining {
			content = content[:remaining]
		}
		c.body = append(c.body, content...)
	}
	return written, nil
}

func (c *bodyCapture) text() string {
	if c == nil || len(c.body) == 0 {
		return ""
	}
	return strings.ToValidUTF8(string(c.body), "�")
}

func (c *bodyCapture) truncated() bool {
	return c != nil && c.total > int64(len(c.body))
}

type capturingReadCloser struct {
	io.ReadCloser
	capture *bodyCapture
}

func (r *capturingReadCloser) Read(content []byte) (int, error) {
	read, err := r.ReadCloser.Read(content)
	if read > 0 {
		_, _ = r.capture.Write(content[:read])
	}
	return read, err
}

func NewManager(controlStore *database.Store, broker *events.Broker) *Manager {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &Manager{database: controlStore, broker: broker, targets: make(map[string]target), edges: make(map[string]*edge), transport: transport}
}

func (m *Manager) SetTarget(scope, service string, port int) {
	m.SetTargetProvider(scope, service, port, model.ProviderLocal)
}

func (m *Manager) SetTargetProvider(scope, service string, port int, provider model.ProviderKind) {
	m.mu.Lock()
	m.targets[targetKey(scope, service)] = target{provider: provider, address: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	m.mu.Unlock()
}

func (m *Manager) ConnectionRuntime(scope, source, targetName string) (proxyAddress string, provider model.ProviderKind, targetEndpoint string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if current := m.edges[edgeKey(scope, source, targetName)]; current != nil && current.listener != nil {
		proxyAddress = current.listener.Addr().String()
	}
	if upstream, ok := m.targets[targetKey(scope, targetName)]; ok {
		provider = upstream.provider
		if upstream.baseURL != nil {
			targetEndpoint = upstream.baseURL.String()
		} else {
			targetEndpoint = upstream.address
		}
	}
	return proxyAddress, provider, targetEndpoint
}

func (m *Manager) SetRemoteTarget(scope, service string, remote model.RemoteTarget) error {
	parsed, err := url.Parse(remote.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return errors.New("remote target must be an http or https URL without credentials, query, or fragment")
	}
	if remote.WritePolicy != model.WriteReadOnly && remote.WritePolicy != model.WriteReadWrite {
		return errors.New("remote target write policy must be read-only or read-write")
	}
	m.mu.Lock()
	m.targets[targetKey(scope, service)] = target{provider: model.ProviderRemote, baseURL: parsed, classification: remote.Classification, writePolicy: remote.WritePolicy, healthPath: remote.HealthPath}
	m.mu.Unlock()
	return nil
}

func (m *Manager) RemoveTarget(scope, service string) {
	m.mu.Lock()
	delete(m.targets, targetKey(scope, service))
	m.mu.Unlock()
}

func (m *Manager) EnsureEdge(ctx context.Context, scope string, connection model.Connection) (int, error) {
	address, err := m.ensureEdge(ctx, scope, connection, "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	return address.Port, nil
}

func (m *Manager) EnsureEdgeAtPort(ctx context.Context, scope string, connection model.Connection, port int) (int, error) {
	if port < 1 || port > 65535 {
		return 0, errors.New("persisted proxy port is invalid")
	}
	address, err := m.ensureEdge(ctx, scope, connection, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return 0, err
	}
	return address.Port, nil
}

// EnsureEdgeAtAddress binds a directed edge to an exact loopback address. A
// distinct address lets multiple resource edges use the same conventional
// client port while preserving source identity for traffic controls.
func (m *Manager) EnsureEdgeAtAddress(ctx context.Context, scope string, connection model.Connection, requestedAddress string) (string, error) {
	parsed, err := net.ResolveTCPAddr("tcp", requestedAddress)
	if err != nil || parsed.IP == nil || !parsed.IP.IsLoopback() || parsed.Port < 1 || parsed.Port > 65535 {
		return "", errors.New("proxy edge address must be an explicit loopback IP and valid port")
	}
	address, err := m.ensureEdge(ctx, scope, connection, parsed.String())
	if err != nil {
		return "", err
	}
	return address.String(), nil
}

func (m *Manager) ensureEdge(ctx context.Context, scope string, connection model.Connection, requestedAddress string) (*net.TCPAddr, error) {
	key := edgeKey(scope, connection.Source, connection.Target)
	m.mu.RLock()
	current := m.edges[key]
	m.mu.RUnlock()
	if current != nil {
		currentAddress := current.listener.Addr().(*net.TCPAddr)
		requested, err := net.ResolveTCPAddr("tcp", requestedAddress)
		if err != nil {
			return nil, err
		}
		if requested.Port != 0 && currentAddress.String() != requested.String() {
			return nil, fmt.Errorf("proxy edge is already bound to %s, expected %s", currentAddress, requested)
		}
		return currentAddress, nil
	}
	listener, err := net.Listen("tcp", requestedAddress)
	if err != nil {
		requested, _ := net.ResolveTCPAddr("tcp", requestedAddress)
		if requested != nil && requested.Port != 0 {
			return nil, fmt.Errorf("bind proxy edge on %s: %w", requestedAddress, err)
		}
		return nil, err
	}
	edgeContext, cancel := context.WithCancel(context.Background())
	created := &edge{scope: scope, source: connection.Source, target: connection.Target, protocol: connection.Protocol, listener: listener, cancel: cancel}
	m.mu.Lock()
	if existing := m.edges[key]; existing != nil {
		m.mu.Unlock()
		cancel()
		_ = listener.Close()
		return existing.listener.Addr().(*net.TCPAddr), nil
	}
	m.edges[key] = created
	m.mu.Unlock()
	if connection.Protocol == model.ProtocolHTTP {
		created.server = &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				m.forwardHTTP(w, r, scope, connection.Source, connection.Target)
			}),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       90 * time.Second,
		}
		go func() { _ = created.server.Serve(listener) }()
	} else {
		go m.serveTCP(edgeContext, created)
	}
	return listener.Addr().(*net.TCPAddr), nil
}

func (m *Manager) HasTarget(scope, service string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.targets[targetKey(scope, service)]
	return ok
}

func (m *Manager) HasEdge(scope, source, targetName string, port int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	current := m.edges[edgeKey(scope, source, targetName)]
	return current != nil && current.listener != nil && (port == 0 || current.listener.Addr().(*net.TCPAddr).Port == port)
}

func (m *Manager) HasEdgeAtAddress(scope, source, targetName, address string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	current := m.edges[edgeKey(scope, source, targetName)]
	return current != nil && current.listener != nil && current.listener.Addr().String() == address
}

func (m *Manager) ServeIngress(w http.ResponseWriter, request *http.Request, scope, service string) {
	m.forwardHTTP(w, request, scope, "external", service)
}

func (m *Manager) CloseEnvironment(ctx context.Context, scope string) {
	m.mu.Lock()
	var closing []*edge
	for key, current := range m.edges {
		if current.scope == scope {
			closing = append(closing, current)
			delete(m.edges, key)
		}
	}
	for key := range m.targets {
		if strings.HasPrefix(key, scope+"\x00") {
			delete(m.targets, key)
		}
	}
	m.mu.Unlock()
	for _, current := range closing {
		if current.cancel != nil {
			current.cancel()
		}
		if current.server != nil {
			_ = current.server.Shutdown(ctx)
		}
		// Close the listener explicitly as well. Shutdown only closes listeners
		// that Serve has already registered, so relying on it races with the
		// goroutine that starts a newly-created edge.
		_ = current.listener.Close()
	}
}

func (m *Manager) Close(ctx context.Context) {
	if !m.closed.CompareAndSwap(false, true) {
		return
	}
	m.mu.RLock()
	projects := make(map[string]struct{})
	for _, current := range m.edges {
		projects[current.scope] = struct{}{}
	}
	m.mu.RUnlock()
	for project := range projects {
		m.CloseEnvironment(ctx, project)
	}
	m.transport.CloseIdleConnections()
}

func (m *Manager) forwardHTTP(writer http.ResponseWriter, request *http.Request, scope, source, targetName string) {
	started := time.Now().UTC()
	requestCapture := captureRequestBody(request)
	fault := m.matchFault(request.Context(), scope, source, targetName, request.Method, request.URL.Path)
	if fault != nil {
		m.applyDelay(request.Context(), *fault)
		if request.Context().Err() != nil {
			return
		}
		if fault.StatusCode != 0 {
			writer.Header().Set("X-Portless-Fault", fault.Name)
			http.Error(writer, "Portless fault "+fault.Name, fault.StatusCode)
			m.finishHTTP(request.Context(), scope, source, targetName, request, started, fault.StatusCode, 0, fault.Name, "", target{}, writer.Header(), requestCapture, nil)
			return
		}
		if fault.Abort {
			m.abortHTTP(writer)
			m.finishHTTP(request.Context(), scope, source, targetName, request, started, 0, 0, fault.Name, "connection aborted by fault", target{}, nil, requestCapture, nil)
			return
		}
	}
	upstream, ok := m.target(scope, targetName)
	if !ok {
		http.Error(writer, "Portless: "+targetName+" is not available", http.StatusBadGateway)
		m.finishHTTP(request.Context(), scope, source, targetName, request, started, http.StatusBadGateway, 0, faultName(fault), "target is not available", target{}, writer.Header(), requestCapture, nil)
		return
	}
	if upstream.provider == model.ProviderRemote && upstream.writePolicy == model.WriteReadOnly && !safeMethod(request.Method) {
		writer.Header().Set("X-Portless-Remote-Policy", string(model.WriteReadOnly))
		http.Error(writer, "Portless: remote target is read-only", http.StatusForbidden)
		m.finishHTTP(request.Context(), scope, source, targetName, request, started, http.StatusForbidden, 0, faultName(fault), "remote target is read-only", upstream, writer.Header(), requestCapture, nil)
		return
	}
	outgoing := request.Clone(request.Context())
	outgoing.RequestURI = ""
	if upstream.provider == model.ProviderRemote {
		outgoing.URL.Scheme = upstream.baseURL.Scheme
		outgoing.URL.Host = upstream.baseURL.Host
		outgoing.URL.Path = joinURLPath(upstream.baseURL.Path, request.URL.Path)
		outgoing.URL.RawPath = ""
		outgoing.Host = upstream.baseURL.Host
	} else {
		outgoing.URL.Scheme = "http"
		outgoing.URL.Host = upstream.address
	}
	removeHopHeaders(outgoing.Header)
	response, err := m.transport.RoundTrip(outgoing)
	if err != nil {
		http.Error(writer, "Portless upstream error: "+err.Error(), http.StatusBadGateway)
		m.finishHTTP(request.Context(), scope, source, targetName, request, started, http.StatusBadGateway, 0, faultName(fault), err.Error(), upstream, writer.Header(), requestCapture, nil)
		return
	}
	defer response.Body.Close()
	removeHopHeaders(response.Header)
	copyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	responseCapture := captureResponseBody(response.Header)
	destination := io.Writer(writer)
	if responseCapture != nil {
		destination = io.MultiWriter(writer, responseCapture)
	}
	written, copyErr := io.Copy(destination, response.Body)
	errorText := ""
	if copyErr != nil {
		errorText = copyErr.Error()
	}
	m.finishHTTP(request.Context(), scope, source, targetName, request, started, response.StatusCode, written, faultName(fault), errorText, upstream, response.Header, requestCapture, responseCapture)
}

func (m *Manager) finishHTTP(ctx context.Context, scope, source, targetName string, request *http.Request, started time.Time, status int, responseBytes int64, fault, errorText string, upstream target, responseHeaders http.Header, requestCapture, responseCapture *bodyCapture) {
	completed := time.Now().UTC()
	project, environment := scopeNames(scope)
	requestBytes := request.ContentLength
	if requestCapture != nil && requestCapture.total > requestBytes {
		requestBytes = requestCapture.total
	}
	if requestBytes < 0 {
		requestBytes = 0
	}
	event := model.TrafficEvent{
		Project: project, Environment: environment, Protocol: model.ProtocolHTTP, Source: source, Target: targetName,
		TargetProvider: upstream.provider, RemoteClassification: upstream.classification,
		StartedAt: started, CompletedAt: completed, Method: request.Method, Host: request.Host,
		Path: request.URL.EscapedPath(), Status: status, DurationMS: completed.Sub(started).Milliseconds(),
		RequestBytes: requestBytes, ResponseBytes: responseBytes, Fault: fault, Error: errorText,
		RequestHeaders: captureHeaders(request.Header), ResponseHeaders: captureHeaders(responseHeaders),
		RequestBody: requestCapture.text(), ResponseBody: responseCapture.text(),
		RequestBodyTruncated: requestCapture.truncated(), ResponseBodyTruncated: responseCapture.truncated(),
	}
	var persistBodies bool
	event.Recording, persistBodies = m.matchRecording(ctx, scope, source, targetName)
	event = m.broker.AddTraffic(event)
	if event.Recording != "" {
		persisted := event
		if !persistBodies {
			persisted.RequestBody = ""
			persisted.ResponseBody = ""
			persisted.RequestBodyTruncated = false
			persisted.ResponseBodyTruncated = false
		}
		_ = m.database.PersistTraffic(context.Background(), persisted)
	}
}

func (m *Manager) serveTCP(ctx context.Context, current *edge) {
	for {
		connection, err := current.listener.Accept()
		if err != nil {
			return
		}
		go m.forwardTCP(ctx, current, connection)
	}
}

func (m *Manager) forwardTCP(ctx context.Context, current *edge, downstream net.Conn) {
	started := time.Now().UTC()
	defer downstream.Close()
	fault := m.matchFault(ctx, current.scope, current.source, current.target, "", "")
	activeConnections := current.activeConnections.Add(1)
	m.publishTCPActivity(current, "open", activeConnections, 0, 0, faultName(fault))
	var requestBytes atomic.Int64
	var responseBytes atomic.Int64
	activityDone := make(chan struct{})
	activityStopped := make(chan struct{})
	go m.reportTCPActivity(ctx, current, &requestBytes, &responseBytes, faultName(fault), activityDone, activityStopped)
	defer func() {
		close(activityDone)
		<-activityStopped
		activeConnections = current.activeConnections.Add(-1)
		m.publishTCPActivity(current, "close", activeConnections, 0, 0, faultName(fault))
	}()
	if fault != nil {
		m.applyDelay(ctx, *fault)
		if fault.Abort || fault.StatusCode != 0 {
			m.finishTCP(current, started, 0, 0, fault.Name, "connection rejected by fault")
			return
		}
	}
	upstreamTarget, ok := m.target(current.scope, current.target)
	if !ok || upstreamTarget.provider == model.ProviderRemote {
		m.finishTCP(current, started, 0, 0, faultName(fault), "target is not running")
		return
	}
	upstream, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", upstreamTarget.address)
	if err != nil {
		m.finishTCP(current, started, 0, 0, faultName(fault), err.Error())
		return
	}
	defer upstream.Close()
	type countResult struct {
		err error
	}
	results := make(chan countResult, 2)
	go func() {
		_, err := io.Copy(upstream, io.TeeReader(downstream, trafficCounter{value: &requestBytes}))
		results <- countResult{err: err}
	}()
	go func() {
		_, err := io.Copy(downstream, io.TeeReader(upstream, trafficCounter{value: &responseBytes}))
		results <- countResult{err: err}
	}()
	first := <-results
	forcedShutdown := false
	var second countResult
	select {
	case second = <-results:
	default:
		forcedShutdown = true
		_ = downstream.SetDeadline(time.Now())
		_ = upstream.SetDeadline(time.Now())
		second = <-results
	}
	errorText := ""
	if first.err != nil && !isClosedConnection(first.err) {
		errorText = first.err.Error()
	} else if !forcedShutdown && second.err != nil && !isClosedConnection(second.err) {
		errorText = second.err.Error()
	}
	m.finishTCP(current, started, requestBytes.Load(), responseBytes.Load(), faultName(fault), errorText)
}

type trafficCounter struct{ value *atomic.Int64 }

func (counter trafficCounter) Write(value []byte) (int, error) {
	counter.value.Add(int64(len(value)))
	return len(value), nil
}

func (m *Manager) reportTCPActivity(ctx context.Context, current *edge, requestBytes, responseBytes *atomic.Int64, fault string, done <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	lastRequestBytes := int64(0)
	lastResponseBytes := int64(0)
	lastPublished := time.Now()
	publish := func(heartbeat bool) {
		requestTotal := requestBytes.Load()
		responseTotal := responseBytes.Load()
		requestDelta := requestTotal - lastRequestBytes
		responseDelta := responseTotal - lastResponseBytes
		if requestDelta == 0 && responseDelta == 0 && !heartbeat {
			return
		}
		phase := "data"
		if requestDelta == 0 && responseDelta == 0 {
			phase = "heartbeat"
		}
		m.publishTCPActivity(current, phase, current.activeConnections.Load(), requestDelta, responseDelta, fault)
		lastRequestBytes = requestTotal
		lastResponseBytes = responseTotal
		lastPublished = time.Now()
	}
	for {
		select {
		case <-done:
			publish(false)
			return
		case <-ctx.Done():
			publish(false)
			return
		case <-ticker.C:
			publish(time.Since(lastPublished) >= 5*time.Second)
		}
	}
}

func (m *Manager) publishTCPActivity(current *edge, phase string, activeConnections, requestBytes, responseBytes int64, fault string) {
	project, environment := scopeNames(current.scope)
	m.broker.Publish(events.Event{
		Type: "traffic.tcp.activity", Project: project, Environment: environment,
		Data: model.TrafficActivity{
			Project: project, Environment: environment, Protocol: current.protocol,
			Source: current.source, Target: current.target, ObservedAt: time.Now().UTC(), Phase: phase,
			ActiveConnections: activeConnections, RequestBytes: requestBytes, ResponseBytes: responseBytes, Fault: fault,
		},
	})
}

func (m *Manager) finishTCP(current *edge, started time.Time, requestBytes, responseBytes int64, fault, errorText string) {
	completed := time.Now().UTC()
	project, environment := scopeNames(current.scope)
	event := model.TrafficEvent{Project: project, Environment: environment, Protocol: current.protocol, Source: current.source, Target: current.target,
		StartedAt: started, CompletedAt: completed, DurationMS: completed.Sub(started).Milliseconds(),
		RequestBytes: requestBytes, ResponseBytes: responseBytes, Fault: fault, Error: errorText}
	if upstream, ok := m.target(current.scope, current.target); ok {
		event.TargetProvider = upstream.provider
		event.RemoteClassification = upstream.classification
	}
	event.Recording, _ = m.matchRecording(context.Background(), current.scope, current.source, current.target)
	event = m.broker.AddTraffic(event)
	if event.Recording != "" {
		_ = m.database.PersistTraffic(context.Background(), event)
	}
}

func (m *Manager) matchFault(ctx context.Context, project, source, target, method, requestPath string) *model.FaultRule {
	faults, err := m.database.Faults(ctx, project, true)
	if err != nil {
		return nil
	}
	for _, fault := range faults {
		if fault.Source != source || fault.Target != target {
			continue
		}
		if fault.Method != "" && !strings.EqualFold(fault.Method, method) {
			continue
		}
		if fault.Path != "" {
			matched, err := path.Match(fault.Path, requestPath)
			if err != nil || !matched {
				continue
			}
		}
		if fault.Probability < 1 && rand.Float64() >= fault.Probability {
			continue
		}
		_ = m.database.IncrementFaultMatch(context.Background(), project, fault.Name)
		return &fault
	}
	return nil
}

func (m *Manager) matchRecording(ctx context.Context, project, source, target string) (string, bool) {
	recordings, err := m.database.ActiveRecordings(ctx, project)
	if err != nil {
		return "", false
	}
	for _, recording := range recordings {
		if recording.Source != "" && recording.Source != source {
			continue
		}
		if recording.Target != "" && recording.Target != target {
			continue
		}
		return recording.Name, recording.CaptureBodies
	}
	return "", false
}

func captureRequestBody(request *http.Request) *bodyCapture {
	if request.Body == nil || !inspectableBody(request.Header.Get("Content-Type")) {
		return nil
	}
	capture := &bodyCapture{}
	request.Body = &capturingReadCloser{ReadCloser: request.Body, capture: capture}
	return capture
}

func captureResponseBody(headers http.Header) *bodyCapture {
	if !inspectableBody(headers.Get("Content-Type")) {
		return nil
	}
	return &bodyCapture{}
}

func inspectableBody(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if mediaType == "" || strings.HasPrefix(mediaType, "text/") {
		return true
	}
	return strings.Contains(mediaType, "json") || strings.Contains(mediaType, "xml") ||
		mediaType == "application/x-www-form-urlencoded" || mediaType == "application/graphql" ||
		mediaType == "application/javascript"
}

func (m *Manager) applyDelay(ctx context.Context, fault model.FaultRule) {
	delay := fault.LatencyMS
	if fault.JitterMS > 0 {
		delay += rand.Int64N(fault.JitterMS + 1)
	}
	if delay <= 0 {
		return
	}
	timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (m *Manager) abortHTTP(writer http.ResponseWriter) {
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		http.Error(writer, "Portless fault aborted the request", http.StatusServiceUnavailable)
		return
	}
	connection, _, err := hijacker.Hijack()
	if err == nil {
		_ = connection.Close()
	}
}

func (m *Manager) target(scope, service string) (target, bool) {
	m.mu.RLock()
	target, ok := m.targets[targetKey(scope, service)]
	m.mu.RUnlock()
	return target, ok
}

func (m *Manager) CheckRemote(ctx context.Context, scope, service string) error {
	configured, ok := m.target(scope, service)
	if !ok || configured.provider != model.ProviderRemote || configured.baseURL == nil {
		return errors.New("remote target is not configured")
	}
	checkURL := *configured.baseURL
	if configured.healthPath != "" {
		checkURL.Path = joinURLPath(checkURL.Path, configured.healthPath)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL.String(), nil)
	if err != nil {
		return err
	}
	response, err := m.transport.RoundTrip(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.CopyN(io.Discard, response.Body, 4096)
	if response.StatusCode >= 500 {
		return fmt.Errorf("remote health check returned %s", response.Status)
	}
	return nil
}

func copyHeaders(destination, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func removeHopHeaders(headers http.Header) {
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		headers.Del(name)
	}
}

func captureHeaders(headers http.Header) map[string]string {
	result := make(map[string]string)
	for name, values := range headers {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		result[canonical] = strings.Join(values, ", ")
	}
	return result
}

func targetKey(project, service string) string { return project + "\x00" + service }

func edgeKey(project, source, target string) string {
	return project + "\x00" + source + "\x00" + target
}

func faultName(fault *model.FaultRule) string {
	if fault == nil {
		return ""
	}
	return fault.Name
}

func isClosedConnection(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(strings.ToLower(err.Error()), "closed network connection")
}

func safeMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func joinURLPath(basePath, requestPath string) string {
	if basePath == "" || basePath == "/" {
		if requestPath == "" {
			return "/"
		}
		return requestPath
	}
	return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(requestPath, "/")
}

func scopeNames(scope string) (string, string) {
	project, environment, err := model.ParseEnvironmentSelector(scope)
	if err != nil {
		return scope, "local"
	}
	return project, environment
}
