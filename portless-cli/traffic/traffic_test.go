package traffic

import (
	"strings"
	"testing"

	"github.com/portless-run/portless/portless-daemon/model"
)

func TestTCPApplicationTrafficUsesProtocolSpecificHumanOutput(t *testing.T) {
	application, output, _ := newTestCommands(t)
	application.printTrafficList(model.Environment{Project: "billing", Name: "local"}, "tcp", []model.TrafficExchange{{
		Sequence: 9, Protocol: model.ProtocolTCP, Source: "checkout", Target: "postgres", DurationMS: 4,
	}})
	for _, expected := range []string{"TCP traffic", "PROTOCOL", "TCP", "checkout:postgres", "ok"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("TCP traffic output does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "METHOD") || strings.Contains(output.String(), "CODE") {
		t.Fatalf("TCP traffic used HTTP columns:\n%s", output.String())
	}
}

func TestTraceHumanOutputShowsTreeAndCorrelationConfidence(t *testing.T) {
	application, output, _ := newTestCommands(t)
	application.printTrace(model.TrafficTrace{
		Number: 8, LastSequence: 10, Method: "POST", RequestTarget: "/checkout?sku=mug", DurationMS: 42,
		Correlation: model.TrafficCorrelationInferred, SpanCount: 2,
		Spans: []model.TrafficTraceSpan{
			{Exchange: model.TrafficExchange{Source: "external", Target: "checkout", Protocol: model.ProtocolHTTP, Method: "POST", RequestTarget: "/checkout?sku=mug", DurationMS: 42}, Correlation: model.TrafficCorrelationExact},
			{Exchange: model.TrafficExchange{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Method: "POST", RequestTarget: "/orders", DurationMS: 18}, Depth: 1, Correlation: model.TrafficCorrelationInferred},
		},
	})
	for _, expected := range []string{"Traffic trace #8", "POST /checkout?sku=mug", "Correlation:", "inferred", "external → checkout", "  checkout → orders"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("trace output does not contain %q:\n%s", expected, output.String())
		}
	}
}
