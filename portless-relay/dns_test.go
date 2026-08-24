package relay

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	portlessdns "github.com/runportless/portless/portless-daemon/dns"
)

type relayResolver struct{}

func (relayResolver) ResolveNetworkName(context.Context, string) (netip.Addr, bool, error) {
	return netip.MustParseAddr("127.77.2.3"), true, nil
}

func TestDNSPacketRelayForwardsToPrivateDaemonSocket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socket := startPrivateDNSServer(t, ctx)
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	go func() { _ = ServeDNSPacketRelay(ctx, packet, socket, 4) }()

	query, _ := portlessdns.Query("postgres.local.store.portless.test", portlessdns.TypeA, 31)
	connection, err := net.DialTimeout("udp", packet.LocalAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = connection.Write(query)
	buffer := make([]byte, portlessdns.MaxMessage)
	count, err := connection.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	address, code, err := portlessdns.ParseAResponse(buffer[:count], 31)
	if err != nil || code != 0 || address.String() != "127.77.2.3" {
		t.Fatalf("address=%s code=%d err=%v", address, code, err)
	}
}

func TestDNSPacketRelaySynthesizesLocalhostWithoutDaemon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	go func() { _ = ServeDNSPacketRelay(ctx, packet, filepath.Join(t.TempDir(), "missing.sock"), 4) }()

	query, _ := portlessdns.Query("checkout.local.store.localhost", portlessdns.TypeA, 34)
	connection, err := net.DialTimeout("udp", packet.LocalAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = connection.Write(query)
	buffer := make([]byte, portlessdns.MaxMessage)
	count, err := connection.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	address, code, err := portlessdns.ParseAResponse(buffer[:count], 34)
	if err != nil || code != 0 || address != portlessdns.LocalhostAddress {
		t.Fatalf("address=%s code=%d err=%v", address, code, err)
	}
}

func TestDNSStreamRelayPreservesTCPFraming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socket := startPrivateDNSServer(t, ctx)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = ServeDNSStreamRelay(ctx, listener, socket, 4) }()

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	query, _ := portlessdns.Query("redis.local.store.portless.test", portlessdns.TypeA, 32)
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(query)))
	_, _ = connection.Write(header[:])
	_, _ = connection.Write(query)
	if _, err := io.ReadFull(connection, header[:]); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, int(binary.BigEndian.Uint16(header[:])))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	address, _, err := portlessdns.ParseAResponse(response, 32)
	if err != nil || address.String() != "127.77.2.3" {
		t.Fatalf("address=%s err=%v", address, err)
	}
}

func TestDNSStreamRelaySynthesizesLocalhostWithoutDaemon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = ServeDNSStreamRelay(ctx, listener, filepath.Join(t.TempDir(), "missing.sock"), 4) }()

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	query, _ := portlessdns.Query("checkout.local.store.localhost", portlessdns.TypeA, 35)
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(query)))
	_, _ = connection.Write(header[:])
	_, _ = connection.Write(query)
	if _, err := io.ReadFull(connection, header[:]); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, int(binary.BigEndian.Uint16(header[:])))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	address, code, err := portlessdns.ParseAResponse(response, 35)
	if err != nil || code != 0 || address != portlessdns.LocalhostAddress {
		t.Fatalf("address=%s code=%d err=%v", address, code, err)
	}
}

func TestDNSPacketRelayReturnsServerFailureWithoutDaemon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	go func() { _ = ServeDNSPacketRelay(ctx, packet, filepath.Join(t.TempDir(), "missing.sock"), 1) }()
	query, _ := portlessdns.Query("postgres.local.store.portless.test", portlessdns.TypeA, 33)
	connection, _ := net.DialTimeout("udp", packet.LocalAddr().String(), time.Second)
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = connection.Write(query)
	buffer := make([]byte, portlessdns.MaxMessage)
	count, err := connection.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := portlessdns.ResponseCode(buffer[:count]); code != portlessdns.ResponseServerFailure {
		t.Fatalf("expected SERVFAIL, got %d", code)
	}
}

func startPrivateDNSServer(t *testing.T, ctx context.Context) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "portless-dns-relay-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "dns.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = portlessdns.Serve(ctx, listener, relayResolver{}) }()
	return socket
}
