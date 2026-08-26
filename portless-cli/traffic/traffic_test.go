package traffic

import (
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestTCPApplicationTrafficUsesProtocolSpecificHumanOutput(t *testing.T) {
	application, output, _ := newTestCommands(t)
	exchange := model.TrafficExchange{
		Sequence: 9, Protocol: model.ProtocolTCP, Source: "checkout", Target: "postgres", DurationMS: 4,
		TCP: &model.TrafficTCPExchange{
			Kind: model.TrafficTCPKindOperation, ApplicationProtocol: model.ApplicationProtocolPostgreSQL,
			Operation: "SELECT", Inspection: model.TrafficInspectionDecoded, Outcome: model.TrafficTCPOutcomeSuccess,
			RequestMessages:  []model.TrafficMessage{{Type: "query", Summary: "SELECT state FROM orders", Content: "SELECT state FROM orders", WireBytes: 29}},
			ResponseMessages: []model.TrafficMessage{{Type: "row", Summary: "Data row", Content: `{"state":"created"}`, ContentType: "application/json", WireBytes: 18}},
		},
	}
	application.printTrafficList(model.Environment{Project: "billing", Name: "local"}, "tcp", []model.TrafficExchange{exchange})
	application.printTrafficDetail(exchange)
	for _, expected := range []string{"TCP traffic", "PROTOCOL", "POSTGRESQL", "SELECT", "checkout:postgres", "ok", "Operation:", "Request messages", "SELECT state FROM orders", "Response messages", `{"state":"created"}`} {
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

func TestHTTPTrafficHumanOutputAttributesMockProfileAndRoute(t *testing.T) {
	application, output, _ := newTestCommands(t)
	event := model.TrafficExchange{
		Sequence: 4, Protocol: model.ProtocolHTTP, Source: "checkout", Target: "inventory",
		TargetProvider: model.ProviderMock, MockProfile: "sold-out", MockRoute: "lookup",
		Method: "GET", RequestTarget: "/inventory/coffee", Status: 200, DurationMS: 3,
	}

	application.printTraffic(event)
	application.printTrafficDetail(event)

	for _, expected := range []string{"mock=sold-out/lookup", "Provider:", "mock", "Mock profile:", "sold-out", "Mock route:", "lookup"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("mock traffic output does not contain %q:\n%s", expected, output.String())
		}
	}
}
