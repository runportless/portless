package runtime

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"

	portlessdns "github.com/runportless/portless/portless-daemon/dns"
)

func serveDNSStreamRelay(ctx context.Context, listener net.Listener, targetSocket string, maxConnections int) error {
	if !filepath.IsAbs(targetSocket) {
		return errors.New("DNS target socket must be an absolute path")
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
			return fmt.Errorf("accept TCP DNS query: %w", err)
		}
		select {
		case semaphore <- struct{}{}:
			connections.add(connection)
			go func() {
				defer func() { <-semaphore }()
				defer connections.done(connection)
				relayDNSStream(ctx, connection, targetSocket)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func serveDNSPacketRelay(ctx context.Context, connection net.PacketConn, targetSocket string, maxConnections int) error {
	if !filepath.IsAbs(targetSocket) {
		return errors.New("DNS target socket must be an absolute path")
	}
	if maxConnections <= 0 {
		maxConnections = defaultConnections
	}
	stopClosing := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopClosing()
	semaphore := make(chan struct{}, maxConnections)
	var workers sync.WaitGroup
	defer workers.Wait()
	for {
		buffer := make([]byte, portlessdns.MaxMessage)
		count, peer, err := connection.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read UDP DNS query: %w", err)
		}
		query := append([]byte(nil), buffer[:count]...)
		select {
		case semaphore <- struct{}{}:
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-semaphore }()
				response := relayDNSQuery(ctx, targetSocket, query)
				_, _ = connection.WriteTo(response, peer)
			}()
		default:
			response := portlessdns.ServerFailure(query)
			_, _ = connection.WriteTo(response, peer)
		}
	}
}

func relayDNSStream(ctx context.Context, client net.Conn, targetSocket string) {
	defer client.Close()
	stopClosing := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopClosing()
	reader := bufio.NewReaderSize(client, portlessdns.MaxMessage+2)
	for {
		_ = client.SetDeadline(time.Now().Add(3 * time.Second))
		query, err := readDNSFrame(reader)
		if err != nil {
			return
		}
		if err := writeDNSFrame(client, relayDNSQuery(ctx, targetSocket, query)); err != nil {
			return
		}
	}
}

func relayDNSQuery(ctx context.Context, targetSocket string, query []byte) []byte {
	if response, handled := portlessdns.LocalhostResponse(query); handled {
		return response
	}
	response, err := queryPrivateDNS(ctx, targetSocket, query)
	if err != nil {
		return portlessdns.ServerFailure(query)
	}
	return response
}

func queryPrivateDNS(ctx context.Context, targetSocket string, query []byte) ([]byte, error) {
	dialer := net.Dialer{Timeout: time.Second}
	connection, err := dialer.DialContext(ctx, "unix", targetSocket)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	stopClosing := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopClosing()
	deadline := time.Now().Add(2 * time.Second)
	contextDeadline, hasContextDeadline := ctx.Deadline()
	if hasContextDeadline && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	if err := writeDNSFrame(connection, query); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	reader := bufio.NewReaderSize(connection, portlessdns.MaxMessage+2)
	response, err := readDNSFrame(reader)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && hasContextDeadline && !time.Now().Before(contextDeadline) {
		return nil, context.DeadlineExceeded
	}
	return response, err
}

func readDNSFrame(reader io.Reader) ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint16(header[:]))
	if length < 12 || length > portlessdns.MaxMessage {
		return nil, errors.New("private DNS response has an invalid size")
	}
	response := make([]byte, length)
	if _, err := io.ReadFull(reader, response); err != nil {
		return nil, err
	}
	return response, nil
}

func writeDNSFrame(writer io.Writer, message []byte) error {
	if len(message) < 12 || len(message) > portlessdns.MaxMessage {
		return errors.New("DNS message has an invalid size")
	}
	frame := make([]byte, 2+len(message))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(message)))
	copy(frame[2:], message)
	for len(frame) > 0 {
		count, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrNoProgress
		}
		frame = frame[count:]
	}
	return nil
}

// CheckDNSSocket verifies the daemon's private authoritative DNS listener
// without involving the privileged UDP/TCP relay.
func CheckDNSSocket(ctx context.Context, targetSocket string) error {
	query, err := portlessdns.Query("portless.test", portlessdns.TypeA, 0x5054)
	if err != nil {
		return err
	}
	response, err := queryPrivateDNS(ctx, targetSocket, query)
	if err != nil {
		return err
	}
	address, code, parseErr := portlessdns.ParseAResponse(response, 0x5054)
	if parseErr != nil {
		return parseErr
	}
	if code != 0 || address != portlessdns.HealthAddress {
		return fmt.Errorf("private DNS returned code %d and address %s", code, address)
	}
	return nil
}
