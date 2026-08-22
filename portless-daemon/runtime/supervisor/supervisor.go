package supervisor

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/runtime/logstore"
)

const (
	// ProtocolVersion is the semantic version of the private runner control protocol.
	ProtocolVersion = "2.0.0"
	statusPath      = "/v1/status"
	stopPath        = "/v1/stop"
)

// Manifest contains private ownership and launch data consumed by a service runner.
type Manifest struct {
	SocketPath  string                  `json:"socketPath"`
	StatePath   string                  `json:"statePath"`
	RunKey      string                  `json:"runKey"`
	Scope       string                  `json:"scope"`
	Service     string                  `json:"service"`
	Generation  int64                   `json:"generation"`
	Port        int                     `json:"port"`
	LaunchMode  model.LaunchMode        `json:"launchMode"`
	Debugger    *model.DebuggerRuntime  `json:"debugger,omitempty"`
	Definition  model.ServiceDefinition `json:"definition"`
	Environment map[string]string       `json:"environment"`
	LogsRoot    string                  `json:"logsRoot"`
}

// Status is the authenticated live or durable state of a supervised service run.
type Status struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Scope           string                 `json:"scope"`
	Service         string                 `json:"service"`
	Generation      int64                  `json:"generation"`
	SupervisorPID   int                    `json:"supervisorPid"`
	PID             int                    `json:"pid,omitempty"`
	Port            int                    `json:"port"`
	LaunchMode      model.LaunchMode       `json:"launchMode"`
	Debugger        *model.DebuggerRuntime `json:"debugger,omitempty"`
	State           string                 `json:"state"`
	Error           string                 `json:"error,omitempty"`
	Expected        bool                   `json:"expected"`
	StartedAt       time.Time              `json:"startedAt"`
	ExitedAt        *time.Time             `json:"exitedAt,omitempty"`
	LogDirectory    string                 `json:"logDirectory"`
}

type runner struct {
	manifest Manifest
	mu       sync.RWMutex
	status   Status
	command  *exec.Cmd
	stopOnce sync.Once
	stop     chan struct{}
}

// Run consumes a private manifest and supervises the service until exit or shutdown.
func Run(ctx context.Context, manifestPath string) error {
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	_ = os.Remove(manifestPath)
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manifest.SocketPath), 0o700); err != nil {
		return err
	}
	if err := removeStaleSocket(manifest.SocketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", manifest.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on supervisor socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(manifest.SocketPath)
	if err := os.Chmod(manifest.SocketPath, 0o600); err != nil {
		return err
	}

	logDirectory := filepath.Join(manifest.LogsRoot, manifest.Service, strconv.FormatInt(manifest.Generation, 10))
	stdout, err := logstore.OpenSink(logDirectory, manifest.Service, "stdout", manifest.Generation)
	if err != nil {
		return err
	}
	stderr, err := logstore.OpenSink(logDirectory, manifest.Service, "stderr", manifest.Generation)
	if err != nil {
		_ = stdout.Close()
		return err
	}
	defer stdout.Close()
	defer stderr.Close()

	command := exec.Command(manifest.Definition.Command[0], manifest.Definition.Command[1:]...)
	command.Dir = manifest.Definition.WorkingDirectory
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Env = append([]string{}, os.Environ()...)
	portEnvironment := manifest.Definition.PortEnvironment
	if portEnvironment == "" {
		portEnvironment = "PORT"
	}
	project, environmentName := scopeNames(manifest.Scope)
	command.Env = append(command.Env,
		portEnvironment+"="+strconv.Itoa(manifest.Port),
		"PORTLESS_PROJECT="+project,
		"PORTLESS_ENVIRONMENT="+environmentName,
		"PORTLESS_SERVICE="+manifest.Service,
		"PORTLESS_RUN_GENERATION="+strconv.FormatInt(manifest.Generation, 10),
	)
	for name, value := range manifest.Definition.Environment {
		command.Env = append(command.Env, name+"="+value)
	}
	for name, value := range manifest.Environment {
		command.Env = append(command.Env, name+"="+value)
	}

	startedAt := time.Now().UTC()
	run := &runner{manifest: manifest, command: command, stop: make(chan struct{}), status: Status{
		ProtocolVersion: ProtocolVersion, Scope: manifest.Scope, Service: manifest.Service,
		Generation: manifest.Generation, SupervisorPID: os.Getpid(), Port: manifest.Port,
		LaunchMode: manifest.LaunchMode, Debugger: cloneDebugger(manifest.Debugger),
		State: "starting", StartedAt: startedAt, LogDirectory: logDirectory,
	}}
	if err := run.persist(); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		run.finish("failed", err, false)
		return fmt.Errorf("start %s: %w", manifest.Service, err)
	}
	run.mu.Lock()
	run.status.PID = command.Process.Pid
	run.status.State = "ready"
	run.mu.Unlock()
	if err := run.persist(); err != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc(statusPath, run.statusHandler)
	mux.HandleFunc(stopPath, run.stopHandler)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8 << 10}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	processDone := make(chan error, 1)
	go func() { processDone <- command.Wait() }()

	select {
	case err := <-processDone:
		run.finish(exitState(err), err, run.current().Expected)
	case <-run.stop:
		run.stopProcess(command.Process.Pid, processDone)
	case <-ctx.Done():
		run.requestStop(true)
		run.stopProcess(command.Process.Pid, processDone)
	case err := <-serverDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			run.requestStop(false)
			run.stopProcess(command.Process.Pid, processDone)
			run.finish("failed", err, false)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	return nil
}

