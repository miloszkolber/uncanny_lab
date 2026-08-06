package events

import (
	"encoding/json"
	"sync"
)

type Event struct {
	JobID string          `json:"job_id,omitempty"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type Broker struct {
	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
}

func NewBroker() *Broker { return &Broker{subscribers: make(map[chan Event]struct{})} }

func (b *Broker) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

func (b *Broker) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- event:
		default: // A slow browser can refresh current state through the jobs endpoint.
		}
	}
}
