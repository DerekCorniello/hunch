package graph

import (
	"sort"
	"sync"
	"testing"
	"time"
)

func TestGraphRecordAndTransitions(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "git add PATH"}
	g.Record(state, "git commit FLAG STR", now)

	transitions := g.Transitions(state)
	if len(transitions) != 1 {
		t.Fatalf("Transitions returned %d, want 1", len(transitions))
	}
	if transitions[0].Next != "git commit FLAG STR" {
		t.Errorf("Next = %q, want %q", transitions[0].Next, "git commit FLAG STR")
	}
	if transitions[0].Count != 1 {
		t.Errorf("Count = %d, want 1", transitions[0].Count)
	}
	if !transitions[0].LastSeen.Equal(now) {
		t.Errorf("LastSeen = %v, want %v", transitions[0].LastSeen, now)
	}
	if len(transitions[0].State) != 2 {
		t.Errorf("State length = %d, want 2", len(transitions[0].State))
	}
}

func TestGraphMultipleRecordsIncrementCount(t *testing.T) {
	g := New(2)

	state := []string{"git add PATH", "git commit FLAG STR"}
	next := "git push STR"

	t1 := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 12, 1, 10, 5, 0, 0, time.UTC)
	t3 := time.Date(2025, 12, 1, 10, 10, 0, 0, time.UTC)

	g.Record(state, next, t1)
	g.Record(state, next, t2)
	g.Record(state, next, t3)

	transitions := g.Transitions(state)
	if len(transitions) != 1 {
		t.Fatalf("Transitions returned %d, want 1", len(transitions))
	}
	if transitions[0].Count != 3 {
		t.Errorf("Count = %d, want 3", transitions[0].Count)
	}
	if !transitions[0].LastSeen.Equal(t3) {
		t.Errorf("LastSeen = %v, want %v", transitions[0].LastSeen, t3)
	}
}

func TestGraphUnknownStateReturnsNil(t *testing.T) {
	g := New(2)
	transitions := g.Transitions([]string{"nonexistent", "state"})
	if transitions != nil {
		t.Errorf("Transitions for unknown state = %v, want nil", transitions)
	}
}

func TestGraphDifferentStatesIndependent(t *testing.T) {
	g := New(2)

	stateA := []string{"", "git add PATH"}
	stateB := []string{"git add PATH", "git commit FLAG STR"}
	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)

	g.Record(stateA, "git commit FLAG STR", now)
	g.Record(stateB, "git push STR", now)

	transitionsA := g.Transitions(stateA)
	if len(transitionsA) != 1 || transitionsA[0].Next != "git commit FLAG STR" {
		t.Errorf("State A should have 1 transition to git commit, got %v", transitionsA)
	}

	transitionsB := g.Transitions(stateB)
	if len(transitionsB) != 1 || transitionsB[0].Next != "git push STR" {
		t.Errorf("State B should have 1 transition to git push, got %v", transitionsB)
	}

	transitionsEmpty := g.Transitions([]string{"", ""})
	if transitionsEmpty != nil {
		t.Errorf("Unrecorded state should return nil, got %v", transitionsEmpty)
	}
}

func TestGraphMergeAddsNewTransitions(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	seed := []Transition{
		{State: []string{"", "git add PATH"}, Next: "git commit FLAG STR", Count: 3, LastSeen: now},
		{State: []string{"git add PATH", "git commit FLAG STR"}, Next: "git push STR", Count: 2, LastSeen: now},
	}

	if err := g.Merge(seed); err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}

	if g.Size() != 2 {
		t.Errorf("Size = %d, want 2", g.Size())
	}
}

func TestGraphMergeIncrementsExisting(t *testing.T) {
	g := New(2)

	state := []string{"", "git add PATH"}
	next := "git commit FLAG STR"

	t1 := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 12, 1, 12, 0, 0, 0, time.UTC)

	g.Record(state, next, t1)

	seed := []Transition{
		{State: state, Next: next, Count: 2, LastSeen: t2},
	}
	if err := g.Merge(seed); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	transitions := g.Transitions(state)
	if len(transitions) != 1 {
		t.Fatalf("Transitions returned %d, want 1", len(transitions))
	}
	if transitions[0].Count != 3 {
		t.Errorf("Count = %d, want 3 (1 existing + 2 seed)", transitions[0].Count)
	}
	if !transitions[0].LastSeen.Equal(t2) {
		t.Errorf("LastSeen = %v, want %v (newer should win)", transitions[0].LastSeen, t2)
	}
}

