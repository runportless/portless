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

	portlessdns "github.com/portless-run/portless/portless-daemon/dns"
	"github.com/portless-run/portless/portless-daemon/networking"
)

// ControlOrigin is the clean control-plane origin used for relay health checks.
const ControlOrigin = "http://portless.localhost"

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

// CheckDNS verifies the privileged UDP DNS listener using the Portless health
// record.
func CheckDNS(ctx context.Context) error {
	queryID := uint16(rand.Uint32())
	query, err := portlessdns.Query(networking.DNSZone, portlessdns.TypeA, queryID)
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
		return err
	}
	if rcode != portlessdns.ResponseSuccess || address != portlessdns.HealthAddress {
		return fmt.Errorf("Portless DNS returned code %d and address %s", rcode, address)
	}
	return nil
}

// CheckResolver verifies the OS-level scoped resolver route used by normal
// applications. Directly reaching the DNS relay is insufficient if the host
// resolver never sends portless.test queries to it.
func CheckResolver(ctx context.Context) error {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, networking.DNSZone)
	if err != nil {
		return fmt.Errorf("resolve %s through the system resolver: %w", networking.DNSZone, err)
	}
	return validateResolverAddresses(addresses)
}

func validateResolverAddresses(addresses []net.IPAddr) error {
	if len(addresses) == 0 {
		return fmt.Errorf("resolve %s through the system resolver: no addresses returned", networking.DNSZone)
	}
	for _, address := range addresses {
		parsed, ok := netip.AddrFromSlice(address.IP)
		if !ok || parsed.Unmap() != portlessdns.HealthAddress {
			return fmt.Errorf("resolve %s through the system resolver: unexpected address %s", networking.DNSZone, address.IP)
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
