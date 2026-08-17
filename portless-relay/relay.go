// Package relay owns the privileged HTTP and DNS forwarding data plane.
package relay

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
	// DefaultListenAddress is the privileged loopback HTTP listener.
	DefaultListenAddress = "127.0.0.1:80"
	// DefaultDNSAddress is the loopback TCP and UDP DNS listener.
	DefaultDNSAddress  = "127.77.0.1:1053"
	defaultConnections = 256
)

// Config defines the public listeners, private daemon sockets, privilege
// target, and concurrency limit for a relay process.
type Config struct {
	ListenAddress    string
	TargetSocket     string
	DNSListenAddress string
	DNSTargetSocket  string
	UID              int
	GID              int
	DropPrivileges   bool
	MaxConnections   int
}

// Run starts the HTTP and DNS relay listeners, optionally drops privileges,
// and serves until a listener fails or ctx is canceled.
func Run(ctx context.Context, config Config) error {
	if config.ListenAddress == "" {
		config.ListenAddress = DefaultListenAddress
	}
	if !filepath.IsAbs(config.TargetSocket) {
		return errors.New("ingress target socket must be an absolute path")
	}
	if config.DNSListenAddress == "" {
		config.DNSListenAddress = DefaultDNSAddress
	}
	if !filepath.IsAbs(config.DNSTargetSocket) {
		return errors.New("DNS target socket must be an absolute path")
	}
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen for the localhost relay on %s: %w", config.ListenAddress, err)
	}
	defer listener.Close()
	dnsTCP, err := net.Listen("tcp", config.DNSListenAddress)
	if err != nil {
		return fmt.Errorf("listen for Portless TCP DNS on %s: %w", config.DNSListenAddress, err)
	}
	defer dnsTCP.Close()
	dnsUDP, err := net.ListenPacket("udp", config.DNSListenAddress)
	if err != nil {
		return fmt.Errorf("listen for Portless UDP DNS on %s: %w", config.DNSListenAddress, err)
	}
	defer dnsUDP.Close()
	if config.DropPrivileges {
		if err := dropPrivileges(config.UID, config.GID); err != nil {
			return err
		}
	}
	relayContext, cancel := context.WithCancel(ctx)
	defer cancel()
	errChannel := make(chan error, 3)
	go func() { errChannel <- ServeRelay(relayContext, listener, config.TargetSocket, config.MaxConnections) }()
	go func() {
		errChannel <- ServeDNSStreamRelay(relayContext, dnsTCP, config.DNSTargetSocket, config.MaxConnections)
	}()
	go func() {
		errChannel <- ServeDNSPacketRelay(relayContext, dnsUDP, config.DNSTargetSocket, config.MaxConnections)
	}()
	err = <-errChannel
	cancel()
	_ = listener.Close()
	_ = dnsTCP.Close()
	_ = dnsUDP.Close()
	return err
}

// ServeRelay forwards HTTP connections from listener to the daemon's private
// ingress socket with a bounded number of concurrent connections.
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
			return fmt.Errorf("accept localhost relay connection: %w", err)
		}
		select {
		case semaphore <- struct{}{}:
			go func() {
				defer func() { <-semaphore }()
				relayConnection(connection, targetSocket)
			}()
		default:
			writeUnavailable(connection, "The Portless relay is busy; retry the request.")
		}
	}
}

func dropPrivileges(uid, gid int) error {
	if uid <= 0 || gid <= 0 {
		return errors.New("relay helper requires a non-root user and group")
	}
	if os.Geteuid() != 0 {
		return errors.New("relay helper must start as root before dropping privileges")
	}
	if err := unix.Setgroups([]int{}); err != nil {
		return fmt.Errorf("clear relay helper supplementary groups: %w", err)
	}
	if err := unix.Setgid(gid); err != nil {
		return fmt.Errorf("drop relay helper group privileges: %w", err)
	}
	if err := unix.Setuid(uid); err != nil {
		return fmt.Errorf("drop relay helper user privileges: %w", err)
	}
	if os.Geteuid() != uid || os.Getegid() != gid {
		return errors.New("relay helper did not drop privileges")
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
