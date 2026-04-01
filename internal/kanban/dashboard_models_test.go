package kanban

import (
	"reflect"
	"testing"
)

func TestIssueStateCountsAndStateBuckets(t *testing.T) {
	counts := IssueStateCounts{}
	counts.AddCount(StateReady, 2)
	counts.AddCount(StateInProgress, 1)
	counts.AddCount(StateDone, 3)
	counts.AddCount(StateBacklog, 0)
	counts.AddCount(StateCancelled, -4)
	counts.AddCount(State("unknown"), 7)

	if got, want := counts, (IssueStateCounts{Ready: 2, InProgress: 1, Done: 3}); got != want {
		t.Fatalf("unexpected state counts: got %#v want %#v", got, want)
	}
	if got, want := counts.Total(), 6; got != want {
		t.Fatalf("counts.Total() = %d, want %d", got, want)
	}
	if got, want := counts.Active(), 3; got != want {
		t.Fatalf("counts.Active() = %d, want %d", got, want)
	}

	if got := BuildStateBuckets(map[string]int{}, []string{"ready"}, []string{"done"}); got != nil {
		t.Fatalf("BuildStateBuckets(empty) = %#v, want nil", got)
	}

	buckets := BuildStateBuckets(
		map[string]int{
			"done":    3,
			"ready":   2,
			"backlog": 1,
		},
		[]string{"ready"},
		[]string{"done"},
	)
	wantBuckets := []StateBucket{
		{State: "backlog", Count: 1},
		{State: "done", Count: 3, IsTerminal: true},
		{State: "ready", Count: 2, IsActive: true},
	}
	if !reflect.DeepEqual(buckets, wantBuckets) {
		t.Fatalf("BuildStateBuckets() = %#v, want %#v", buckets, wantBuckets)
	}

	total, active, terminal := AggregateStateBuckets(buckets)
	if total != 6 || active != 2 || terminal != 3 {
		t.Fatalf("AggregateStateBuckets() = (%d, %d, %d), want (6, 2, 3)", total, active, terminal)
	}
}
