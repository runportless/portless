// Package health probes the relay's HTTP, DNS, private-socket, and host
// resolver paths and defines bounded end-to-end readiness.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"time"

	portlessdns "github.com/runportless/portless/portless-daemon/dns"
	"github.com/runportless/portless/portless-daemon/networking"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

const localhostResolverProbe = "resolver.portless.localhost"

// Probes contains the independent end-to-end checks that make up relay
// readiness inspection.
type Probes struct {
	HTTP     func(context.Context) error
	DNS      func(context.Context) error
	Resolver func(context.Context) error
}

// Inspection records the result of each independent relay health path.
type Inspection struct {
	HTTPError     error
	DNSError      error
	ResolverError error
}

// DefaultProbes returns the production HTTP, DNS, and system-resolver checks.
func DefaultProbes() Probes {
	return Probes{HTTP: Check, DNS: CheckDNS, Resolver: CheckResolver}
}

// Check verifies end-to-end HTTP access through the default privileged relay.
func Check(ctx context.Context) error {
	return checkAt(ctx, relayruntime.DefaultListenAddress, relayruntime.ControlOrigin)
}

// CheckSocket verifies the daemon's private ingress listener without using the
// privileged port-80 relay.
func CheckSocket(ctx context.Context, socketPath string) error {
	if !filepath.IsAbs(socketPath) {
		return errors.New("daemon ingress socket path must be absolute")
	}
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	return checkWithDial(ctx, relayruntime.ControlOrigin, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socketPath)
	})
}

// WaitUntilReady polls HTTP, DNS, and resolver health until every relay path is
// ready, the timeout elapses, or ctx is canceled.
func WaitUntilReady(ctx context.Context, timeout time.Duration) error {
	return waitUntilReady(ctx, timeout, DefaultProbes())
}

