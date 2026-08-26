package mysql

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
)

func TestDecoderCapturesQueryAndTextResultSet(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	decoder.Observe(protocol.DirectionResponse, mysqlPacket(0, []byte{0x0a, '8', '.', '4', 0}), now)
	capabilities := make([]byte, 32)
	decoder.Observe(protocol.DirectionRequest, mysqlPacket(1, capabilities), now)
	decoder.Observe(protocol.DirectionResponse, mysqlPacket(2, []byte{0x00, 0, 0, 2, 0, 0, 0}), now)
	query := "SELECT state FROM deliveries WHERE id = 42"
	decoder.Observe(protocol.DirectionRequest, mysqlPacket(0, append([]byte{0x03}, []byte(query)...)), now.Add(time.Millisecond))

	response := mysqlPacket(1, []byte{0x01})
	response = append(response, mysqlPacket(2, mysqlColumn("state"))...)
	response = append(response, mysqlPacket(3, []byte{0xfe, 0, 0, 2, 0})...)
	response = append(response, mysqlPacket(4, append([]byte{7}, []byte("created")...))...)
	response = append(response, mysqlPacket(5, []byte{0xfe, 0, 0, 2, 0})...)
	operations := decoder.Observe(protocol.DirectionResponse, response, now.Add(6*time.Millisecond))
	if len(operations) != 1 || operations[0].Name != "SELECT" || operations[0].Outcome != model.TrafficTCPOutcomeSuccess {
		t.Fatalf("query operations = %#v", operations)
	}
	var row string
	for _, message := range operations[0].ResponseMessages {
		if message.Type == "row" {
			row = message.Content
		}
	}
	if !strings.Contains(operations[0].RequestMessages[0].Content, "deliveries WHERE id = 42") || !strings.Contains(row, "created") {
		t.Fatalf("query payload = %#v", operations[0])
	}
}

func TestDecoderClassifiesMySQLTLSHandshake(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	decoder.Observe(protocol.DirectionResponse, mysqlPacket(0, []byte{0x0a, '8', 0}), now)
	capabilities := make([]byte, 32)
	binary.LittleEndian.PutUint32(capabilities, clientSSL)
	decoder.Observe(protocol.DirectionRequest, mysqlPacket(1, capabilities), now)
	if state := decoder.State(); state.Inspection != model.TrafficInspectionEncrypted {
		t.Fatalf("TLS state = %#v", state)
	}
}

func TestDecoderMarksPartialMySQLPacketsMalformedOnClose(t *testing.T) {
	decoder := New(protocol.Config{})
	decoder.Observe(protocol.DirectionResponse, []byte{4, 0, 0, 0, 0x0a}, time.Now().UTC())
	decoder.Close(time.Now().UTC(), nil)
	if state := decoder.State(); state.Inspection != model.TrafficInspectionMalformed {
		t.Fatalf("partial packet state = %#v", state)
	}
}

func TestDecoderClassifiesOversizedMySQLPacketsAsLimited(t *testing.T) {
	decoder := New(protocol.Config{})
	decoder.Observe(protocol.DirectionRequest, []byte{1, 0, 16, 0}, time.Now().UTC())
	if state := decoder.State(); state.Inspection != model.TrafficInspectionLimited {
		t.Fatalf("oversized packet state = %#v", state)
	}
}

func TestDecoderDoesNotRetainChangeUserAuthenticationData(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	decoder.Observe(protocol.DirectionResponse, mysqlPacket(0, []byte{0x0a, '8', '.', '4', 0}), now)
	decoder.Observe(protocol.DirectionRequest, mysqlPacket(1, make([]byte, 32)), now)
	decoder.Observe(protocol.DirectionResponse, mysqlPacket(2, []byte{0x00, 0, 0, 2, 0, 0, 0}), now)
	payload := append([]byte("operator\x00"), []byte("credential-bytes")...)
	decoder.Observe(protocol.DirectionRequest, mysqlPacket(0, append([]byte{0x11}, payload...)), now)
	operations := decoder.Observe(protocol.DirectionResponse, mysqlPacket(1, []byte{0x00, 0, 0, 2, 0, 0, 0}), now)
	if len(operations) != 1 || strings.Contains(operations[0].RequestMessages[0].Content, "credential-bytes") || operations[0].RequestMessages[0].Fields[1].Value != "[REDACTED]" {
		t.Fatalf("change-user operation = %#v", operations)
	}
}

func mysqlPacket(sequence byte, payload []byte) []byte {
	result := make([]byte, 4+len(payload))
	length := len(payload)
	result[0], result[1], result[2], result[3] = byte(length), byte(length>>8), byte(length>>16), sequence
	copy(result[4:], payload)
	return result
}

func mysqlColumn(name string) []byte {
	values := []string{"def", "portless", "deliveries", "deliveries", name, name}
	var result []byte
	for _, value := range values {
		result = append(result, byte(len(value)))
		result = append(result, value...)
	}
	return result
}
