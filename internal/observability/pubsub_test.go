package observability

import (
	"testing"
	"time"
)

func TestBroadcasterSubscribeAndBroadcast(t *testing.T) {
	ch, unsubscribe := Subscribe()
	defer unsubscribe()

	BroadcastUpdate()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected broadcast to reach subscriber")
	}
}

func TestBroadcastUpdateNoSubscribersIsNoOp(t *testing.T) {
	BroadcastUpdate()
}