func (r *runner) statusHandler(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !authorized(request, r.manifest.RunKey) {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(r.current())
}

func (r *runner) stopHandler(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !authorized(request, r.manifest.RunKey) {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	r.requestStop(true)
	writer.WriteHeader(http.StatusAccepted)
}

func (r *runner) requestStop(expected bool) {
	r.mu.Lock()
	if r.status.State == "ready" || r.status.State == "starting" {
		r.status.State = "stopping"
		r.status.Expected = expected
	}
	r.mu.Unlock()
	_ = r.persist()
	r.stopOnce.Do(func() { close(r.stop) })
}

func (r *runner) stopProcess(pid int, done <-chan error) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	var err error
	select {
	case err = <-done:
	case <-timer.C:
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		err = <-done
	}
	r.finish("stopped", err, true)
}

func (r *runner) finish(state string, err error, expected bool) {
	now := time.Now().UTC()
	r.mu.Lock()
	r.status.State = state
	r.status.Expected = expected
	r.status.ExitedAt = &now
	if err != nil && !expected {
		r.status.Error = err.Error()
	}
	r.mu.Unlock()
	_ = r.persist()
}

func (r *runner) current() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *runner) persist() error { return writePrivateJSON(r.manifest.StatePath, r.current()) }

// StatusFor returns authenticated live status or falls back to validated durable terminal state.
func StatusFor(ctx context.Context, socketPath, statePath, runKey string) (Status, error) {
	status, err := LiveStatus(ctx, socketPath, runKey)
	if err == nil {
		return status, nil
	}
	content, readErr := os.ReadFile(statePath)
	if readErr != nil {
		return Status{}, err
	}
	if decodeErr := json.Unmarshal(content, &status); decodeErr != nil {
		return Status{}, decodeErr
	}
	return status, validateStatus(status)
}

// LiveStatus reads authenticated status from a running supervisor's private socket.
func LiveStatus(ctx context.Context, socketPath, runKey string) (Status, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	}}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://supervisor"+statusPath, nil)
	if err != nil {
		return Status{}, err
	}
	request.Header.Set("Authorization", "Bearer "+runKey)
	response, err := client.Do(request)
	if err != nil {
		return Status{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return Status{}, errors.New("supervisor authentication failed")
	}
	if response.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("supervisor status returned %s", response.Status)
	}
	var status Status
	if decodeErr := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&status); decodeErr != nil {
		return Status{}, decodeErr
	}
	return status, validateStatus(status)
}

// Stop requests authenticated shutdown and waits for a terminal supervisor state.
func Stop(ctx context.Context, socketPath, statePath, runKey string) (Status, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	}}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://supervisor"+stopPath, nil)
	if err != nil {
		return Status{}, err
	}
	request.Header.Set("Authorization", "Bearer "+runKey)
	response, err := client.Do(request)
	if err != nil {
		return Status{}, err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return Status{}, fmt.Errorf("supervisor stop returned %s", response.Status)
	}
	for {
		status, statusErr := StatusFor(ctx, socketPath, statePath, runKey)
		if statusErr == nil && terminal(status.State) {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// WriteManifest persists a private, atomic supervisor launch manifest.
func WriteManifest(path string, manifest Manifest) error { return writePrivateJSON(path, manifest) }

// StatePath derives the default durable status path from a supervisor socket path.
func StatePath(socketPath string) string { return strings.TrimSuffix(socketPath, ".sock") + ".json" }

func terminal(state string) bool { return state == "stopped" || state == "exited" || state == "failed" }

func validateStatus(status Status) error {
	if status.ProtocolVersion != ProtocolVersion || status.Scope == "" || status.Service == "" || status.Generation <= 0 || status.Port <= 0 || !validLaunch(status.LaunchMode, status.Debugger) {
		return errors.New("invalid supervisor status")
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.SocketPath == "" || manifest.StatePath == "" || manifest.RunKey == "" || manifest.Scope == "" || manifest.Service == "" || manifest.Generation <= 0 || manifest.Port <= 0 {
		return errors.New("supervisor manifest is incomplete")
	}
	if len(manifest.Definition.Command) == 0 || manifest.Definition.Kind != model.ServiceProcess {
		return errors.New("supervisor requires a process service command")
	}
	if !validLaunch(manifest.LaunchMode, manifest.Debugger) {
		return errors.New("supervisor launch mode is invalid")
	}
	return nil
}

func validLaunch(mode model.LaunchMode, debugger *model.DebuggerRuntime) bool {
	switch mode {
	case model.LaunchManaged:
		return debugger == nil
	case model.LaunchDebug:
		return debugger != nil && debugger.Adapter != "" && debugger.Host == "127.0.0.1" && debugger.Port > 0 && debugger.Port <= 65535
	default:
		return false
	}
}

func cloneDebugger(input *model.DebuggerRuntime) *model.DebuggerRuntime {
	if input == nil {
		return nil
	}
	result := *input
	return &result
}

func readManifest(path string) (Manifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Manifest{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return Manifest{}, errors.New("supervisor manifest is not a private regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func writePrivateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if err := json.NewEncoder(temporary).Encode(value); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("refusing to replace non-socket supervisor path")
	}
	return os.Remove(path)
}

func authorized(request *http.Request, key string) bool {
	provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	return len(provided) == len(key) && subtle.ConstantTimeCompare([]byte(provided), []byte(key)) == 1
}

func exitState(err error) string {
	if err == nil {
		return "exited"
	}
	return "failed"
}

func scopeNames(scope string) (string, string) {
	project, environment, found := strings.Cut(scope, "/")
	if !found {
		return scope, ""
	}
	return project, environment
}
