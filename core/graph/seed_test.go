package graph

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The JSON names are the seed file format. A rename that is not matched on the
// reading side does not error: the field silently unmarshals to its zero
// value. For LastSeen that means decay deletes the whole seed on the next
// start, so the names are pinned here.
func TestTransitionJSONFieldNames(t *testing.T) {
	data, err := json.Marshal(Transition{
		State:    []string{"cd STR"},
		Next:     "make STR",
		Count:    3,
		LastSeen: time.Unix(1000000, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`"state"`, `"next"`, `"count"`, `"last_seen"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("marshaled transition is missing %s: %s", want, data)
		}
	}
}

func TestTransitionRoundTripsThroughJSON(t *testing.T) {
	want := Transition{
		State:    []string{"cd STR", "make STR"},
		Next:     "git status",
		Count:    7,
		LastSeen: time.Unix(1700000000, 0).UTC(),
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Transition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if !got.LastSeen.Equal(want.LastSeen) {
		t.Errorf("LastSeen = %v, want %v", got.LastSeen, want.LastSeen)
	}
	if got.Next != want.Next || got.Count != want.Count || len(got.State) != len(want.State) {
		t.Errorf("round trip changed the transition: %+v", got)
	}
}

func TestSeedValidateRejectsZeroLastSeen(t *testing.T) {
	// This is what a seed written with the wrong field name produces: every
	// other field parses, so nothing looks wrong until the data disappears.
	seed := Seed{
		Version: 1,
		Transitions: []Transition{
			{State: []string{"cd STR"}, Next: "make STR", Count: 50},
		},
	}

	err := seed.Validate()
	if err == nil {
		t.Fatal("expected an error for a transition with no last_seen")
	}
	if !strings.Contains(err.Error(), "last_seen") {
		t.Errorf("error should name the offending field, got %q", err)
	}
	if !strings.Contains(err.Error(), "make STR") {
		t.Errorf("error should name the offending transition, got %q", err)
	}
}

func TestSeedValidateRejectsEmpty(t *testing.T) {
	if err := (Seed{Version: 1}).Validate(); err == nil {
		t.Error("expected an error for a seed with no transitions")
	}
}

func TestSeedValidateAcceptsWellFormed(t *testing.T) {
	seed := Seed{
		Version: 1,
		Transitions: []Transition{
			{State: []string{"cd STR"}, Next: "make STR", Count: 50, LastSeen: time.Now()},
		},
	}
	if err := seed.Validate(); err != nil {
		t.Errorf("Validate rejected a well-formed seed: %v", err)
	}
}

// A seed whose timestamps did not parse used to import cleanly and then be
// pruned entirely on the next start, because decay reads a zero timestamp as
// two thousand years old.
func TestZeroLastSeenIsPrunedByDecay(t *testing.T) {
	g := New(2)
	if err := g.Merge([]Transition{
		{State: []string{"cd STR"}, Next: "make STR", Count: 50}, // no LastSeen
	}); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if g.Size() != 1 {
		t.Fatalf("size after merge = %d, want 1", g.Size())
	}

	res := g.Decay(time.Now(), 720*time.Hour)
	if len(res.Pruned) != 1 {
		t.Errorf("decay pruned %d transitions, want 1", len(res.Pruned))
	}
	if g.Size() != 0 {
		t.Errorf("size after decay = %d, want 0", g.Size())
	}
	// Which is exactly why Seed.Validate refuses to let this in.
}
