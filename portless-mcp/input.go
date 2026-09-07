package portlessmcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"io"
)

const ordinaryInputLimit = 8 << 20
const replayInputLimit = 6*contract.TrafficReplayMaxBodyBytes + (1 << 20)

// messageReader validates bounded complete JSON-RPC objects before the SDK's
// decoder allocates its RawMessage and tool arguments. It recognizes JSON
// structure, so embedded newlines and pretty printing cannot reset the budget.
// Only an opted-in replay update can use the larger, worst-case escaped body
// envelope. No bytes from rejected frames reach the SDK or the tool handler.
type messageReader struct {
	source  *bufio.Reader
	replay  bool
	pending *bytes.Reader
	failed  error
}

func newMessageReader(source io.Reader, replay bool) *messageReader {
	return &messageReader{source: bufio.NewReaderSize(source, 64<<10), replay: replay}
}

// Read supplies one validated bounded JSON-RPC message at a time.
func (r *messageReader) Read(output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	if r.failed != nil {
		return 0, r.failed
	}
	if r.pending != nil && r.pending.Len() > 0 {
		return r.pending.Read(output)
	}
	frame, err := r.readFrame()
	if err != nil {
		r.failed = err
		return 0, err
	}
	r.pending = bytes.NewReader(frame)
	return r.pending.Read(output)
}
func (r *messageReader) readFrame() ([]byte, error) {
	limit := ordinaryInputLimit
	if r.replay {
		limit = replayInputLimit
	}
	frame := make([]byte, 0, 4096)
	depth := 0
	inString, escaped, started := false, false, false
	leading := 0
	for {
		b, err := r.source.ReadByte()
		if err != nil {
			if err == io.EOF && started {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if !started {
			if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
				leading++
				if leading > ordinaryInputLimit {
					return nil, errors.New("MCP input whitespace exceeds its limit")
				}
				continue
			}
			if b != '{' {
				return nil, errors.New("MCP input must be a JSON-RPC object")
			}
			started = true
		}
		if len(frame) >= limit {
			return nil, errors.New("MCP input exceeds its message limit")
		}
		frame = append(frame, b)
		if inString {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > 128 {
				return nil, errors.New("MCP input nesting exceeds its limit")
			}
		case '}', ']':
			depth--
		}
		if depth == 0 {
			break
		}
	}
	var envelope struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		return nil, errors.New("invalid MCP JSON-RPC input")
	}
	if len(frame) > ordinaryInputLimit && (!r.replay || envelope.Method != "tools/call" || envelope.Params.Name != "portless_update_replay") {
		return nil, errors.New("MCP tool input exceeds its ordinary message limit")
	}
	return append(frame, '\n'), nil
}
