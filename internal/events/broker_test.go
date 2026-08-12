package events

import (
	"context"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func TestTrafficUsesProjectLocalSequencesAndTypedTopics(t *testing.T) {
	broker := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription := broker.Subscribe(ctx, "billing", []string{"traffic.http"})
	defer subscription.Close()
	first := broker.AddTraffic(model.TrafficEvent{Project: "billing", Protocol: model.ProtocolHTTP})
	other := broker.AddTraffic(model.TrafficEvent{Project: "orders", Protocol: model.ProtocolHTTP})
	if first.Sequence != 1 || other.Sequence != 1 {
		t.Fatalf("sequences should be project local: billing=%d orders=%d", first.Sequence, other.Sequence)
	}
	select {
	case event := <-subscription.C:
		if event.Type != "traffic.http" || event.Project != "billing" {
			t.Fatalf("unexpected event %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("traffic event was not published")
	}
}

func TestTrafficSequenceCanResumeAboveDurableHistory(t *testing.T) {
	broker := NewBroker()
	scope := model.EnvironmentSelector("billing", "local")
	broker.EnsureTrafficSequence(scope, 41)
	event := broker.AddTraffic(model.TrafficEvent{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP})
	if event.Sequence != 42 {
		t.Fatalf("sequence = %d, want 42", event.Sequence)
	}
	broker.EnsureTrafficSequence(scope, 10)
	event = broker.AddTraffic(model.TrafficEvent{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP})
	if event.Sequence != 43 {
		t.Fatalf("lower restoration moved sequence backwards: %d", event.Sequence)
	}
}
