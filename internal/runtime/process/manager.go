package process

import (
	"context"
	"crypto/rand"
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
)

type ExitEvent struct {
	Scope      string
	Service    string
	Generation int64
	Error      error
	Expected   bool
}

type StartResult struct {
	PID           int
	Port          int
	Generation    int64
	PrivateRunKey string
	StartedAt     time.Time
	LogDirectory  string
}

type managedProcess struct {
	scope      string
	service    string
	generation int64
	privateKey string
	command    *exec.Cmd
	done       chan struct{}
	exitError  error
	stopping   atomic.Bool
}

type Manager struct {
	mu     sync.Mutex
	runs   map[string]*managedProcess
	onExit func(ExitEvent)
}

func NewManager(onExit func(ExitEvent)) *Manager {
	return &Manager{runs: make(map[string]*managedProcess), onExit: onExit}
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
	if definition.Kind != model.ServiceProcess {
		return StartResult{}, errors.New("process manager only starts process services")
	}
	if len(definition.Command) == 0 {
		return StartResult{}, errors.New("service command is empty")
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
	stdout, err := os.OpenFile(filepath.Join(logDirectory, "stdout.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return StartResult{}, err
	}
	stderr, err := os.OpenFile(filepath.Join(logDirectory, "stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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

func runMapKey(scope, service string) string { return scope + "\x00" + service }

func privateRunKey() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
