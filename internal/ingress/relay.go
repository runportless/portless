package ingress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	DefaultListenAddress = "127.0.0.1:80"
	defaultConnections   = 256
)

type RelayConfig struct {
	ListenAddress  string
	TargetSocket   string
	UID            int
	GID            int
	DropPrivileges bool
	MaxConnections int
}

func RunRelay(ctx context.Context, config RelayConfig) error {
	if config.ListenAddress == "" {
		config.ListenAddress = DefaultListenAddress
	}
	if !filepath.IsAbs(config.TargetSocket) {
		return errors.New("ingress target socket must be an absolute path")
	}
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen for clean localhost ingress on %s: %w", config.ListenAddress, err)
	}
	defer listener.Close()
	if config.DropPrivileges {
		if err := dropPrivileges(config.UID, config.GID); err != nil {
			return err
		}
	}
	return ServeRelay(ctx, listener, config.TargetSocket, config.MaxConnections)
}

func ServeRelay(ctx context.Context, listener net.Listener, targetSocket string, maxConnections int) error {
	if !filepath.IsAbs(targetSocket) {
		return errors.New("ingress target socket must be an absolute path")
	}
	if maxConnections <= 0 {
		maxConnections = defaultConnections
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	semaphore := make(chan struct{}, maxConnections)
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept clean localhost ingress: %w", err)
		}
		select {
		case semaphore <- struct{}{}:
			go func() {
				defer func() { <-semaphore }()
				relayConnection(connection, targetSocket)
			}()
		default:
			writeUnavailable(connection, "Portless ingress is busy; retry the request.")
		}
	}
}

func dropPrivileges(uid, gid int) error {
	if uid <= 0 || gid <= 0 {
		return errors.New("ingress helper requires a non-root user and group")
	}
	if os.Geteuid() != 0 {
		return errors.New("ingress helper must start as root before dropping privileges")
	}
	if err := unix.Setgroups([]int{}); err != nil {
		return fmt.Errorf("clear ingress helper supplementary groups: %w", err)
	}
	if err := unix.Setgid(gid); err != nil {
		return fmt.Errorf("drop ingress helper group privileges: %w", err)
	}
	if err := unix.Setuid(uid); err != nil {
		return fmt.Errorf("drop ingress helper user privileges: %w", err)
	}
	if os.Geteuid() != uid || os.Getegid() != gid {
		return errors.New("ingress helper did not drop privileges")
	}
	return nil
}

func relayConnection(client net.Conn, targetSocket string) {
	defer client.Close()
	upstream, err := net.DialTimeout("unix", targetSocket, 2*time.Second)
	if err != nil {
		writeUnavailable(client, "Portless is not running; run `portless up` and retry.")
		return
	}
	defer upstream.Close()

	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(upstream, client)
		closeWrite(upstream)
	}()
	go func() {
		defer copies.Done()
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
	}()
	copies.Wait()
}

func closeWrite(connection net.Conn) {
	if writer, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = writer.CloseWrite()
	}
}

func writeUnavailable(connection net.Conn, message string) {
	defer connection.Close()
	_ = connection.SetWriteDeadline(time.Now().Add(time.Second))
	response := fmt.Sprintf("HTTP/1.1 503 Service Unavailable\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(message), message)
	_, _ = io.WriteString(connection, response)
}
