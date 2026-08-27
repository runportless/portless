package postgres

import (
	"encoding/binary"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
)

func TestDecoderCompletesSimpleQueryAtReadyForQuery(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	decoder.Observe(protocol.DirectionRequest, startupPacket(196608), now)
	query := typedPacket('Q', append([]byte("SELECT id, state FROM orders WHERE id = 42"), 0))
	decoder.Observe(protocol.DirectionRequest, query, now.Add(time.Millisecond))
	response := append(typedPacket('C', append([]byte("SELECT 1"), 0)), typedPacket('Z', []byte{'I'})...)
	operations := decoder.Observe(protocol.DirectionResponse, response, now.Add(5*time.Millisecond))
	if len(operations) != 1 || operations[0].Name != "SELECT" || operations[0].Outcome != model.TrafficTCPOutcomeSuccess {
		t.Fatalf("query operations = %#v", operations)
	}
	if !strings.Contains(operations[0].RequestMessages[0].Content, "orders WHERE id = 42") || operations[0].ResponseMessages[len(operations[0].ResponseMessages)-1].Type != "ready" {
		t.Fatalf("query payload = %#v", operations[0])
	}
}

func TestDecoderClassifiesDriverAndPoolQueriesAsBackground(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		background bool
	}{
		{name: "empty validation", query: "", background: true},
		{name: "jdbc application name", query: "SET application_name = 'PostgreSQL JDBC Driver'", background: true},
		{name: "driver setting", query: "SET extra_float_digits = 3", background: true},
		{name: "isolation discovery", query: "SHOW TRANSACTION ISOLATION LEVEL", background: true},
		{name: "validation query", query: " SELECT 1; ", background: true},
		{name: "application select", query: "SELECT id FROM orders", background: false},
		{name: "application setting", query: "SET statement_timeout = 500", background: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := New(protocol.Config{})
			now := time.Now().UTC()
			decoder.Observe(protocol.DirectionRequest, startupPacket(196608), now)
			decoder.Observe(protocol.DirectionRequest, typedPacket('Q', append([]byte(test.query), 0)), now.Add(time.Millisecond))
			operations := decoder.Observe(protocol.DirectionResponse, typedPacket('Z', []byte{'I'}), now.Add(2*time.Millisecond))
			if len(operations) != 1 || operations[0].Background != test.background {
				t.Fatalf("query %q operation = %#v, want background=%v", test.query, operations, test.background)
			}
		})
	}
}

func TestDecoderClassifiesExtendedDriverQueryAsBackground(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	decoder.Observe(protocol.DirectionRequest, startupPacket(196608), now)
	parse := append([]byte{0}, []byte("SELECT 1")...)
	parse = append(parse, 0, 0, 0)
	decoder.Observe(protocol.DirectionRequest, append(typedPacket('P', parse), typedPacket('S', nil)...), now.Add(time.Millisecond))
	operations := decoder.Observe(protocol.DirectionResponse, typedPacket('Z', []byte{'I'}), now.Add(2*time.Millisecond))
	if len(operations) != 1 || operations[0].Name != "SELECT" || !operations[0].Background {
		t.Fatalf("extended validation operation = %#v", operations)
	}
}

