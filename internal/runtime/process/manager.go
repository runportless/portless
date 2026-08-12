package process

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/runtime/health"
	"github.com/portless-run/portless/internal/runtime/logstore"
	"github.com/portless-run/portless/internal/runtime/supervisor"
)

type ExitEvent struct {
	Scope      string
	Service    string
	Generation int64
	Error      error
	Expected   bool
}

type StartResult struct {
	PID              int
	Port             int
	Generation       int64
	PrivateRunKey    string
	StartedAt        time.Time
	LogDirectory     string
	SupervisorSocket string
	SupervisorState  string
	SupervisorPID    int
}

type managedProcess struct {
	scope            string
	service          string
	generation       int64
	privateKey       string
	command          *exec.Cmd
	done             chan struct{}
	exitError        error
	stopping         atomic.Bool
	supervisorSocket string
	supervisorState  string
	supervisorPID    int
	pid              int
}

type Manager struct {
	mu         sync.Mutex
	runs       map[string]*managedProcess
	onExit     func(ExitEvent)
	executable string
	runsRoot   string
	socketRoot string
	supervised bool
	monitorCtx context.Context
	cancel     context.CancelFunc
}

func NewManager(onExit func(ExitEvent)) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{runs: make(map[string]*managedProcess), onExit: onExit, monitorCtx: ctx, cancel: cancel}
}

func NewSupervisedManager(executable, runsRoot string, onExit func(ExitEvent)) *Manager {
	manager := NewManager(onExit)
	manager.executable = executable
	manager.runsRoot = runsRoot
	rootDigest := sha256.Sum256([]byte(runsRoot))
	manager.socketRoot = filepath.Join(os.TempDir(), fmt.Sprintf("portless-%d", os.Geteuid()), hex.EncodeToString(rootDigest[:6]))
	manager.supervised = true
	return manager
}

func AllocatePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func (m *Manager) Start(ctx context.Context, scope string, definition model.ServiceDefinition, generation int64, environment map[string]string, logsRoot string) (StartResult, error) {
	return m.StartPrepared(ctx, scope, definition, generation, environment, logsRoot, nil)
}

func (m *Manager) StartPrepared(ctx context.Context, scope string, definition model.ServiceDefinition, generation int64, environment map[string]string, logsRoot string, prepared func(StartResult) error) (StartResult, error) {
	if definition.Kind != model.ServiceProcess {
		return StartResult{}, errors.New("process manager only starts process services")
	}
	if len(definition.Command) == 0 {
		return StartResult{}, errors.New("service command is empty")
	}
	if m.supervised {
		return m.startSupervised(ctx, scope, definition, generation, environment, logsRoot, prepared)
	}
	project, environmentName, err := model.ParseEnvironmentSelector(scope)
	if err != nil {
		return StartResult{}, err
	}
	key := runMapKey(scope, definition.Name)
	m.mu.Lock()
	if current := m.runs[key]; current != nil {
		select {
		case <-current.done:
			delete(m.runs, key)
		default:
			m.mu.Unlock()
			return StartResult{}, fmt.Errorf("service %s is already running", definition.Name)
		}
	}
	m.mu.Unlock()

	port, err := AllocatePort()
	if err != nil {
		return StartResult{}, fmt.Errorf("allocate service port: %w", err)
	}
	privateKey, err := privateRunKey()
	if err != nil {
		return StartResult{}, err
	}
	logDirectory := filepath.Join(logsRoot, definition.Name, strconv.FormatInt(generation, 10))
	if err := os.MkdirAll(logDirectory, 0o700); err != nil {
		return StartResult{}, err
	}
	stdout, err := logstore.OpenSink(logDirectory, definition.Name, "stdout", generation)
	if err != nil {
		return StartResult{}, err
	}
	stderr, err := logstore.OpenSink(logDirectory, definition.Name, "stderr", generation)
	if err != nil {
		stdout.Close()
		return StartResult{}, err
	}
	command := exec.Command(definition.Command[0], definition.Command[1:]...)
	command.Dir = definition.WorkingDirectory
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Env = append([]string{}, os.Environ()...)
	portEnvironment := definition.PortEnvironment
	if portEnvironment == "" {
		portEnvironment = "PORT"
	}
	command.Env = append(command.Env,
		portEnvironment+"="+strconv.Itoa(port),
		"PORTLESS_PROJECT="+project,
		"PORTLESS_ENVIRONMENT="+environmentName,
		"PORTLESS_SERVICE="+definition.Name,
		"PORTLESS_RUN_GENERATION="+strconv.FormatInt(generation, 10),
	)
	for name, value := range definition.Environment {
		command.Env = append(command.Env, name+"="+value)
	}
	for name, value := range environment {
		command.Env = append(command.Env, name+"="+value)
	}
	startedAt := time.Now().UTC()
	if err := command.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return StartResult{}, fmt.Errorf("start %s: %w", definition.Name, err)
	}
	run := &managedProcess{scope: scope, service: definition.Name, generation: generation, privateKey: privateKey, command: command, done: make(chan struct{})}
	m.mu.Lock()
	m.runs[key] = run
	m.mu.Unlock()
	go func() {
		run.exitError = command.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
		close(run.done)
		if m.onExit != nil {
			m.onExit(ExitEvent{Scope: scope, Service: definition.Name, Generation: generation, Error: run.exitError, Expected: run.stopping.Load()})
		}
	}()
	result := StartResult{PID: command.Process.Pid, Port: port, Generation: generation, PrivateRunKey: privateKey, StartedAt: startedAt, LogDirectory: logDirectory}
	if prepared != nil {
		if err := prepared(result); err != nil {
			_ = m.Stop(context.Background(), scope, definition.Name, 3*time.Second)
			return StartResult{}, err
		}
	}
	if err := health.Wait(ctx, port, definition.Health); err != nil {
		_ = m.Stop(context.Background(), scope, definition.Name, 3*time.Second)
		return result, err
	}
	return result, nil
}

