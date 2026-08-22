package events

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

// Event is a sequenced live control-plane notification.
type Event struct {
	ID          int64     `json:"id"`
	Type        string    `json:"type"`
	Project     string    `json:"project,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Data        any       `json:"data"`
}

// Subscription exposes an event channel and owns its broker registration.
type Subscription struct {
	C      <-chan Event
	cancel func()
}

// Close unregisters the subscription and closes its event channel.
func (s Subscription) Close() { s.cancel() }

type subscriber struct {
	scope   string
	topics  map[string]struct{}
	channel chan Event
}

// Broker distributes non-blocking live control-plane notifications.
type Broker struct {
	mu               sync.RWMutex
	subscribers      map[int64]subscriber
	nextSubscriberID int64
	globalSequence   atomic.Int64
}

// NewBroker constructs an empty event broker.
func NewBroker() *Broker {
	return &Broker{
		subscribers: make(map[int64]subscriber),
	}
}

// Publish assigns missing metadata and offers event to matching subscribers
// without blocking on slow consumers.
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

// Subscribe registers a bounded subscription filtered by environment scope and
// topics. Cancellation of ctx closes the subscription.
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

func eventScope(project, environment string) string {
	if environment == "" {
		return project
	}
	return model.EnvironmentSelector(project, environment)
}
