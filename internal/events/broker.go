package events

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/portless-run/portless/internal/model"
)

type Event struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	Project   string    `json:"project,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

type Subscription struct {
	C      <-chan Event
	cancel func()
}

func (s Subscription) Close() { s.cancel() }

type subscriber struct {
	project string
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
		if subscriber.project != "" && subscriber.project != event.Project {
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

func (b *Broker) Subscribe(ctx context.Context, project string, topics []string) Subscription {
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
	b.subscribers[id] = subscriber{project: project, topics: topicSet, channel: channel}
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
	b.mu.Lock()
	b.trafficSequence[event.Project]++
	event.Sequence = b.trafficSequence[event.Project]
	items := append(b.traffic[event.Project], event)
	if len(items) > b.trafficLimit {
		copy(items, items[len(items)-b.trafficLimit:])
		items = items[:b.trafficLimit]
	}
	b.traffic[event.Project] = items
	b.mu.Unlock()
	topic := "traffic.tcp"
	if event.Protocol == model.ProtocolHTTP {
		topic = "traffic.http"
	}
	b.Publish(Event{Type: topic, Project: event.Project, Data: event})
	return event
}

func (b *Broker) RecentTraffic(project string, limit int) []model.TrafficEvent {
	b.mu.RLock()
	items := b.traffic[project]
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