func (m *Manager) Stop(ctx context.Context, scope, service string, timeout time.Duration) error {
	key := runMapKey(scope, service)
	m.mu.Lock()
	run := m.runs[key]
	m.mu.Unlock()
	if run == nil {
		return nil
	}
	if run.supervisorSocket != "" {
		stopCtx := ctx
		var cancel context.CancelFunc
		if timeout > 0 {
			stopCtx, cancel = context.WithTimeout(ctx, timeout+2*time.Second)
			defer cancel()
		}
		_, err := supervisor.Stop(stopCtx, run.supervisorSocket, run.supervisorState, run.privateKey)
		if err != nil {
			return err
		}
		select {
		case <-run.done:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		m.mu.Lock()
		delete(m.runs, key)
		m.mu.Unlock()
		return nil
	}
	select {
	case <-run.done:
		m.mu.Lock()
		delete(m.runs, key)
		m.mu.Unlock()
		return nil
	default:
	}
	if run.command.Process == nil {
		return nil
	}
	run.stopping.Store(true)
	if err := syscall.Kill(-run.command.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-run.done:
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		if err := syscall.Kill(-run.command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		select {
		case <-run.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	delete(m.runs, key)
	m.mu.Unlock()
	return nil
}

func (m *Manager) IsRunning(scope, service string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.runs[runMapKey(scope, service)]
	if run == nil {
		return false
	}
	select {
	case <-run.done:
		return false
	default:
		return true
	}
}

func (m *Manager) Attach(ctx context.Context, scope, service string, generation int64, socketPath, statePath, privateKey string) (StartResult, error) {
	if !m.supervised {
		return StartResult{}, errors.New("process manager does not support supervisor attachment")
	}
	if statePath == "" {
		statePath = supervisor.StatePath(socketPath)
	}
	status, err := supervisor.LiveStatus(ctx, socketPath, privateKey)
	if err != nil {
		return StartResult{}, err
	}
	if status.Scope != scope || status.Service != service || status.Generation != generation {
		return StartResult{}, errors.New("supervisor identity does not match persisted service run")
	}
	if status.State != "ready" && status.State != "starting" {
		return StartResult{}, fmt.Errorf("supervisor reports service %s", status.State)
	}
	run := &managedProcess{
		scope: scope, service: service, generation: generation, privateKey: privateKey,
		done: make(chan struct{}), supervisorSocket: socketPath, supervisorState: statePath,
		supervisorPID: status.SupervisorPID, pid: status.PID,
	}
	if err := m.installSupervisedRun(run); err != nil {
		return StartResult{}, err
	}
	return StartResult{
		PID: status.PID, Port: status.Port, Generation: status.Generation, PrivateRunKey: privateKey,
		StartedAt: status.StartedAt, LogDirectory: status.LogDirectory,
		SupervisorSocket: socketPath, SupervisorState: statePath, SupervisorPID: status.SupervisorPID,
	}, nil
}

func (m *Manager) startSupervised(ctx context.Context, scope string, definition model.ServiceDefinition, generation int64, environment map[string]string, logsRoot string, prepared func(StartResult) error) (StartResult, error) {
	if m.executable == "" || m.runsRoot == "" || m.socketRoot == "" {
		return StartResult{}, errors.New("supervisor executable and runtime directory are required")
	}
	if _, _, err := model.ParseEnvironmentSelector(scope); err != nil {
		return StartResult{}, err
	}
	key := runMapKey(scope, definition.Name)
	m.mu.Lock()
	if current := m.runs[key]; current != nil {
		select {
		case <-current.done:
			delete(m.runs, key)
		default:
			m.mu.Unlock()
			return StartResult{}, fmt.Errorf("service %s is already running", definition.Name)
		}
	}
	m.mu.Unlock()
	port, err := AllocatePort()
	if err != nil {
		return StartResult{}, fmt.Errorf("allocate service port: %w", err)
	}
	privateKey, err := privateRunKey()
	if err != nil {
		return StartResult{}, err
	}
	if err := os.MkdirAll(m.runsRoot, 0o700); err != nil {
		return StartResult{}, err
	}
	if err := ensurePrivateSocketDirectory(m.socketRoot); err != nil {
		return StartResult{}, err
	}
	digest := sha256.Sum256([]byte(scope + "\x00" + definition.Name + "\x00" + strconv.FormatInt(generation, 10)))
	base := hex.EncodeToString(digest[:10])
	socketPath := filepath.Join(m.socketRoot, base+".sock")
	statePath := filepath.Join(m.runsRoot, base+".state.json")
	manifestPath := filepath.Join(m.runsRoot, base+".manifest.json")
	startedAt := time.Now().UTC()
	provisional := StartResult{
		Port: port, Generation: generation, PrivateRunKey: privateKey, StartedAt: startedAt,
		LogDirectory:     filepath.Join(logsRoot, definition.Name, strconv.FormatInt(generation, 10)),
		SupervisorSocket: socketPath, SupervisorState: statePath,
	}
	if prepared != nil {
		if err := prepared(provisional); err != nil {
			return StartResult{}, err
		}
	}
	manifest := supervisor.Manifest{
		SocketPath: socketPath, StatePath: statePath, RunKey: privateKey, Scope: scope,
		Service: definition.Name, Generation: generation, Port: port, Definition: definition,
		Environment: environment, LogsRoot: logsRoot,
	}
	if err := supervisor.WriteManifest(manifestPath, manifest); err != nil {
		return StartResult{}, err
	}
	command := exec.Command(m.executable, "__runner", "--manifest", manifestPath)
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = os.Remove(manifestPath)
		return StartResult{}, fmt.Errorf("start service supervisor: %w", err)
	}
	provisional.SupervisorPID = command.Process.Pid
	if prepared != nil {
		if err := prepared(provisional); err != nil {
			m.stopUnreadySupervisor(socketPath, statePath, privateKey, provisional.SupervisorPID)
			_ = command.Wait()
			return StartResult{}, err
		}
	}
	_ = command.Process.Release()
	deadline := time.Now().Add(10 * time.Second)
	var status supervisor.Status
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		status, err = supervisor.LiveStatus(probeCtx, socketPath, privateKey)
		cancel()
		if err == nil && status.State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = fmt.Errorf("supervisor entered state %s", status.State)
			}
			m.stopUnreadySupervisor(socketPath, statePath, privateKey, provisional.SupervisorPID)
			return StartResult{}, fmt.Errorf("service supervisor did not become ready: %w", err)
		}
		select {
		case <-ctx.Done():
			m.stopUnreadySupervisor(socketPath, statePath, privateKey, provisional.SupervisorPID)
			return StartResult{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	run := &managedProcess{
		scope: scope, service: definition.Name, generation: generation, privateKey: privateKey,
		done: make(chan struct{}), supervisorSocket: socketPath, supervisorState: statePath,
		supervisorPID: status.SupervisorPID, pid: status.PID,
	}
	if err := m.installSupervisedRun(run); err != nil {
		_, _ = supervisor.Stop(context.Background(), socketPath, statePath, privateKey)
		return StartResult{}, err
	}
	result := StartResult{
		PID: status.PID, Port: status.Port, Generation: generation, PrivateRunKey: privateKey,
		StartedAt: status.StartedAt, LogDirectory: status.LogDirectory,
		SupervisorSocket: socketPath, SupervisorState: statePath, SupervisorPID: status.SupervisorPID,
	}
	if err := health.Wait(ctx, port, definition.Health); err != nil {
		_ = m.Stop(context.Background(), scope, definition.Name, 3*time.Second)
		return result, err
	}
	return result, nil
}

func (m *Manager) stopUnreadySupervisor(socketPath, statePath, privateKey string, supervisorPID int) {
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, err := supervisor.Stop(stopCtx, socketPath, statePath, privateKey)
	cancel()
	if err != nil && supervisorPID > 0 {
		_ = syscall.Kill(-supervisorPID, syscall.SIGKILL)
	}
}

func (m *Manager) installSupervisedRun(run *managedProcess) error {
	key := runMapKey(run.scope, run.service)
	m.mu.Lock()
	if current := m.runs[key]; current != nil {
		select {
		case <-current.done:
			delete(m.runs, key)
		default:
			m.mu.Unlock()
			return fmt.Errorf("service %s is already attached", run.service)
		}
	}
	m.runs[key] = run
	m.mu.Unlock()
	go m.watchSupervisor(run)
	return nil
}

func (m *Manager) watchSupervisor(run *managedProcess) {
	consecutiveFailures := 0
	for {
		probeCtx, cancel := context.WithTimeout(m.monitorCtx, 500*time.Millisecond)
		status, err := supervisor.LiveStatus(probeCtx, run.supervisorSocket, run.privateKey)
		cancel()
		if m.monitorCtx.Err() != nil {
			return
		}
		if err == nil {
			consecutiveFailures = 0
			if supervisorTerminal(status.State) {
				m.finishSupervisedRun(run, status)
				return
			}
		} else {
			// A normally exiting runner writes its terminal state before removing
			// the socket. Read that durable state only for terminal results; a
			// stale "ready" file must never make a dead runner look alive.
			persisted, persistedErr := supervisor.StatusFor(probeCtx, run.supervisorSocket, run.supervisorState, run.privateKey)
			if persistedErr == nil && supervisorTerminal(persisted.State) {
				m.finishSupervisedRun(run, persisted)
				return
			}
			consecutiveFailures++
			if consecutiveFailures >= 8 {
				run.exitError = fmt.Errorf("service supervisor became unavailable: %w", err)
				close(run.done)
				if m.onExit != nil {
					m.onExit(ExitEvent{Scope: run.scope, Service: run.service, Generation: run.generation, Error: run.exitError})
				}
				return
			}
		}
		select {
		case <-m.monitorCtx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (m *Manager) finishSupervisedRun(run *managedProcess, status supervisor.Status) {
	run.exitError = nil
	if status.Error != "" {
		run.exitError = errors.New(status.Error)
	}
	close(run.done)
	if m.onExit != nil {
		m.onExit(ExitEvent{Scope: run.scope, Service: run.service, Generation: run.generation, Error: run.exitError, Expected: status.Expected})
	}
}

func supervisorTerminal(state string) bool {
	return state == "stopped" || state == "exited" || state == "failed"
}

func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}
}

func runMapKey(scope, service string) string { return scope + "\x00" + service }

func privateRunKey() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func ensurePrivateSocketDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("supervisor socket directory must be a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("supervisor socket directory belongs to another user")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}