func TestGraphMergeTakesNewerLastSeen(t *testing.T) {
	g := New(2)

	state := []string{"git add PATH", "git commit FLAG STR"}
	next := "git push STR"

	older := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)

	g.Record(state, next, newer)

	seed := []Transition{
		{State: state, Next: next, Count: 1, LastSeen: older},
	}
	if err := g.Merge(seed); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	transitions := g.Transitions(state)
	if !transitions[0].LastSeen.Equal(newer) {
		t.Errorf("LastSeen = %v, want %v (newer should win)", transitions[0].LastSeen, newer)
	}
}

func TestGraphMergeWithOlderSeedLastSeen(t *testing.T) {
	g := New(2)

	state := []string{"npm test", "npm run STR"}
	next := "npm test"

	older := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	g.Record(state, next, older)

	seed := []Transition{
		{State: state, Next: next, Count: 1, LastSeen: newer},
	}
	if err := g.Merge(seed); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	transitions := g.Transitions(state)
	if !transitions[0].LastSeen.Equal(newer) {
		t.Errorf("LastSeen = %v, want %v (newer from seed should win)", transitions[0].LastSeen, newer)
	}
}

func TestGraphAllSorted(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)

	g.Record([]string{"b", "c"}, "z", now)
	g.Record([]string{"a", "b"}, "y", now)
	g.Record([]string{"a", "b"}, "x", now)

	all := g.All()

	if len(all) != 3 {
		t.Fatalf("All returned %d transitions, want 3", len(all))
	}

	ok := sort.SliceIsSorted(all, func(i, j int) bool {
		si := joinState(all[i].State)
		sj := joinState(all[j].State)
		if si != sj {
			return si < sj
		}
		return all[i].Next < all[j].Next
	})
	if !ok {
		t.Errorf("All is not sorted by (state, next)")
	}
}

func TestGraphSize(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	if g.Size() != 0 {
		t.Errorf("Size of empty graph = %d, want 0", g.Size())
	}

	g.Record([]string{"", "git add PATH"}, "git commit FLAG STR", now)
	if g.Size() != 1 {
		t.Errorf("Size after first record = %d, want 1", g.Size())
	}

	g.Record([]string{"", "git add PATH"}, "git commit FLAG STR", now)
	if g.Size() != 1 {
		t.Errorf("Size after duplicate should still be 1, got %d", g.Size())
	}

	g.Record([]string{"", "git add PATH"}, "git push STR", now)
	if g.Size() != 2 {
		t.Errorf("Size after second next = %d, want 2", g.Size())
	}

	g.Record([]string{"git add PATH", "git commit FLAG STR"}, "git push STR", now)
	if g.Size() != 3 {
		t.Errorf("Size after new state = %d, want 3", g.Size())
	}
}

func TestGraphDecayAutoTicks(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	g.Record([]string{"", "cmd"}, "next", now)

	before := g.Size()

	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	g.Decay(after, 720*time.Hour)

	// Recent entry should survive decay.
	if g.Size() != before {
		t.Errorf("Size after Decay = %d, want %d", g.Size(), before)
	}

	transitions := g.Transitions([]string{"", "cmd"})
	if len(transitions) != 1 {
		t.Fatalf("Transitions after Decay = %d, want 1", len(transitions))
	}
	if transitions[0].Count != 1 {
		t.Errorf("Count after Decay = %d, want 1", transitions[0].Count)
	}
}

func TestGraphConcurrentRecords(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}
	next := "next"

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Record(state, next, now)
		}()
	}
	wg.Wait()

	transitions := g.Transitions(state)
	if len(transitions) != 1 {
		t.Fatalf("Transitions returned %d, want 1", len(transitions))
	}
	if transitions[0].Count != 100 {
		t.Errorf("Count = %d, want 100", transitions[0].Count)
	}
}

