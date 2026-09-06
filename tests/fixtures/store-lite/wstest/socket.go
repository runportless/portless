// Package wstest supplies a bounded, dependency-free WebSocket endpoint for the
// store-lite fixture. It is test infrastructure, not application proxy code.
package wstest

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Socket reads and writes complete, unfragmented frames in this fixture.
type Socket struct {
	net.Conn
	reader *bufio.Reader
	client bool
}

func accept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// Echo upgrades a fixture request and echoes text/binary data and close frames.
func Echo(w http.ResponseWriter, r *http.Request) {
	key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	if err != nil || len(key) != 16 || r.Header.Get("Sec-WebSocket-Version") != "13" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "WebSocket required", 400)
		return
	}
	c, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: portless-test\r\nX-Fixture: websocket\r\n\r\n", accept(r.Header.Get("Sec-WebSocket-Key")))
	if err != nil || rw.Flush() != nil {
		return
	}
	socket := &Socket{Conn: c, reader: rw.Reader}
	for {
		_ = c.SetReadDeadline(time.Now().Add(time.Minute))
		opcode, payload, err := socket.ReadFrame()
		if err != nil {
			return
		}
		if opcode == 9 {
			opcode = 10
		}
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := socket.WriteFrame(opcode, payload); err != nil {
			return
		}
		if opcode == 8 {
			return
		}
	}
}

// Dial opens a fixture WebSocket through its supplied HTTP dependency URL.
func Dial(ctx context.Context, endpoint string) (*Socket, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = c.Close()
		}
	}()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", key)
	request.Header.Set("Sec-WebSocket-Protocol", "portless-test")
	if err = request.Write(c); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(c)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Accept") != accept(key) {
		return nil, fmt.Errorf("WebSocket handshake returned %d", response.StatusCode)
	}
	ok = true
	return &Socket{Conn: c, reader: reader, client: true}, nil
}

// ReadFrame reads one bounded fixture frame, validating its masking direction.
func (s *Socket) ReadFrame() (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(s.reader, header); err != nil {
		return 0, nil, err
	}
	if header[0]&0xf0 != 0x80 {
		return 0, nil, errors.New("fixture requires an unfragmented, uncompressed frame")
	}
	masked := header[1]&0x80 != 0
	if masked == s.client {
		return 0, nil, errors.New("incorrect mask direction")
	}
	size := uint64(header[1] & 127)
	if size == 126 {
		var length [2]byte
		if _, err := io.ReadFull(s.reader, length[:]); err != nil {
			return 0, nil, err
		}
		size = uint64(binary.BigEndian.Uint16(length[:]))
	}
	if size == 127 {
		var length [8]byte
		if _, err := io.ReadFull(s.reader, length[:]); err != nil {
			return 0, nil, err
		}
		size = binary.BigEndian.Uint64(length[:])
	}
	if size > 1<<20 {
		return 0, nil, errors.New("fixture frame exceeds one MiB")
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(s.reader, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(s.reader, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return header[0] & 15, payload, nil
}

// WriteFrame writes one bounded fixture frame, masking client messages.
func (s *Socket) WriteFrame(opcode byte, payload []byte) error {
	if len(payload) > 1<<20 {
		return errors.New("fixture frame exceeds one MiB")
	}
	header := []byte{0x80 | opcode}
	flag := byte(0)
	if s.client {
		flag = 0x80
	}
	switch {
	case len(payload) < 126:
		header = append(header, flag|byte(len(payload)))
	case len(payload) < 65536:
		header = append(header, flag|126)
		header = binary.BigEndian.AppendUint16(header, uint16(len(payload)))
	default:
		header = append(header, flag|127)
		header = binary.BigEndian.AppendUint64(header, uint64(len(payload)))
	}
	if s.client {
		var mask [4]byte
		if _, err := rand.Read(mask[:]); err != nil {
			return err
		}
		header = append(header, mask[:]...)
		for i, value := range payload {
			header = append(header, value^mask[i%4])
		}
	} else {
		header = append(header, payload...)
	}
	_, err := s.Conn.Write(header)
	return err
}
