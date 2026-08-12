package events

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/portless-run/portless/internal/model"
)

type Event struct {
	ID          int64     `json:"id"`
	Type        string    `json:"type"`
	Project     string    `json:"project,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Data        any       `json:"data"`
}

type Subscription struct {
	C      <-chan Event
	cancel func()
}

func (s Subscription) Close() { s.cancel() }

type subscriber struct {
	scope   string
	topics  map[string]struct{}
	channel chan Event
}

type Broker struct {
	mu               sync.RWMutex
	subscribers      map[int64]subscriber
	nextSubscriberID int64
	globalSequence   atomic.Int64
	trafficSequence  map[string]int64
	traffic          map[string][]model.TrafficEvent
	trafficLimit     int
}

func NewBroker() *Broker {
	return &Broker{
		subscribers:     make(map[int64]subscriber),
		trafficSequence: make(map[string]int64),
		traffic:         make(map[string][]model.TrafficEvent),
		trafficLimit:    1000,
	}
}

func (b *Broker) Publish(event Event) {
	if event.ID == 0 {
		event.ID = b.globalSequence.Add(1)
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, subscriber := range b.subscribers {
		if subscriber.scope != "" && subscriber.scope != eventScope(event.Project, event.Environment) {
			continue
		}
		if len(subscriber.topics) > 0 {
			if _, ok := subscriber.topics[event.Type]; !ok {
				continue
			}
		}
		select {
		case subscriber.channel <- event:
		default:
			// Slow clients resynchronize from snapshot endpoints. Proxying never blocks.
		}
	}
}

func (b *Broker) Subscribe(ctx context.Context, scope string, topics []string) Subscription {
	channel := make(chan Event, 128)
	topicSet := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		if topic != "" {
			topicSet[topic] = struct{}{}
		}
	}
	b.mu.Lock()
	b.nextSubscriberID++
	id := b.nextSubscriberID
	b.subscribers[id] = subscriber{scope: scope, topics: topicSet, channel: channel}
	b.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, id)
			close(channel)
			b.mu.Unlock()
		})
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return Subscription{C: channel, cancel: cancel}
}

func (b *Broker) AddTraffic(event model.TrafficEvent) model.TrafficEvent {
	scope := eventScope(event.Project, event.Environment)
	b.mu.Lock()
	b.trafficSequence[scope]++
	event.Sequence = b.trafficSequence[scope]
	items := append(b.traffic[scope], event)
	if len(items) > b.trafficLimit {
		copy(items, items[len(items)-b.trafficLimit:])
		items = items[:b.trafficLimit]
	}
	b.traffic[scope] = items
	b.mu.Unlock()
	topic := "traffic.tcp"
	if event.Protocol == model.ProtocolHTTP {
		topic = "traffic.http"
	}
	b.Publish(Event{Type: topic, Project: event.Project, Environment: event.Environment, Data: event})
	return event
}

// EnsureTrafficSequence restores the high-water mark of durable traffic when a
// daemon starts so a new live event cannot replace an event kept by a recording.
func (b *Broker) EnsureTrafficSequence(scope string, sequence int64) {
	b.mu.Lock()
	if b.trafficSequence[scope] < sequence {
		b.trafficSequence[scope] = sequence
	}
	b.mu.Unlock()
}

func (b *Broker) RecentTraffic(scope string, limit int) []model.TrafficEvent {
	b.mu.RLock()
	items := b.traffic[scope]
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	result := make([]model.TrafficEvent, 0, limit)
	for index := len(items) - 1; index >= len(items)-limit; index-- {
		result = append(result, items[index])
	}
	b.mu.RUnlock()
	return result
}

func eventScope(project, environment string) string {
	if environment == "" {
		return project
	}
	return model.EnvironmentSelector(project, environment)
}