func TestGraphConcurrentReadsAndWrites(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)

	for i := range 50 {
		state := []string{"", "cmd"}
		next := "next"
		g.Record(state, next, now)
		_ = i
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Transitions([]string{"", "cmd"})
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.All()
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Size()
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Record([]string{"", "cmd"}, "next", now)
		}()
	}
	wg.Wait()
}

func TestGraphAllEmpty(t *testing.T) {
	g := New(2)
	all := g.All()
	if all != nil {
		t.Errorf("All on empty graph should return nil, got %v", all)
	}
}

func TestGraphMergeAcceptsAnyStateLength(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	seed := []Transition{
		{State: []string{"single"}, Next: "cmd", Count: 1, LastSeen: now},
	}
	if err := g.Merge(seed); err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	if g.Size() != 1 {
		t.Errorf("Size after merging single-state transition = %d, want 1", g.Size())
	}

	transitions := g.Transitions([]string{"single"})
	if len(transitions) != 1 {
		t.Fatalf("Transitions returned %d, want 1", len(transitions))
	}
	if transitions[0].Count != 1 {
		t.Errorf("Count = %d, want 1", transitions[0].Count)
	}
}

func TestGraphRecordPreservesStateSlice(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "git add PATH"}
	g.Record(state, "git commit FLAG STR", now)

	transitions := g.Transitions(state)
	if len(transitions) != 1 {
		t.Fatalf("Transitions returned %d, want 1", len(transitions))
	}

	wantState := []string{"", "git add PATH"}
	for i, s := range transitions[0].State {
		if s != wantState[i] {
			t.Errorf("State[%d] = %q, want %q", i, s, wantState[i])
		}
	}
}

func TestGraphTransitionsReturnsCopy(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}
	g.Record(state, "next", now)

	transitions := g.Transitions(state)
	transitions[0].Count = 999

	transitions2 := g.Transitions(state)
	if transitions2[0].Count == 999 {
		t.Errorf("Modifying returned Transitions affected graph internals")
	}
}

func TestGraphMultipleStatesAndNexts(t *testing.T) {
	g := New(2)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)

	states := [][]string{
		{"", "git add PATH"},
		{"git add PATH", "git commit FLAG STR"},
		{"git commit FLAG STR", "git push STR"},
	}
	nexts := []string{
		"git commit FLAG STR",
		"git push STR",
		"gh pr STR FLAG STR",
	}

	for i := range states {
		g.Record(states[i], nexts[i], now)
		g.Record(states[i], nexts[i], now)
	}

	if g.Size() != 3 {
		t.Errorf("Size = %d, want 3", g.Size())
	}

	all := g.All()
	if len(all) != 3 {
		t.Fatalf("All returned %d, want 3", len(all))
	}
	for i, tr := range all {
		if tr.Count != 2 {
			t.Errorf("Transition %d count = %d, want 2", i, tr.Count)
		}
	}
}

func joinState(s []string) string {
	r := ""
	for i, p := range s {
		if i > 0 {
			r += "\x00"
		}
		r += p
	}
	return r
}

func TestMergeValidateEmptyState(t *testing.T) {
	g := New(2)
	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	seed := []Transition{
		{State: nil, Next: "cmd", Count: 1, LastSeen: now},
	}
	if err := g.Merge(seed); err == nil {
		t.Fatal("expected error for empty state")
	}
}

func TestMergeValidateEmptyNext(t *testing.T) {
	g := New(2)
	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	seed := []Transition{
		{State: []string{"a"}, Next: "", Count: 1, LastSeen: now},
	}
	if err := g.Merge(seed); err == nil {
		t.Fatal("expected error for empty next")
	}
}

func TestMergeValidateNonPositiveCount(t *testing.T) {
	g := New(2)
	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	seed := []Transition{
		{State: []string{"a"}, Next: "b", Count: 0, LastSeen: now},
	}
	if err := g.Merge(seed); err == nil {
		t.Fatal("expected error for zero count")
	}

	seed = []Transition{
		{State: []string{"a"}, Next: "b", Count: -1, LastSeen: now},
	}
	if err := g.Merge(seed); err == nil {
		t.Fatal("expected error for negative count")
	}
}
