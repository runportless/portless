package dns

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Serve accepts length-prefixed DNS queries on listener and serves them with
// resolver until ctx is canceled.
func Serve(ctx context.Context, listener net.Listener, resolver Resolver) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept private DNS connection: %w", err)
		}
		go serveConnection(ctx, connection, resolver)
	}
}

func serveConnection(ctx context.Context, connection net.Conn, resolver Resolver) {
	defer connection.Close()
	reader := bufio.NewReaderSize(connection, MaxMessage+2)
	for {
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		var header [2]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint16(header[:]))
		if length < 12 || length > MaxMessage {
			return
		}
		query := make([]byte, length)
		if _, err := io.ReadFull(reader, query); err != nil {
			return
		}
		response := Response(ctx, resolver, query)
		if len(response) > MaxMessage {
			return
		}
		binary.BigEndian.PutUint16(header[:], uint16(len(response)))
		if _, err := connection.Write(header[:]); err != nil {
			return
		}
		if _, err := connection.Write(response); err != nil {
			return
		}
	}
}
