package ipc

import (
	"testing"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
)

func TestTransitionFromGraphRoundTrip(t *testing.T) {
	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	tr := graph.Transition{
		State:    []string{"", "git add PATH"},
		Next:     "git commit STR",
		Count:    3,
		LastSeen: now,
	}

	json := TransitionFromGraph(tr)

	if len(json.State) != 2 {
		t.Errorf("State length = %d, want 2", len(json.State))
	}
	if json.State[0] != "" {
		t.Errorf("State[0] = %q, want %q", json.State[0], "")
	}
	if json.State[1] != "git add PATH" {
		t.Errorf("State[1] = %q, want %q", json.State[1], "git add PATH")
	}
	if json.Next != "git commit STR" {
		t.Errorf("Next = %q, want %q", json.Next, "git commit STR")
	}
	if json.Count != 3 {
		t.Errorf("Count = %d, want 3", json.Count)
	}
	if json.LastSeen != "2025-12-01T10:00:00Z" {
		t.Errorf("LastSeen = %q, want %q", json.LastSeen, "2025-12-01T10:00:00Z")
	}
}
