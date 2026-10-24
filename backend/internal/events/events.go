// Package events is the in-process pub/sub hub that feeds the SSE stream.
package events

import (
	"context"
	"encoding/json"
	"sync"
)

// Event is one SSE frame.
type Event struct {
	Type string
	Data any
}

// Hub fans events out to every subscriber. A subscriber that cannot keep up
// is disconnected; the client reconnects and refetches state.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[chan Event]struct{}{}} }

// Publish sends e to all subscribers without blocking.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// Emit is shorthand for Publish(Event{typ, data}).
func (h *Hub) Emit(typ string, data any) { h.Publish(Event{Type: typ, Data: data}) }

// Subscribe returns a channel that receives events until ctx ends or the
// subscriber falls behind. The channel is closed in both cases.
func (h *Hub) Subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, 512)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}()
	return ch
}

// CloseAll disconnects every subscriber (used on shutdown).
func (h *Hub) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}

// Frame renders e as an SSE frame.
func Frame(e Event) []byte {
	data, err := json.Marshal(e.Data)
	if err != nil {
		data = []byte("null")
	}
	b := make([]byte, 0, len(data)+len(e.Type)+16)
	b = append(b, "event: "...)
	b = append(b, e.Type...)
	b = append(b, "\ndata: "...)
	b = append(b, data...)
	b = append(b, "\n\n"...)
	return b
}

// FSChanged is the payload of fs.changed.
type FSChanged struct {
	Place string `json:"place"`
	Dir   string `json:"dir"`
}

// FS publishes fs.changed for a directory.
func (h *Hub) FS(place, dir string) { h.Emit("fs.changed", FSChanged{Place: place, Dir: dir}) }
