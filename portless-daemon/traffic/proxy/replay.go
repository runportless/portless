package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

const replayResponseLimit = 1 << 20

type replayContextKey struct{}

type replayAttempt struct {
	manager                   *Manager
	scope, source, targetName string
	upstream                  target
	provenance                model.TrafficReplay
	ctx                       context.Context
	cancel                    context.CancelFunc
	mu                        sync.Mutex
	invalid                   bool
	connections               []net.Conn
	done                      chan struct{}
	dispatched                atomic.Bool
	responseReceived          bool
	transport                 *http.Transport
	exchange                  model.TrafficExchange
	failure                   error
	secrets                   []string
}

// ReplayTarget verifies the reviewed binding against the registered target and returns its private generation without contacting it.
func (m *Manager) ReplayTarget(scope, service string, expected model.ComponentBinding) (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	upstream, ok := m.targets[targetKey(scope, service)]
	if !ok || m.closed.Load() {
		return 0, errors.New("replay target is unavailable")
	}
	if upstream.provider != expected.Provider {
		return 0, errors.New("replay target provider changed")
	}
	if expected.Provider == model.ProviderRemote {
		if expected.Remote == nil {
			return 0, errors.New("replay remote policy is unavailable")
		}
		reviewed, err := buildRemoteTarget(*expected.Remote)
		if err != nil || upstream.baseURL == nil || upstream.baseURL.String() != reviewed.baseURL.String() || upstream.classification != reviewed.classification || upstream.writePolicy != reviewed.writePolicy || upstream.healthPath != reviewed.healthPath {
			return 0, errors.New("replay remote destination changed")
		}
	}
	return upstream.generation, nil
}

