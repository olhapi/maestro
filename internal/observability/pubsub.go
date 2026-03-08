package observability

import "sync"

type Broadcaster struct {
	mu   sync.RWMutex
	subs map[chan struct{}]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[chan struct{}]struct{}{}}
}

func (b *Broadcaster) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		close(ch)
		b.mu.Unlock()
	}
}

func (b *Broadcaster) BroadcastUpdate() {
	if b == nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

var defaultBroadcaster = NewBroadcaster()

func Subscribe() (<-chan struct{}, func()) {
	return defaultBroadcaster.Subscribe()
}

func BroadcastUpdate() {
	defaultBroadcaster.BroadcastUpdate()
}
