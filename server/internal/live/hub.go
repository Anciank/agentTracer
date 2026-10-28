package live

import (
	"sync"
)

// RunEvent is emitted after every successful run create/patch (post-merge).
type RunEvent struct {
	TraceID string
	RunID   string
	Project string // session name when known; may be empty
	Op      string // "post" | "patch"
	Run     map[string]any
}

type subscriber struct {
	ch chan RunEvent
}

// Hub fans out run upserts to SSE listeners.
type Hub struct {
	mu   sync.RWMutex
	subs map[*subscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[*subscriber]struct{}{}}
}

func (h *Hub) Subscribe(buf int) (<-chan RunEvent, func()) {
	if buf <= 0 {
		buf = 64
	}
	s := &subscriber{ch: make(chan RunEvent, buf)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	unsub := func() {
		h.mu.Lock()
		if _, ok := h.subs[s]; ok {
			delete(h.subs, s)
			close(s.ch)
		}
		h.mu.Unlock()
	}
	return s.ch, unsub
}

func (h *Hub) Publish(ev RunEvent) {
	if h == nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		select {
		case s.ch <- ev:
		default:
			// drop if slow consumer; UI reconnects via Last-Event-ID / poll
		}
	}
}
