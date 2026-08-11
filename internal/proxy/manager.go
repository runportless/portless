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

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/store"
)

type edge struct {
	scope    string
	source   string
	target   string
	protocol model.Protocol
	listener net.Listener
	server   *http.Server
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
	store     *store.Store
	broker    *events.Broker
	mu        sync.RWMutex
	targets   map[string]target
	edges     map[string]*edge
	transport *http.Transport
	closed    atomic.Bool
}

func NewManager(controlStore *store.Store, broker *events.Broker) *Manager {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &Manager{store: controlStore, broker: broker, targets: make(map[string]target), edges: make(map[string]*edge), transport: transport}
}

func (m *Manager) SetTarget(scope, service string, port int) {
	m.mu.Lock()
	m.targets[targetKey(scope, service)] = target{provider: model.ProviderLocal, address: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	m.mu.Unlock()
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
	key := edgeKey(scope, connection.Source, connection.Target)
	m.mu.RLock()
	current := m.edges[key]
	m.mu.RUnlock()
	if current != nil {
		return current.listener.Addr().(*net.TCPAddr).Port, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	created := &edge{scope: scope, source: connection.Source, target: connection.Target, protocol: connection.Protocol, listener: listener}
	m.mu.Lock()
	if existing := m.edges[key]; existing != nil {
		m.mu.Unlock()
		_ = listener.Close()
		return existing.listener.Addr().(*net.TCPAddr).Port, nil
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
		go m.serveTCP(ctx, created)
	}
	return listener.Addr().(*net.TCPAddr).Port, nil
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
		if current.server != nil {
			_ = current.server.Shutdown(ctx)
		} else {
			_ = current.listener.Close()
		}
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
	fault := m.matchFault(request.Context(), scope, source, targetName, request.Method, request.URL.Path)
	if fault != nil {
		m.applyDelay(request.Context(), *fault)
		if request.Context().Err() != nil {
			return
		}
		if fault.StatusCode != 0 {
			writer.Header().Set("X-Portless-Fault", fault.Name)
			http.Error(writer, "Portless fault "+fault.Name, fault.StatusCode)
			m.finishHTTP(request.Context(), scope, source, targetName, request, started, fault.StatusCode, 0, fault.Name, "", target{})
			return
		}
		if fault.Abort {
			m.abortHTTP(writer)
			m.finishHTTP(request.Context(), scope, source, targetName, request, started, 0, 0, fault.Name, "connection aborted by fault", target{})
			return
		}
	}
	upstream, ok := m.target(scope, targetName)
	if !ok {
		http.Error(writer, "Portless: "+targetName+" is not available", http.StatusBadGateway)
		m.finishHTTP(request.Context(), scope, source, targetName, request, started, http.StatusBadGateway, 0, faultName(fault), "target is not available", target{})
		return
	}
	if upstream.provider == model.ProviderRemote && upstream.writePolicy == model.WriteReadOnly && !safeMethod(request.Method) {
		writer.Header().Set("X-Portless-Remote-Policy", string(model.WriteReadOnly))
		http.Error(writer, "Portless: remote target is read-only", http.StatusForbidden)
		m.finishHTTP(request.Context(), scope, source, targetName, request, started, http.StatusForbidden, 0, faultName(fault), "remote target is read-only", upstream)
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
		m.finishHTTP(request.Context(), scope, source, targetName, request, started, http.StatusBadGateway, 0, faultName(fault), err.Error(), upstream)
		return
	}
	defer response.Body.Close()
	removeHopHeaders(response.Header)
	copyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	written, copyErr := io.Copy(writer, response.Body)
	errorText := ""
	if copyErr != nil {
		errorText = copyErr.Error()
	}
	m.finishHTTP(request.Context(), scope, source, targetName, request, started, response.StatusCode, written, faultName(fault), errorText, upstream)
}

func (m *Manager) finishHTTP(ctx context.Context, scope, source, targetName string, request *http.Request, started time.Time, status int, responseBytes int64, fault, errorText string, upstream target) {
	completed := time.Now().UTC()
	project, environment := scopeNames(scope)
	event := model.TrafficEvent{
		Project: project, Environment: environment, Protocol: model.ProtocolHTTP, Source: source, Target: targetName,
		TargetProvider: upstream.provider, RemoteClassification: upstream.classification,
		StartedAt: started, CompletedAt: completed, Method: request.Method, Host: request.Host,
		Path: request.URL.EscapedPath(), Status: status, DurationMS: completed.Sub(started).Milliseconds(),
		RequestBytes: request.ContentLength, ResponseBytes: responseBytes, Fault: fault, Error: errorText,
		Headers: redactHeaders(request.Header),
	}
	event = m.broker.AddTraffic(event)
	if recording := m.matchRecording(ctx, scope, source, targetName); recording != "" {
		event.Recording = recording
		_ = m.store.PersistTraffic(context.Background(), event)
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
		bytes int64
		err   error
	}
	results := make(chan countResult, 2)
	go func() { count, err := io.Copy(upstream, downstream); results <- countResult{count, err} }()
	go func() { count, err := io.Copy(downstream, upstream); results <- countResult{count, err} }()
	first := <-results
	_ = downstream.SetDeadline(time.Now())
	_ = upstream.SetDeadline(time.Now())
	second := <-results
	errorText := ""
	if first.err != nil && !isClosedConnection(first.err) {
		errorText = first.err.Error()
	} else if second.err != nil && !isClosedConnection(second.err) {
		errorText = second.err.Error()
	}
	m.finishTCP(current, started, first.bytes, second.bytes, faultName(fault), errorText)
}

func (m *Manager) finishTCP(current *edge, started time.Time, requestBytes, responseBytes int64, fault, errorText string) {
	completed := time.Now().UTC()
	project, environment := scopeNames(current.scope)
	event := model.TrafficEvent{Project: project, Environment: environment, Protocol: current.protocol, Source: current.source, Target: current.target,
		StartedAt: started, CompletedAt: completed, DurationMS: completed.Sub(started).Milliseconds(),
		RequestBytes: requestBytes, ResponseBytes: responseBytes, Fault: fault, Error: errorText}
	event = m.broker.AddTraffic(event)
	if recording := m.matchRecording(context.Background(), current.scope, current.source, current.target); recording != "" {
		event.Recording = recording
		_ = m.store.PersistTraffic(context.Background(), event)
	}
}

func (m *Manager) matchFault(ctx context.Context, project, source, target, method, requestPath string) *model.FaultRule {
	faults, err := m.store.Faults(ctx, project, true)
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
		_ = m.store.IncrementFaultMatch(context.Background(), project, fault.Name)
		return &fault
	}
	return nil
}

func (m *Manager) matchRecording(ctx context.Context, project, source, target string) string {
	recordings, err := m.store.ActiveRecordings(ctx, project)
	if err != nil {
		return ""
	}
	for _, recording := range recordings {
		if recording.Source != "" && recording.Source != source {
			continue
		}
		if recording.Target != "" && recording.Target != target {
			continue
		}
		return recording.Name
	}
	return ""
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

func redactHeaders(headers http.Header) map[string]string {
	result := make(map[string]string)
	for name, values := range headers {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		lower := strings.ToLower(canonical)
		if strings.Contains(lower, "authorization") || strings.Contains(lower, "cookie") || strings.Contains(lower, "token") || strings.Contains(lower, "api-key") || strings.Contains(lower, "secret") {
			result[canonical] = "[REDACTED]"
			continue
		}
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
