package mocks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
)

const (
	// ProfileHeader identifies the mock profile selected by the private runtime.
	ProfileHeader = "X-Portless-Mock-Profile"
	// RouteHeader identifies the mock route selected by the private runtime.
	RouteHeader = "X-Portless-Mock-Route"
)

type runtime struct {
	listener net.Listener
	server   *http.Server
	profile  atomic.Pointer[CompiledProfile]
}

// Manager owns private loopback HTTP listeners for active mock providers.
type Manager struct {
	mu       sync.RWMutex
	runtimes map[string]*runtime
}

// NewManager constructs an empty mock runtime manager.
func NewManager() *Manager {
	return &Manager{runtimes: map[string]*runtime{}}
}

// Set compiles a profile, starts its private listener if needed, and returns its port.
func (m *Manager) Set(scope, service string, profile model.MockProfile) (int, error) {
	compiled, err := Compile(profile)
	if err != nil {
		return 0, err
	}
	key := runtimeKey(scope, service)
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.runtimes[key]; current != nil {
		current.profile.Store(compiled)
		return listenerPort(current.listener), nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("listen for mock %s: %w", profile.Name, err)
	}
	created := &runtime{listener: listener}
	created.profile.Store(compiled)
	created.server = &http.Server{Handler: http.HandlerFunc(created.serveHTTP), ReadHeaderTimeout: 5 * time.Second}
	m.runtimes[key] = created
	go func() {
		if serveErr := created.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			// The control plane detects an unavailable listener through its normal
			// proxy and recovery checks. Runtime goroutines intentionally do not
			// mutate durable state.
		}
	}()
	return listenerPort(listener), nil
}

// Address returns the private loopback address for an active mock service.
func (m *Manager) Address(scope, service string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	runtime := m.runtimes[runtimeKey(scope, service)]
	if runtime == nil {
		return "", false
	}
	return runtime.listener.Addr().String(), true
}

// Remove stops one active mock service.
func (m *Manager) Remove(ctx context.Context, scope, service string) error {
	key := runtimeKey(scope, service)
	m.mu.Lock()
	runtime := m.runtimes[key]
	delete(m.runtimes, key)
	m.mu.Unlock()
	if runtime == nil {
		return nil
	}
	return runtime.server.Shutdown(ctx)
}

// RemoveScope stops every mock listener belonging to an environment.
func (m *Manager) RemoveScope(ctx context.Context, scope string) error {
	prefix := strings.ToLower(scope) + "\x00"
	m.mu.Lock()
	var selected []*runtime
	for key, runtime := range m.runtimes {
		if strings.HasPrefix(key, prefix) {
			selected = append(selected, runtime)
			delete(m.runtimes, key)
		}
	}
	m.mu.Unlock()
	var result error
	for _, runtime := range selected {
		result = errors.Join(result, runtime.server.Shutdown(ctx))
	}
	return result
}

// Close stops every managed mock listener.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	selected := make([]*runtime, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		selected = append(selected, runtime)
	}
	m.runtimes = map[string]*runtime{}
	m.mu.Unlock()
	var result error
	for _, runtime := range selected {
		result = errors.Join(result, runtime.server.Shutdown(ctx))
	}
	return result
}

func (r *runtime) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	compiled := r.profile.Load()
	if compiled == nil {
		http.Error(writer, "mock profile is not loaded", http.StatusServiceUnavailable)
		return
	}
	profile := compiled.Profile()
	writer.Header().Set(ProfileHeader, profile.Name)
	route, err := compiled.Match(request.Method, request.URL.Path, request.URL.Query())
	if err != nil {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"error": map[string]string{
				"code":    "MOCK_ROUTE_NOT_MATCHED",
				"message": "No enabled route in mock profile " + profile.Name + " matched " + request.Method + " " + request.URL.RequestURI(),
			},
		})
		return
	}
	if route.DelayMS > 0 {
		timer := time.NewTimer(time.Duration(route.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-request.Context().Done():
			return
		}
	}
	for name, value := range route.Headers {
		writer.Header().Set(name, value)
	}
	writer.Header().Set(ProfileHeader, profile.Name)
	writer.Header().Set(RouteHeader, route.Name)
	writer.WriteHeader(route.Status)
	_, _ = writer.Write([]byte(route.Body))
}

func runtimeKey(scope, service string) string {
	return strings.ToLower(scope) + "\x00" + strings.ToLower(service)
}

func listenerPort(listener net.Listener) int {
	_, rawPort, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(rawPort)
	return port
}
