package debuglaunch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
)

const Host = "127.0.0.1"

type Result struct {
	Command     []string
	Environment map[string]string
	Debugger    model.DebuggerRuntime
}

func Prepare(capability *model.DebugCapability, port int, artifactsRoot string) (Result, error) {
	if capability == nil {
		return Result{}, errors.New("no safe debug launcher was discovered")
	}
	if port < 1 || port > 65535 {
		return Result{}, errors.New("debug port is invalid")
	}
	if len(capability.Command) == 0 {
		return Result{}, errors.New("debug command is empty")
	}
	result := Result{
		Environment: map[string]string{},
		Debugger: model.DebuggerRuntime{
			Adapter: capability.Adapter, Host: Host, Port: port, State: "starting",
		},
	}
	address := net.JoinHostPort(Host, strconv.Itoa(port))
	switch capability.Launcher {
	case model.DebugNodeDirect:
		if filepath.Base(capability.Command[0]) != "node" {
			return Result{}, errors.New("node debug command does not start with node")
		}
		result.Command = append([]string{capability.Command[0], "--inspect=" + address}, capability.Command[1:]...)
	case model.DebugNestCLI:
		result.Command = append(append([]string{}, capability.Command...), "--debug", address)
	case model.DebugSpringGradle:
		task, err := gradleBootRunTask(capability.Command)
		if err != nil {
			return Result{}, err
		}
		initScript, err := writeGradleInitScript(artifactsRoot)
		if err != nil {
			return Result{}, err
		}
		result.Command = append([]string{capability.Command[0], "--init-script", initScript}, capability.Command[1:]...)
		result.Environment["PORTLESS_GRADLE_DEBUG_PORT"] = strconv.Itoa(port)
		result.Environment["PORTLESS_GRADLE_DEBUG_TASK"] = task
	case model.DebugSpringMaven:
		result.Command = append([]string{}, capability.Command...)
		result.Command = append(result.Command, "-Dspring-boot.run.jvmArguments=-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address="+address)
	default:
		return Result{}, fmt.Errorf("unsupported debug launcher %q", capability.Launcher)
	}
	return result, nil
}

func Wait(ctx context.Context, debugger model.DebuggerRuntime) error {
	if debugger.Port < 1 || debugger.Port > 65535 || debugger.Host != Host {
		return errors.New("debugger endpoint is invalid")
	}
	for {
		ready, err := ready(ctx, debugger)
		if ready {
			return nil
		}
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s debugger on %s: %w", debugger.Adapter, net.JoinHostPort(debugger.Host, strconv.Itoa(debugger.Port)), ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func ready(ctx context.Context, debugger model.DebuggerRuntime) (bool, error) {
	address := net.JoinHostPort(debugger.Host, strconv.Itoa(debugger.Port))
	switch debugger.Adapter {
	case model.DebugNodeInspector:
		probeCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+address+"/json/list", nil)
		if err != nil {
			return false, err
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return false, err
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return response.StatusCode == http.StatusOK, nil
	case model.DebugJDWP:
		listener, err := net.Listen("tcp", address)
		if err == nil {
			_ = listener.Close()
			return false, nil
		}
		if errors.Is(err, syscall.EADDRINUSE) {
			return true, nil
		}
		return false, err
	default:
		return false, fmt.Errorf("unsupported debug adapter %q", debugger.Adapter)
	}
}

func gradleBootRunTask(command []string) (string, error) {
	for _, argument := range command[1:] {
		if argument == "bootRun" {
			return ":bootRun", nil
		}
		if strings.HasSuffix(argument, ":bootRun") {
			if strings.HasPrefix(argument, ":") {
				return argument, nil
			}
			return ":" + argument, nil
		}
	}
	return "", errors.New("Gradle debug command does not select a bootRun task")
}

func writeGradleInitScript(root string) (string, error) {
	if root == "" {
		return "", errors.New("Gradle debug artifacts directory is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create Gradle debug artifacts directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", fmt.Errorf("protect Gradle debug artifacts directory: %w", err)
	}
	path := filepath.Join(root, "portless-debug.init.gradle")
	const script = `gradle.beforeProject { project ->
    project.tasks.withType(org.gradle.api.tasks.JavaExec).configureEach { task ->
        def selectedTask = System.getenv('PORTLESS_GRADLE_DEBUG_TASK')
        if (selectedTask != null && task.path == selectedTask) {
            task.debugOptions {
                enabled = true
                port = Integer.parseInt(System.getenv('PORTLESS_GRADLE_DEBUG_PORT'))
                server = true
                suspend = false
            }
        }
    }
}
`
	temporary, err := os.CreateTemp(root, "gradle-debug-*.tmp")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", err
	}
	if _, err := temporary.WriteString(script); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", err
	}
	return path, nil
}
