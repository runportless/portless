package nats

import (
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
)

func TestDecoderCapturesPublishAndDeliveryPayloads(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	request := []byte("PUB orders.created 14\r\n{\"order\":\"42\"}\r\n")
	var operations []protocol.Operation
	for _, part := range [][]byte{request[:10], request[10:24], request[24:]} {
		operations = append(operations, decoder.Observe(protocol.DirectionRequest, part, now)...)
	}
	if len(operations) != 1 || operations[0].Name != "PUB" || operations[0].Outcome != model.TrafficTCPOutcomeOneWay || !strings.Contains(operations[0].RequestMessages[0].Content, "order") {
		t.Fatalf("publish operation = %#v", operations)
	}
	delivery := decoder.Observe(protocol.DirectionResponse, []byte("MSG orders.created 1 14\r\n{\"order\":\"42\"}\r\n"), now.Add(time.Millisecond))
	if len(delivery) != 1 || delivery[0].Name != "MSG" || len(delivery[0].ResponseMessages) != 1 || delivery[0].ResponseMessages[0].ContentType != "application/json" {
		t.Fatalf("delivery operation = %#v", delivery)
	}
}

func TestDecoderSuppressesNATSHeartbeatNoise(t *testing.T) {
	decoder := New(protocol.Config{})
	if operations := decoder.Observe(protocol.DirectionResponse, []byte("PING\r\nPONG\r\n"), time.Now().UTC()); len(operations) != 0 {
		t.Fatalf("heartbeat operations = %#v", operations)
	}
}

func TestDecoderClassifiesOversizedNATSPayloadsAsLimited(t *testing.T) {
	decoder := New(protocol.Config{})
	decoder.Observe(protocol.DirectionRequest, []byte("PUB oversized 1048577\r\n"), time.Now().UTC())
	if state := decoder.State(); state.Inspection != model.TrafficInspectionLimited {
		t.Fatalf("oversized payload state = %#v", state)
	}
}
