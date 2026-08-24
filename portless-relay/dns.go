package relay

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	portlessdns "github.com/runportless/portless/portless-daemon/dns"
)

// ServeDNSStreamRelay forwards length-prefixed TCP DNS connections from
// listener to the daemon's private Unix socket until ctx is canceled.
func ServeDNSStreamRelay(ctx context.Context, listener net.Listener, targetSocket string, maxConnections int) error {
	if !filepath.IsAbs(targetSocket) {
		return errors.New("DNS target socket must be an absolute path")
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
			return fmt.Errorf("accept TCP DNS query: %w", err)
		}
		select {
		case semaphore <- struct{}{}:
			go func() {
				defer func() { <-semaphore }()
				relayDNSStream(connection, targetSocket)
			}()
		default:
			_ = connection.Close()
		}
	}
}

// ServeDNSPacketRelay forwards UDP DNS packets to the daemon's private Unix
// socket until ctx is canceled.
func ServeDNSPacketRelay(ctx context.Context, connection net.PacketConn, targetSocket string, maxConnections int) error {
	if !filepath.IsAbs(targetSocket) {
		return errors.New("DNS target socket must be an absolute path")
	}
	if maxConnections <= 0 {
		maxConnections = defaultConnections
	}
	go func() {
		<-ctx.Done()
		_ = connection.Close()
	}()
	semaphore := make(chan struct{}, maxConnections)
	for {
		buffer := make([]byte, portlessdns.MaxMessage)
		count, peer, err := connection.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("read UDP DNS query: %w", err)
		}
		query := append([]byte(nil), buffer[:count]...)
		select {
		case semaphore <- struct{}{}:
			go func() {
				defer func() { <-semaphore }()
				response := relayDNSQuery(targetSocket, query)
				_ = connection.SetWriteDeadline(time.Now().Add(time.Second))
				_, _ = connection.WriteTo(response, peer)
			}()
		default:
			response := portlessdns.ServerFailure(query)
			_ = connection.SetWriteDeadline(time.Now().Add(time.Second))
			_, _ = connection.WriteTo(response, peer)
		}
	}
}

func relayDNSStream(client net.Conn, targetSocket string) {
	defer client.Close()
	reader := bufio.NewReaderSize(client, portlessdns.MaxMessage+2)
	for {
		_ = client.SetDeadline(time.Now().Add(3 * time.Second))
		query, err := readDNSFrame(reader)
		if err != nil {
			return
		}
		if err := writeDNSFrame(client, relayDNSQuery(targetSocket, query)); err != nil {
			return
		}
	}
}

func relayDNSQuery(targetSocket string, query []byte) []byte {
	if response, handled := portlessdns.LocalhostResponse(query); handled {
		return response
	}
	response, err := queryPrivateDNS(targetSocket, query)
	if err != nil {
		return portlessdns.ServerFailure(query)
	}
	return response
}

func queryPrivateDNS(targetSocket string, query []byte) ([]byte, error) {
	connection, err := net.DialTimeout("unix", targetSocket, time.Second)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	if err := writeDNSFrame(connection, query); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(connection, portlessdns.MaxMessage+2)
	return readDNSFrame(reader)
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
	type result struct {
		response []byte
		err      error
	}
	finished := make(chan result, 1)
	go func() {
		response, queryErr := queryPrivateDNS(targetSocket, query)
		finished <- result{response: response, err: queryErr}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case outcome := <-finished:
		if outcome.err != nil {
			return outcome.err
		}
		address, code, parseErr := portlessdns.ParseAResponse(outcome.response, 0x5054)
		if parseErr != nil {
			return parseErr
		}
		if code != 0 || address != portlessdns.HealthAddress {
			return fmt.Errorf("private DNS returned code %d and address %s", code, address)
		}
		return nil
	}
}
