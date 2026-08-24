package relay

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
)

// ControlOrigin is the clean control-plane origin used for relay health checks.
const ControlOrigin = "http://portless.localhost"

const localhostResolverProbe = "resolver.portless.localhost"

// Check verifies end-to-end HTTP access through the default privileged relay.
func Check(ctx context.Context) error {
	return checkAt(ctx, DefaultListenAddress, ControlOrigin)
}

// CheckSocket verifies the daemon's private ingress listener without using the
// privileged port-80 relay.
func CheckSocket(ctx context.Context, socketPath string) error {
	if !filepath.IsAbs(socketPath) {
		return errors.New("daemon ingress socket path must be absolute")
	}
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	return checkWithDial(ctx, ControlOrigin, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socketPath)
	})
}

// WaitUntilReady polls HTTP, DNS, and resolver health until every relay path is
// ready, the timeout elapses, or ctx is canceled.
func WaitUntilReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastError error
	for {
		httpErr := Check(ctx)
		dnsErr := CheckDNS(ctx)
		resolverErr := CheckResolver(ctx)
		if httpErr == nil && dnsErr == nil && resolverErr == nil {
			return nil
		}
		lastError = errors.Join(httpErr, dnsErr, resolverErr)
		if time.Now().After(deadline) {
			return fmt.Errorf("localhost relay did not become ready: %w", lastError)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
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
	queryID := uint16(rand.Uint32())
	query, err := portlessdns.Query(name, portlessdns.TypeA, queryID)
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	connection, err := dialer.DialContext(ctx, "udp", DefaultDNSAddress)
	if err != nil {
		return fmt.Errorf("connect to Portless DNS at %s: %w", DefaultDNSAddress, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := connection.Write(query); err != nil {
		return err
	}
	response := make([]byte, portlessdns.MaxMessage)
	count, err := connection.Read(response)
	if err != nil {
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