// ReplayHTTP sends one bounded HTTP request through the selected logical edge and returns its retained exchange and dispatch outcome.
func (m *Manager) ReplayHTTP(ctx context.Context, scope, source, targetName, method, requestTarget string, headers map[string][]string, body string, generation uint64, provenance model.TrafficReplay) (model.TrafficExchange, string, error) {
	request, err := replayRequest(ctx, scope, targetName, method, requestTarget, headers, body)
	if err != nil {
		return model.TrafficExchange{}, "not-sent", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	attempt := &replayAttempt{manager: m, scope: scope, source: source, targetName: targetName, provenance: provenance, ctx: ctx, cancel: cancel, done: make(chan struct{}), secrets: replaySecrets(request.Header)}
	m.mu.Lock()
	upstream, ok := m.targets[targetKey(scope, targetName)]
	if m.closed.Load() || !ok || generation == 0 || generation != upstream.generation || ctx.Err() != nil {
		m.mu.Unlock()
		return model.TrafficExchange{}, "not-sent", errors.New("replay destination changed or is unavailable")
	}
	if len(m.replayAttempts) >= 4 {
		m.mu.Unlock()
		return model.TrafficExchange{}, "not-sent", errors.New("replay execution capacity reached")
	}
	if m.replayAttempts == nil {
		m.replayAttempts = make(map[*replayAttempt]struct{})
	}
	attempt.upstream = upstream
	m.replayAttempts[attempt] = struct{}{}
	m.mu.Unlock()
	stop := context.AfterFunc(ctx, attempt.closeConnections)
	defer func() {
		stop()
		attempt.closeConnections()
		if attempt.transport != nil {
			attempt.transport.CloseIdleConnections()
		}
		m.mu.Lock()
		delete(m.replayAttempts, attempt)
		close(attempt.done)
		m.mu.Unlock()
	}()
	attempt.transport = attempt.newTransport()
	request = request.WithContext(context.WithValue(ctx, replayContextKey{}, attempt))
	m.forwardHTTP(&replaySink{header: make(http.Header)}, request, scope, source, targetName)
	outcome := "not-sent"
	if attempt.responseReceived {
		outcome = "response-received"
	} else if attempt.dispatched.Load() {
		outcome = "unknown"
	}
	return attempt.exchange, outcome, attempt.failure
}

func replayRequest(ctx context.Context, scope, target, method, requestTarget string, headers map[string][]string, body string) (*http.Request, error) {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		return nil, errors.New("unsupported replay method")
	}
	if len(body) > contract.TrafficReplayMaxBodyBytes {
		return nil, errors.New("replay request body exceeds the input limit")
	}
	if !utf8.ValidString(body) {
		return nil, errors.New("replay body must contain valid UTF-8 text")
	}
	if len(requestTarget) > 8192 || !utf8.ValidString(requestTarget) || !strings.HasPrefix(requestTarget, "/") || strings.HasPrefix(requestTarget, "//") || strings.ContainsAny(requestTarget, "#\\") {
		return nil, errors.New("invalid replay request target")
	}
	for _, char := range requestTarget {
		if char <= ' ' || char == 127 {
			return nil, errors.New("invalid replay request target")
		}
	}
	parsed, err := url.ParseRequestURI(requestTarget)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return nil, errors.New("invalid replay request target")
	}
	if _, err := url.QueryUnescape(parsed.RawQuery); err != nil {
		return nil, errors.New("invalid replay query escaping")
	}
	project, environment, err := model.ParseEnvironmentSelector(scope)
	if err != nil {
		return nil, errors.New("invalid replay environment")
	}
	host := target + "." + environment + "." + project + ".localhost"
	parsed.Scheme, parsed.Host = "http", host
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), strings.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid replay request")
	}
	request.GetBody = nil
	count, size := 0, 0
	for name, values := range headers {
		if !validReplayHeaderName(name) {
			return nil, errors.New("invalid replay header name")
		}
		count += max(1, len(values))
		size += len(name)
		for _, value := range values {
			size += len(value) + 4
			if !utf8.ValidString(value) || strings.Contains(strings.ToUpper(value), "[REDACTED]") {
				return nil, errors.New("invalid replay header value")
			}
			for _, char := range value {
				if char < 32 && char != '\t' || char == 127 {
					return nil, errors.New("invalid replay header value")
				}
			}
			request.Header.Add(name, value)
		}
	}
	if count > 128 || size > 32<<10 {
		return nil, errors.New("replay headers exceed the limit")
	}
	removeHopHeaders(request.Header)
	for name := range request.Header {
		lower := strings.ToLower(name)
		if lower == "host" || lower == "content-length" || lower == "expect" || lower == "forwarded" || lower == "x-real-ip" || lower == "x-csrf-token" || lower == "x-xsrf-token" || lower == "traceparent" || lower == "tracestate" || lower == "baggage" || lower == "b3" || strings.HasPrefix(lower, "sec-") || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-b3-") || strings.HasPrefix(lower, "x-datadog-") || strings.HasPrefix(lower, "portless-") || strings.HasPrefix(lower, "x-portless-") {
			request.Header.Del(name)
		}
	}
	if encoding := request.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(strings.TrimSpace(encoding), "identity") {
		return nil, errors.New("replay request content encoding must be identity")
	}
	if body != "" && !inspectableBody(request.Header.Get("Content-Type")) {
		return nil, errors.New("replay request body must use a supported text content type")
	}
	request.Header.Set("Accept-Encoding", "identity")
	return request, nil
}

func validReplayHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c > 127 || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

func replayFrom(ctx context.Context) *replayAttempt {
	attempt, _ := ctx.Value(replayContextKey{}).(*replayAttempt)
	return attempt
}

func (r *replayAttempt) check() error {
	r.manager.mu.RLock()
	current, ok := r.manager.targets[targetKey(r.scope, r.targetName)]
	valid := ok && current.generation == r.upstream.generation && !r.manager.closed.Load()
	r.manager.mu.RUnlock()
	if !valid || r.ctx.Err() != nil {
		return errors.New("replay destination changed or request was canceled")
	}
	return nil
}

func (r *replayAttempt) attach(connection net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.invalid || r.ctx.Err() != nil {
		_ = connection.Close()
		return false
	}
	r.connections = append(r.connections, connection)
	return true
}

func (r *replayAttempt) closeConnections() {
	r.mu.Lock()
	r.invalid = true
	connections := r.connections
	r.connections = nil
	r.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func (r *replayAttempt) newTransport() *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	return &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 64 << 10, Protocols: protocols,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return r.dial(ctx, network, address, false)
		},
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return r.dial(ctx, network, address, true)
		},
	}
}