func TestDecoderDecodesExtendedQueryBoundParameters(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	decoder.Observe(protocol.DirectionRequest, startupPacket(196608), now)

	query := "UPDATE inventory SET on_hand = on_hand - $1 WHERE sku = $2 AND on_hand >= $3"
	parse := append([]byte{0}, []byte(query)...)
	parse = append(parse, 0, 0, 3)
	for _, oid := range []uint32{23, 1043, 23} {
		encoded := make([]byte, 4)
		binary.BigEndian.PutUint32(encoded, oid)
		parse = append(parse, encoded...)
	}

	bind := []byte{0, 0, 0, 1, 0, 1, 0, 3}
	for _, value := range [][]byte{{0, 0, 0, 1}, []byte("coffee-mug"), {0, 0, 0, 2}} {
		length := make([]byte, 4)
		binary.BigEndian.PutUint32(length, uint32(len(value)))
		bind = append(bind, length...)
		bind = append(bind, value...)
	}
	bind = append(bind, 0, 0)

	request := append(typedPacket('P', parse), typedPacket('B', bind)...)
	request = append(request, typedPacket('S', nil)...)
	decoder.Observe(protocol.DirectionRequest, request, now.Add(time.Millisecond))
	operations := decoder.Observe(protocol.DirectionResponse, typedPacket('Z', []byte{'I'}), now.Add(2*time.Millisecond))
	if len(operations) != 1 || len(operations[0].RequestMessages) != 3 {
		t.Fatalf("extended query operations = %#v", operations)
	}
	var parameters []any
	if err := json.Unmarshal([]byte(operations[0].RequestMessages[1].Content), &parameters); err != nil {
		t.Fatalf("decode bind parameters: %v", err)
	}
	if expected := []any{float64(1), "coffee-mug", float64(2)}; !reflect.DeepEqual(parameters, expected) {
		t.Fatalf("bind parameters = %#v, want %#v", parameters, expected)
	}
}

func TestDecoderResolvesReusedPreparedCommit(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	decoder.Observe(protocol.DirectionRequest, startupPacket(196608), now)

	query := func(statement string, status byte, offset time.Duration) protocol.Operation {
		decoder.Observe(protocol.DirectionRequest, typedPacket('Q', append([]byte(statement), 0)), now.Add(offset))
		operations := decoder.Observe(protocol.DirectionResponse, typedPacket('Z', []byte{status}), now.Add(offset+time.Millisecond))
		if len(operations) != 1 {
			t.Fatalf("%s operations = %#v, want one", statement, operations)
		}
		return operations[0]
	}
	commitResponse := func(offset time.Duration, parsed bool) protocol.Operation {
		var response []byte
		if parsed {
			response = append(response, typedPacket('1', nil)...)
		}
		response = append(response, typedPacket('2', nil)...)
		response = append(response, typedPacket('C', append([]byte("COMMIT"), 0))...)
		response = append(response, typedPacket('Z', []byte{'I'})...)
		operations := decoder.Observe(protocol.DirectionResponse, response, now.Add(offset+time.Millisecond))
		if len(operations) != 1 {
			t.Fatalf("commit operations = %#v, want one", operations)
		}
		return operations[0]
	}

	query("BEGIN", 'T', time.Millisecond)
	parse := append([]byte("S_1\x00COMMIT\x00"), 0, 0)
	firstRequest := append(typedPacket('P', parse), preparedBindPacket("S_1")...)
	firstRequest = append(firstRequest, typedPacket('E', []byte{0, 0, 0, 0, 0})...)
	firstRequest = append(firstRequest, typedPacket('S', nil)...)
	decoder.Observe(protocol.DirectionRequest, firstRequest, now.Add(3*time.Millisecond))
	firstCommit := commitResponse(3*time.Millisecond, true)
	if firstCommit.Name != "COMMIT" || firstCommit.TransactionSequence != 1 {
		t.Fatalf("prepared commit = %#v, want named transaction 1 commit", firstCommit)
	}

	query("BEGIN", 'T', 5*time.Millisecond)
	reusedRequest := append(preparedBindPacket("S_1"), typedPacket('E', []byte{0, 0, 0, 0, 0})...)
	reusedRequest = append(reusedRequest, typedPacket('S', nil)...)
	decoder.Observe(protocol.DirectionRequest, reusedRequest, now.Add(7*time.Millisecond))
	reusedCommit := commitResponse(7*time.Millisecond, false)
	if reusedCommit.Name != "COMMIT" || reusedCommit.TransactionSequence != 2 {
		t.Fatalf("reused commit = %#v, want named transaction 2 commit", reusedCommit)
	}
}

