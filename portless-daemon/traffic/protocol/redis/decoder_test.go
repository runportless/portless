package redis

import (
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
)

func TestDecoderPairsFragmentedPipelineResponses(t *testing.T) {
	decoder := New(protocol.Config{})
	started := time.Now().UTC()
	request := []byte("*2\r\n$3\r\nGET\r\n$5\r\nfirst\r\n*2\r\n$3\r\nGET\r\n$6\r\nsecond\r\n")
	for _, part := range [][]byte{request[:7], request[7:21], request[21:]} {
		if operations := decoder.Observe(protocol.DirectionRequest, part, started); len(operations) != 0 {
			t.Fatalf("request unexpectedly completed operations: %#v", operations)
		}
	}
	response := []byte("$3\r\none\r\n$3\r\ntwo\r\n")
	operations := decoder.Observe(protocol.DirectionResponse, response, started.Add(4*time.Millisecond))
	if len(operations) != 2 || operations[0].Name != "GET" || operations[1].Name != "GET" {
		t.Fatalf("pipeline operations = %#v", operations)
	}
	if !strings.Contains(operations[0].RequestMessages[0].Content, "first") || !strings.Contains(operations[0].ResponseMessages[0].Content, "one") || operations[0].Outcome != model.TrafficTCPOutcomeSuccess {
		t.Fatalf("first operation = %#v", operations[0])
	}
}

func TestDecoderClassifiesClientAndPoolCommandsAsBackground(t *testing.T) {
	tests := []struct {
		name       string
		request    string
		background bool
	}{
		{name: "hello", request: "*2\r\n$5\r\nHELLO\r\n$1\r\n3\r\n", background: true},
		{name: "client metadata", request: "*4\r\n$6\r\nCLIENT\r\n$7\r\nSETINFO\r\n$8\r\nLIB-NAME\r\n$10\r\nnode-redis\r\n", background: true},
		{name: "validation", request: "*1\r\n$4\r\nPING\r\n", background: true},
		{name: "cache read", request: "*2\r\n$3\r\nGET\r\n$5\r\norder\r\n", background: false},
		{name: "client inspection", request: "*2\r\n$6\r\nCLIENT\r\n$2\r\nID\r\n", background: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := New(protocol.Config{})
			now := time.Now().UTC()
			decoder.Observe(protocol.DirectionRequest, []byte(test.request), now)
			operations := decoder.Observe(protocol.DirectionResponse, []byte("+OK\r\n"), now.Add(time.Millisecond))
			if len(operations) != 1 || operations[0].Background != test.background {
				t.Fatalf("request operation = %#v, want background=%v", operations, test.background)
			}
		})
	}
}

func TestDecoderRedactsOnlyRedisAuthenticationArguments(t *testing.T) {
	decoder := New(protocol.Config{})
	request := []byte("*2\r\n$4\r\nAUTH\r\n$12\r\nsecret-value\r\n")
	decoder.Observe(protocol.DirectionRequest, request, time.Now().UTC())
	operations := decoder.Observe(protocol.DirectionResponse, []byte("+OK\r\n"), time.Now().UTC())
	if len(operations) != 1 || strings.Contains(operations[0].RequestMessages[0].Content, "secret-value") || !strings.Contains(operations[0].RequestMessages[0].Content, "[REDACTED]") {
		t.Fatalf("AUTH operation = %#v", operations)
	}
}

func TestDecoderClassifiesImmediateTLS(t *testing.T) {
	decoder := New(protocol.Config{})
	decoder.Observe(protocol.DirectionRequest, []byte{0x16, 0x03, 0x03, 0x00}, time.Now().UTC())
	if state := decoder.State(); state.Inspection != model.TrafficInspectionEncrypted {
		t.Fatalf("TLS state = %#v", state)
	}
}

func TestDecoderWaitsForFragmentedInlineCommands(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	if operations := decoder.Observe(protocol.DirectionRequest, []byte("PI"), now); len(operations) != 0 {
		t.Fatalf("partial inline command emitted operations: %#v", operations)
	}
	if state := decoder.State(); state.Inspection != model.TrafficInspectionDecoded {
		t.Fatalf("partial inline command state = %#v", state)
	}
	decoder.Observe(protocol.DirectionRequest, []byte("NG\r\n"), now)
	operations := decoder.Observe(protocol.DirectionResponse, []byte("+PONG\r\n"), now.Add(time.Millisecond))
	if len(operations) != 1 || operations[0].Name != "PING" {
		t.Fatalf("inline command operation = %#v", operations)
	}
}

func TestDecoderMarksPartialFramesMalformedOnClose(t *testing.T) {
	decoder := New(protocol.Config{})
	decoder.Observe(protocol.DirectionRequest, []byte("*2\r\n$3\r\nGET\r\n$5\r\npar"), time.Now().UTC())
	decoder.Close(time.Now().UTC(), nil)
	if state := decoder.State(); state.Inspection != model.TrafficInspectionMalformed {
		t.Fatalf("partial frame state = %#v", state)
	}
}

func TestDecoderRejectsOversizedAggregateDeclarationsWithoutAllocatingThem(t *testing.T) {
	decoder := New(protocol.Config{})
	decoder.Observe(protocol.DirectionRequest, []byte("*999999999\r\n"), time.Now().UTC())
	if state := decoder.State(); state.Inspection != model.TrafficInspectionLimited {
		t.Fatalf("oversized aggregate state = %#v", state)
	}
}

func TestDecoderPairsRESP3SubscriptionAcknowledgementPush(t *testing.T) {
	decoder := New(protocol.Config{})
	now := time.Now().UTC()
	decoder.Observe(protocol.DirectionRequest, []byte("*2\r\n$9\r\nSUBSCRIBE\r\n$6\r\norders\r\n"), now)
	operations := decoder.Observe(protocol.DirectionResponse, []byte(">3\r\n+subscribe\r\n+orders\r\n:1\r\n"), now.Add(time.Millisecond))
	if len(operations) != 1 || operations[0].Name != "SUBSCRIBE" || operations[0].Outcome != model.TrafficTCPOutcomeSuccess {
		t.Fatalf("subscription acknowledgement = %#v", operations)
	}
}
