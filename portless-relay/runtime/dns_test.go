package runtime

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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

func FuzzDNSFrameRoundTrip(f *testing.F) {
	query, _ := portlessdns.Query("postgres.local.store.portless.test", portlessdns.TypeA, 41)
	f.Add(query)
	f.Add(make([]byte, 12))
	f.Add([]byte("short"))
	f.Fuzz(func(t *testing.T, message []byte) {
		var framed bytes.Buffer
		err := writeDNSFrame(&framed, message)
		if len(message) < 12 || len(message) > portlessdns.MaxMessage {
			if err == nil {
				t.Fatalf("invalid DNS message length %d was framed", len(message))
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		actual, err := readDNSFrame(&framed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, message) {
			t.Fatalf("DNS frame round trip changed %d-byte message", len(message))
		}
	})
}

func FuzzReadDNSFrameRejectsMalformedInputWithoutPanicking(f *testing.F) {
	query, _ := portlessdns.Query("checkout.local.store.localhost", portlessdns.TypeA, 42)
	var valid bytes.Buffer
	if err := writeDNSFrame(&valid, query); err != nil {
		f.Fatal(err)
	}
	f.Add(valid.Bytes())
	f.Add([]byte{0, 12})
	f.Add([]byte{0xff, 0xff})
	f.Fuzz(func(t *testing.T, frame []byte) {
		message, err := readDNSFrame(bytes.NewReader(frame))
		if err == nil && (len(message) < 12 || len(message) > portlessdns.MaxMessage) {
			t.Fatalf("malformed frame produced %d-byte DNS message", len(message))
		}
	})
}

func FuzzRelayDNSFailureAndLocalhostResponsesStayBounded(f *testing.F) {
	localhost, _ := portlessdns.Query("checkout.local.store.localhost", portlessdns.TypeA, 43)
	dynamic, _ := portlessdns.Query("postgres.local.store.portless.test", portlessdns.TypeA, 44)
	f.Add(localhost)
	f.Add(dynamic)
	f.Add([]byte("malformed"))
	f.Fuzz(func(t *testing.T, query []byte) {
		response, handled := portlessdns.LocalhostResponse(query)
		if handled && len(response) > portlessdns.MaxMessage {
			t.Fatalf("localhost response exceeded DNS limit: %d", len(response))
		}
		failure := portlessdns.ServerFailure(query)
		if len(failure) > portlessdns.MaxMessage {
			t.Fatalf("failure response exceeded DNS limit: %d", len(failure))
		}
	})
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
	go func() { _ = serveDNSPacketRelay(ctx, packet, socket, 4) }()

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
	go func() { _ = serveDNSPacketRelay(ctx, packet, filepath.Join(t.TempDir(), "missing.sock"), 4) }()

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
	go func() { _ = serveDNSStreamRelay(ctx, listener, socket, 4) }()

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
	go func() { _ = serveDNSStreamRelay(ctx, listener, filepath.Join(t.TempDir(), "missing.sock"), 4) }()

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
	go func() { _ = serveDNSPacketRelay(ctx, packet, filepath.Join(t.TempDir(), "missing.sock"), 1) }()
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

func TestPrivateDNSCheckHonorsContextCancellation(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "portless-dns-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socketPath := filepath.Join(directory, "dns.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = CheckDNSSocket(ctx, socketPath)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("private DNS cancellation err=%v duration=%s", err, time.Since(started))
	}
	select {
	case connection := <-accepted:
		_ = connection.Close()
	default:
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