func (r *replayAttempt) dial(ctx context.Context, network, address string, encrypted bool) (net.Conn, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, errors.New("replay upstream connection failed")
	}
	if !r.attach(connection) {
		return nil, errors.New("replay destination changed during connection")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if encrypted {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			_ = connection.Close()
			return nil, errors.New("invalid replay TLS destination")
		}
		tlsConnection := tls.Client(connection, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, NextProtos: []string{"http/1.1"}})
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			_ = connection.Close()
			return nil, errors.New("replay TLS verification or handshake failed")
		}
		connection = tlsConnection
	}
	if err := r.check(); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &replayConnection{Conn: connection, attempt: r}, nil
}

type replayConnection struct {
	net.Conn
	attempt *replayAttempt
}

// Write records the start of application HTTP transmission before writing bytes.
func (c *replayConnection) Write(body []byte) (int, error) {
	if err := c.attempt.check(); err != nil {
		return 0, err
	}
	c.attempt.dispatched.Store(true)
	return c.Conn.Write(body)
}

type replaySink struct{ header http.Header }

// Header returns the private sink's response headers.
func (s *replaySink) Header() http.Header { return s.header }

// WriteHeader accepts the status without forwarding an application response into the control API.
func (s *replaySink) WriteHeader(int) {}

// Write discards response bytes after the shared bounded capture observes them.
func (s *replaySink) Write(body []byte) (int, error) { return len(body), nil }

func (m *Manager) invalidateReplaysLocked(scope, service string) []*replayAttempt {
	var closing []*replayAttempt
	for attempt := range m.replayAttempts {
		if (scope == "" || attempt.scope == scope) && (service == "" || attempt.source == service || attempt.targetName == service) {
			attempt.mu.Lock()
			attempt.invalid = true
			attempt.mu.Unlock()
			attempt.cancel()
			closing = append(closing, attempt)
		}
	}
	return closing
}

func closeReplays(attempts []*replayAttempt) {
	for _, attempt := range attempts {
		attempt.closeConnections()
	}
}

func waitReplays(ctx context.Context, attempts []*replayAttempt) {
	for _, attempt := range attempts {
		select {
		case <-attempt.done:
		case <-ctx.Done():
			return
		}
	}
}

func replaySecrets(headers http.Header) []string {
	var secrets []string
	for name, values := range headers {
		if !sensitiveTrafficHeader(name) {
			continue
		}
		for _, value := range values {
			if value == "" || value == "[REDACTED]" {
				continue
			}
			secrets = append(secrets, value)
			if strings.EqualFold(name, "Authorization") {
				if _, token, ok := strings.Cut(value, " "); ok && token != "" {
					secrets = append(secrets, token)
				}
			}
			if strings.EqualFold(name, "Cookie") {
				for _, part := range strings.Split(value, ";") {
					if _, token, ok := strings.Cut(strings.TrimSpace(part), "="); ok && token != "" {
						secrets = append(secrets, token)
					}
				}
			}
		}
	}
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	return slices.Compact(secrets)
}

func (r *replayAttempt) redact(value string) string {
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}

func (r *replayAttempt) redactExchange(exchange *model.TrafficExchange) {
	exchange.Replay = &r.provenance
	for _, headers := range []map[string][]string{exchange.RequestHeaders, exchange.ResponseHeaders} {
		for name, values := range headers {
			for index, value := range values {
				headers[name][index] = r.redact(value)
			}
		}
	}
	for _, entry := range []struct {
		body    *string
		capture *model.HTTPCapture
	}{{&exchange.RequestBody, exchange.RequestCapture}, {&exchange.ResponseBody, exchange.ResponseCapture}} {
		redacted := r.redact(*entry.body)
		if redacted != *entry.body && entry.capture != nil {
			entry.capture.Exact = false
			if entry.capture.State == "complete" {
				entry.capture.State = "omitted"
			}
		}
		*entry.body = redacted
	}
	exchange.Path, exchange.RequestTarget, exchange.Error = r.redact(exchange.Path), r.redact(exchange.RequestTarget), r.redact(exchange.Error)
}

var errReplayResponseLimit = errors.New("replay response exceeded the consumption limit")

func copyHTTPResponse(destination io.Writer, source io.Reader, replay *replayAttempt) (int64, error) {
	if replay == nil {
		return io.Copy(destination, source)
	}
	read, err := io.Copy(destination, io.LimitReader(source, replayResponseLimit))
	if err != nil {
		return read, err
	}
	if read == replayResponseLimit {
		return read, errReplayResponseLimit
	}
	return read, nil
}
