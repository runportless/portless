// Package runtime owns the privileged HTTP and DNS forwarding data plane.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	// ControlOrigin is the clean control-plane origin served through the relay.
	ControlOrigin = "http://portless.localhost"
	// DefaultListenAddress is the privileged loopback HTTP listener.
	DefaultListenAddress = "127.0.0.1:80"
	// DefaultDNSAddress is the loopback TCP and UDP DNS listener.
	DefaultDNSAddress  = "127.77.0.1:1053"
	defaultConnections = 256
)

// Identity describes the private daemon targets and non-root user identity
// authorized to run the privileged relay.
type Identity struct {
	TargetSocket    string
	DNSTargetSocket string
	UID             int
	GID             int
}

type config struct {
	ListenAddress    string
	DNSListenAddress string
	identity         Identity
	DropPrivileges   bool
	MaxConnections   int
}

type runtimeLimits struct {
	httpConnections   int
	dnsTCPConnections int
	dnsUDPQueries     int
}

func defaultRuntimeLimits() runtimeLimits {
	return runtimeLimits{
		httpConnections: defaultConnections, dnsTCPConnections: defaultConnections, dnsUDPQueries: defaultConnections,
	}
}

func run(ctx context.Context, config config) error {
	if err := ValidateIdentity(config.identity); err != nil {
		return err
	}
	if config.ListenAddress == "" {
		config.ListenAddress = DefaultListenAddress
	}
	if config.DNSListenAddress == "" {
		config.DNSListenAddress = DefaultDNSAddress
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
		if err := dropPrivileges(config.identity.UID, config.identity.GID); err != nil {
			return err
		}
	}
	relayContext, cancel := context.WithCancel(ctx)
	limits := defaultRuntimeLimits()
	if config.MaxConnections > 0 {
		limits = runtimeLimits{
			httpConnections: config.MaxConnections, dnsTCPConnections: config.MaxConnections, dnsUDPQueries: config.MaxConnections,
		}
	}
	errChannel := make(chan error, 3)
	go func() {
		errChannel <- serveHTTPRelay(relayContext, listener, config.identity.TargetSocket, limits.httpConnections)
	}()
	go func() {
		errChannel <- serveDNSStreamRelay(relayContext, dnsTCP, config.identity.DNSTargetSocket, limits.dnsTCPConnections)
	}()
	go func() {
		errChannel <- serveDNSPacketRelay(relayContext, dnsUDP, config.identity.DNSTargetSocket, limits.dnsUDPQueries)
	}()
	firstErr := <-errChannel
	cancel()
	_ = listener.Close()
	_ = dnsTCP.Close()
	_ = dnsUDP.Close()
	return errors.Join(firstErr, <-errChannel, <-errChannel)
}

// ValidateIdentity verifies that a relay runtime identity contains only the
// fixed private socket targets and non-root user accepted by the helper.
func ValidateIdentity(identity Identity) error {
	if !filepath.IsAbs(identity.TargetSocket) || filepath.Base(filepath.Clean(identity.TargetSocket)) != "ingress.sock" {
		return errors.New("ingress target must be a private ingress.sock path")
	}
	if invalidServicePath(identity.TargetSocket) {
		return errors.New("ingress target socket contains an invalid control character or encoding")
	}
	if !filepath.IsAbs(identity.DNSTargetSocket) || filepath.Base(filepath.Clean(identity.DNSTargetSocket)) != "dns.sock" {
		return errors.New("DNS target must be a private dns.sock path")
	}
	if invalidServicePath(identity.DNSTargetSocket) {
		return errors.New("DNS target socket contains an invalid control character or encoding")
	}
	if identity.UID <= 0 || identity.GID <= 0 {
		return errors.New("relay helper requires a non-root user and group")
	}
	return nil
}

func invalidServicePath(path string) bool {
	return !utf8.ValidString(path) || strings.IndexFunc(path, unicode.IsControl) >= 0
}

func serveHTTPRelay(ctx context.Context, listener net.Listener, targetSocket string, maxConnections int) error {
	if !filepath.IsAbs(targetSocket) {
		return errors.New("ingress target socket must be an absolute path")
	}
	if maxConnections <= 0 {
		maxConnections = defaultConnections
	}
	stopClosing := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopClosing()
	connections := newActiveConnections()
	defer connections.closeAndWait()
	semaphore := make(chan struct{}, maxConnections)
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept localhost relay connection: %w", err)
		}
		select {
		case semaphore <- struct{}{}:
			connections.add(connection)
			go func() {
				defer func() { <-semaphore }()
				defer connections.done(connection)
				relayConnection(ctx, connection, targetSocket)
			}()
		default:
			writeUnavailable(connection, "The Portless relay is busy; retry the request.")
		}
	}
}

type activeConnections struct {
	mutex       sync.Mutex
	connections map[net.Conn]struct{}
	wait        sync.WaitGroup
}

func newActiveConnections() *activeConnections {
	return &activeConnections{connections: map[net.Conn]struct{}{}}
}

func (active *activeConnections) add(connection net.Conn) {
	active.mutex.Lock()
	active.connections[connection] = struct{}{}
	active.wait.Add(1)
	active.mutex.Unlock()
}

func (active *activeConnections) done(connection net.Conn) {
	active.mutex.Lock()
	delete(active.connections, connection)
	active.mutex.Unlock()
	active.wait.Done()
}

func (active *activeConnections) closeAndWait() {
	active.mutex.Lock()
	for connection := range active.connections {
		_ = connection.Close()
	}
	active.mutex.Unlock()
	active.wait.Wait()
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

func relayConnection(ctx context.Context, client net.Conn, targetSocket string) {
	defer client.Close()
	dialer := net.Dialer{Timeout: 2 * time.Second}
	upstream, err := dialer.DialContext(ctx, "unix", targetSocket)
	if err != nil {
		writeUnavailable(client, "Portless is not running; run `portless up` and retry.")
		return
	}
	defer upstream.Close()
	stopClosing := context.AfterFunc(ctx, func() {
		_ = client.Close()
		_ = upstream.Close()
	})
	defer stopClosing()

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
	body := renderUnavailablePage(message)
	response := fmt.Sprintf("HTTP/1.1 503 Service Unavailable\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\nCache-Control: no-store\r\nContent-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'\r\nReferrer-Policy: no-referrer\r\nRetry-After: 2\r\nX-Content-Type-Options: nosniff\r\nConnection: close\r\n\r\n%s", len(body), body)
	_, _ = io.WriteString(connection, response)
}