func TestDecoderAssignsConnectionLocalTransactionSequences(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	decoder.Observe(protocol.DirectionRequest, startupPacket(196608), now)

	query := func(statement string, status byte, offset time.Duration) protocol.Operation {
		decoder.Observe(protocol.DirectionRequest, typedPacket('Q', append([]byte(statement), 0)), now.Add(offset))
		operations := decoder.Observe(protocol.DirectionResponse, typedPacket('Z', []byte{status}), now.Add(offset+time.Millisecond))
		if len(operations) != 1 {
			t.Fatalf("%s operations = %#v, want one", statement, operations)
		}
		return operations[0]
	}

	first := []protocol.Operation{
		query("BEGIN", 'T', time.Millisecond),
		query("UPDATE inventory SET on_hand = on_hand - 1", 'T', 3*time.Millisecond),
		query("COMMIT", 'I', 5*time.Millisecond),
	}
	for _, operation := range first {
		if operation.TransactionSequence != 1 {
			t.Fatalf("first transaction operation = %#v, want transaction 1", operation)
		}
	}
	if autocommit := query("SELECT 1", 'I', 7*time.Millisecond); autocommit.TransactionSequence != 0 {
		t.Fatalf("autocommit operation = %#v, want no transaction", autocommit)
	}
	if second := query("BEGIN", 'T', 9*time.Millisecond); second.TransactionSequence != 2 {
		t.Fatalf("second transaction operation = %#v, want transaction 2", second)
	}
}

func TestDecoderRecognizesPostgreSQLTLSUpgrade(t *testing.T) {
	decoder := New(protocol.Config{})
	decoder.Observe(protocol.DirectionRequest, startupPacket(sslRequestCode), time.Now().UTC())
	decoder.Observe(protocol.DirectionResponse, []byte{'S'}, time.Now().UTC())
	if state := decoder.State(); state.Inspection != model.TrafficInspectionEncrypted {
		t.Fatalf("TLS state = %#v", state)
	}
}

func TestDecoderMarksPartialPostgreSQLPacketsMalformedOnClose(t *testing.T) {
	decoder := New(protocol.Config{})
	decoder.Observe(protocol.DirectionRequest, []byte{0, 0, 0, 12, 0, 3}, time.Now().UTC())
	decoder.Close(time.Now().UTC(), nil)
	if state := decoder.State(); state.Inspection != model.TrafficInspectionMalformed {
		t.Fatalf("partial packet state = %#v", state)
	}
}

func TestDecoderClassifiesOversizedPostgreSQLPacketsAsLimited(t *testing.T) {
	decoder := New(protocol.Config{})
	packet := make([]byte, 5)
	packet[0] = 'Q'
	binary.BigEndian.PutUint32(packet[1:], uint32(protocol.MaximumPayloadLimit+5))
	decoder.Observe(protocol.DirectionRequest, startupPacket(196608), time.Now().UTC())
	decoder.Observe(protocol.DirectionRequest, packet, time.Now().UTC())
	if state := decoder.State(); state.Inspection != model.TrafficInspectionLimited {
		t.Fatalf("oversized packet state = %#v", state)
	}
}

func startupPacket(code uint32) []byte {
	result := make([]byte, 8)
	binary.BigEndian.PutUint32(result[:4], uint32(len(result)))
	binary.BigEndian.PutUint32(result[4:], code)
	return result
}

func typedPacket(kind byte, payload []byte) []byte {
	result := make([]byte, 5+len(payload))
	result[0] = kind
	binary.BigEndian.PutUint32(result[1:5], uint32(4+len(payload)))
	copy(result[5:], payload)
	return result
}

func preparedBindPacket(statement string) []byte {
	payload := append([]byte{0}, []byte(statement)...)
	payload = append(payload, 0, 0, 0, 0, 0, 0, 0)
	return typedPacket('B', payload)
}
