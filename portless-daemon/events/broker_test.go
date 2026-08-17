package events

import (
	"context"
	"testing"
	"time"
)

func TestBrokerPublishesOnlyMatchingScopedTopics(t *testing.T) {
	broker := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription := broker.Subscribe(ctx, "billing/local", []string{"service.state"})
	defer subscription.Close()
	broker.Publish(Event{Type: "service.state", Project: "orders", Environment: "local", Data: "other"})
	broker.Publish(Event{Type: "timeline.state", Project: "billing", Environment: "local", Data: "other topic"})
	broker.Publish(Event{Type: "service.state", Project: "billing", Environment: "local", Data: "ready"})
	select {
	case event := <-subscription.C:
		if event.Type != "service.state" || event.Project != "billing" || event.Environment != "local" || event.Data != "ready" {
			t.Fatalf("unexpected event %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("matching event was not published")
	}
}