func waitUntilReady(ctx context.Context, timeout time.Duration, probes Probes) error {
	readyContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastError error
	for {
		attemptContext, cancelAttempt := context.WithTimeout(readyContext, 1500*time.Millisecond)
		result := Inspect(attemptContext, probes)
		cancelAttempt()
		if result.HTTPError == nil && result.DNSError == nil && result.ResolverError == nil {
			return nil
		}
		lastError = errors.Join(result.HTTPError, result.DNSError, result.ResolverError)
		select {
		case <-readyContext.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("localhost relay did not become ready within %s: %w", timeout, lastError)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Inspect runs the supplied relay health probes concurrently and returns when
// every probe completes or ctx is canceled.
func Inspect(ctx context.Context, probes Probes) Inspection {
	type probeResult struct {
		name string
		err  error
	}
	results := make(chan probeResult, 3)
	go func() { results <- probeResult{name: "http", err: probes.HTTP(ctx)} }()
	go func() { results <- probeResult{name: "dns", err: probes.DNS(ctx)} }()
	go func() { results <- probeResult{name: "resolver", err: probes.Resolver(ctx)} }()
	inspection := Inspection{}
	completed := make(map[string]bool, 3)
	for len(completed) < 3 {
		select {
		case result := <-results:
			completed[result.name] = true
			switch result.name {
			case "http":
				inspection.HTTPError = result.err
			case "dns":
				inspection.DNSError = result.err
			case "resolver":
				inspection.ResolverError = result.err
			}
		case <-ctx.Done():
			if !completed["http"] {
				inspection.HTTPError = ctx.Err()
			}
			if !completed["dns"] {
				inspection.DNSError = ctx.Err()
			}
			if !completed["resolver"] {
				inspection.ResolverError = ctx.Err()
			}
			return inspection
		}
	}
	return inspection
}

// CheckDNS verifies the privileged UDP DNS listener using the dynamic endpoint
// zone health record and a static localhost record.
func CheckDNS(ctx context.Context) error {
	if err := checkDNSRecord(ctx, networking.DNSZone, portlessdns.HealthAddress); err != nil {
		return err
	}
	return checkDNSRecord(ctx, localhostResolverProbe, portlessdns.LocalhostAddress)
}

func checkDNSRecord(ctx context.Context, name string, expected netip.Addr) error {
	return checkDNSRecordAt(ctx, relayruntime.DefaultDNSAddress, name, expected)
}

func checkDNSRecordAt(ctx context.Context, listenAddress, name string, expected netip.Addr) error {
	queryID := uint16(rand.Uint32())
	query, err := portlessdns.Query(name, portlessdns.TypeA, queryID)
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "udp", listenAddress)
	if err != nil {
		return fmt.Errorf("connect to Portless DNS at %s: %w", listenAddress, err)
	}
	defer connection.Close()
	stopClosing := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopClosing()
	deadline := time.Now().Add(time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	if _, err := connection.Write(query); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if contextDeadline, ok := ctx.Deadline(); ok && !time.Now().Before(contextDeadline) {
			return context.DeadlineExceeded
		}
		return err
	}
	response := make([]byte, portlessdns.MaxMessage)
	count, err := connection.Read(response)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if contextDeadline, ok := ctx.Deadline(); ok && !time.Now().Before(contextDeadline) {
			return context.DeadlineExceeded
		}
		return fmt.Errorf("read Portless DNS response: %w", err)
	}
	address, rcode, err := portlessdns.ParseAResponse(response[:count], queryID)
	if err != nil {
		return fmt.Errorf("parse Portless DNS response for %s: %w", name, err)
	}
	if rcode != portlessdns.ResponseSuccess || address != expected {
		return fmt.Errorf("Portless DNS returned code %d and address %s for %s", rcode, address, name)
	}
	return nil
}

// CheckResolver verifies the OS-level resolver routes used by normal
// applications for clean HTTP and TCP endpoint names. Directly reaching the
// DNS relay is insufficient if the host resolver never sends those queries to
// it or otherwise synthesizes the required loopback answers.
func CheckResolver(ctx context.Context) error {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, networking.DNSZone)
	if err != nil {
		return fmt.Errorf("resolve %s through the system resolver: %w", networking.DNSZone, err)
	}
	if err := validateResolverAddresses(networking.DNSZone, addresses, portlessdns.HealthAddress); err != nil {
		return err
	}
	addresses, err = net.DefaultResolver.LookupIPAddr(ctx, localhostResolverProbe)
	if err != nil {
		return fmt.Errorf("resolve %s through the system resolver: %w", localhostResolverProbe, err)
	}
	return validateLocalhostResolverAddresses(localhostResolverProbe, addresses)
}

func validateResolverAddresses(name string, addresses []net.IPAddr, expected netip.Addr) error {
	if len(addresses) == 0 {
		return fmt.Errorf("resolve %s through the system resolver: no addresses returned", name)
	}
	for _, address := range addresses {
		parsed, ok := netip.AddrFromSlice(address.IP)
		if !ok || parsed.Unmap() != expected {
			return fmt.Errorf("resolve %s through the system resolver: unexpected address %s", name, address.IP)
		}
	}
	return nil
}

func validateLocalhostResolverAddresses(name string, addresses []net.IPAddr) error {
	if len(addresses) == 0 {
		return fmt.Errorf("resolve %s through the system resolver: no addresses returned", name)
	}
	for _, address := range addresses {
		parsed, ok := netip.AddrFromSlice(address.IP)
		if !ok || !parsed.Unmap().IsLoopback() {
			return fmt.Errorf("resolve %s through the system resolver: unexpected non-loopback address %s", name, address.IP)
		}
	}
	return nil
}

func checkAt(ctx context.Context, address, origin string) error {
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	return checkWithDial(ctx, origin, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	})
}

func checkWithDial(ctx context.Context, origin string, dialContext func(context.Context, string, string) (net.Conn, error)) error {
	transport := &http.Transport{Proxy: nil, DialContext: dialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/v1/health", nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", origin, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", origin, response.Status)
	}
	var health struct {
		Ready      bool   `json:"ready"`
		APIVersion string `json:"apiVersion"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&health); err != nil {
		return fmt.Errorf("decode %s health response: %w", origin, err)
	}
	if !health.Ready || health.APIVersion == "" {
		return errors.New("Portless relay health response is incompatible")
	}
	return nil
}
