package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/DerekCorniello/hunch/core/types"
	"github.com/DerekCorniello/hunch/ipc"
)

const testHalfLife = 720 * time.Hour

func TestRawOuterKeyIgnoresEmptyState(t *testing.T) {
	// The graph pads state slices to a fixed width with empty strings, so
	// these two spellings of the same context must collide.
	if a, b := rawOuterKey([]string{"", "git add PATH"}, "git commit"), rawOuterKey([]string{"git add PATH"}, "git commit"); a != b {
		t.Errorf("padded and unpadded state produced different keys:\n %q\n %q", a, b)
	}
	if a, b := rawOuterKey(nil, "ls"), rawOuterKey([]string{""}, "ls"); a != b {
		t.Errorf("nil and empty state produced different keys:\n %q\n %q", a, b)
	}
}

func TestRawOuterKeyDistinguishesContexts(t *testing.T) {
	same := rawOuterKey([]string{"git add PATH"}, "git commit")
	diffState := rawOuterKey([]string{"cargo build"}, "git commit")
	diffTemplate := rawOuterKey([]string{"git add PATH"}, "git push")

	if same == diffState {
		t.Error("different prior state produced the same key")
	}
	if same == diffTemplate {
		t.Error("different next template produced the same key")
	}
}

func TestSplitOuterKeyRoundTrips(t *testing.T) {
	tests := []struct {
		state    []string
		template string
	}{
		{nil, "ls"},
		{[]string{"git add PATH"}, "git commit FLAG STR"},
		{[]string{"cd PATH", "cargo build"}, "cargo run"},
	}
	for _, tt := range tests {
		state, template, ok := splitOuterKey(rawOuterKey(tt.state, tt.template))
		if !ok {
			t.Errorf("splitOuterKey rejected a key it produced for %v", tt.state)
			continue
		}
		if template != tt.template {
			t.Errorf("template = %q, want %q", template, tt.template)
		}
		if len(state) != len(tt.state) {
			t.Errorf("state = %v, want %v", state, tt.state)
		}
	}
}

func TestSplitOuterKeyRejectsMalformed(t *testing.T) {
	if _, _, ok := splitOuterKey("no delimiter here"); ok {
		t.Error("accepted a key with no delimiter")
	}
}

func TestRawStoreRecordAccumulates(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()
	state := []string{"git add PATH"}

	s.record(state, "git commit FLAG STR", `git commit -m "wip"`, now)
	s.record(state, "git commit FLAG STR", `git commit -m "wip"`, now)

	snap := s.snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 record, got %d", len(snap))
	}
	if snap[0].Count != 2 {
		t.Errorf("count = %d, want 2", snap[0].Count)
	}
	if snap[0].Raw != `git commit -m "wip"` {
		t.Errorf("raw = %q", snap[0].Raw)
	}
}

// A raw is only suggested back inside the context it was seen in, which is
// what makes "cd <the repo I just cloned>" work.
func TestRawStoreHydrateIsContextConditioned(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	s.record([]string{"git clone REPO"}, "cd STR", "cd hunch", now)
	s.record([]string{"cargo build"}, "cd STR", "cd target", now)

	for _, tt := range []struct {
		state []string
		want  string
	}{
		{[]string{"git clone REPO"}, "cd hunch"},
		{[]string{"cargo build"}, "cd target"},
	} {
		suggestions := []types.Suggestion{{Template: "cd STR"}}
		s.hydrate(suggestions, tt.state, "", nil, now)
		if suggestions[0].Raw != tt.want {
			t.Errorf("after %v: raw = %q, want %q", tt.state, suggestions[0].Raw, tt.want)
		}
	}
}

// With no exact context match, a shorter state window should still hydrate
// rather than leaving the suggestion bare.
func TestRawStoreHydrateFallsBackToShorterState(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()
	s.record(nil, "cargo run", "cargo run --release", now)

	suggestions := []types.Suggestion{{Template: "cargo run"}}
	s.hydrate(suggestions, []string{"never seen", "also unseen"}, "", nil, now)

	if suggestions[0].Raw != "cargo run --release" {
		t.Errorf("raw = %q, want the context-free fallback", suggestions[0].Raw)
	}
}

func TestRawStoreHydratePrefersPrefixMatch(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	// The non-matching raw is far more frequent, so only prefix preference
	// can pull the other one to the front.
	for i := 0; i < 20; i++ {
		s.record(nil, "git push", "git push origin main", now)
	}
	s.record(nil, "git push", "git push upstream main", now)

	suggestions := []types.Suggestion{{Template: "git push"}}
	s.hydrate(suggestions, nil, "git push up", nil, now)

	if suggestions[0].Raw != "git push upstream main" {
		t.Errorf("raw = %q, want the prefix match despite lower count", suggestions[0].Raw)
	}
}

func TestRawStoreHydrateFallsBackWhenNoPrefixMatches(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()
	s.record(nil, "git push", "git push origin main", now)

	suggestions := []types.Suggestion{{Template: "git push"}}
	s.hydrate(suggestions, nil, "totally different", nil, now)

	if suggestions[0].Raw != "git push origin main" {
		t.Errorf("raw = %q, want the overall best when no raw matches the prefix", suggestions[0].Raw)
	}
}

func TestRawStoreRecencyOutweighsStaleCount(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	// Old but frequent versus recent and rare. Ten half-lives of decay puts
	// the stale entry near zero.
	for i := 0; i < 50; i++ {
		s.record(nil, "make STR", "make legacy", now.Add(-10*testHalfLife))
	}
	s.record(nil, "make STR", "make current", now)

	suggestions := []types.Suggestion{{Template: "make STR"}}
	s.hydrate(suggestions, nil, "", nil, now)

	if suggestions[0].Raw != "make current" {
		t.Errorf("raw = %q, want the recent entry to beat the decayed one", suggestions[0].Raw)
	}
}

func TestRawStoreArgTokenBoost(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	for i := 0; i < 5; i++ {
		s.record(nil, "vim PATH", "vim main.go", now)
	}
	s.record(nil, "vim PATH", "vim handlers.go", now)

	suggestions := []types.Suggestion{{Template: "vim PATH"}}
	s.hydrate(suggestions, nil, "", []string{"handlers.go"}, now)

	if suggestions[0].Raw != "vim handlers.go" {
		t.Errorf("raw = %q, want the token-boosted entry", suggestions[0].Raw)
	}
}

func TestRawStoreMergeExamplesSumsCounts(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	s.mergeExamples([]ipc.RawExampleJSON{
		{State: []string{"git add PATH"}, Template: "git commit", Raw: "git commit -v", Count: 3},
		{State: []string{"git add PATH"}, Template: "git commit", Raw: "git commit -v", Count: 4},
		{Template: "", Raw: "skipped", Count: 9},
		{Template: "also skipped", Raw: "", Count: 9},
	}, now)

	snap := s.snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 record (entries missing template or raw are dropped), got %d", len(snap))
	}
	if snap[0].Count != 7 {
		t.Errorf("count = %d, want 7", snap[0].Count)
	}
}

func TestRawStoreMergeExamplesKeepsLatestTimestamp(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now().Truncate(time.Second)
	older := now.Add(-48 * time.Hour)

	s.mergeExamples([]ipc.RawExampleJSON{
		{Template: "ls", Raw: "ls -la", Count: 1, LastSeen: now.Unix()},
		{Template: "ls", Raw: "ls -la", Count: 1, LastSeen: older.Unix()},
	}, now)

	snap := s.snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 record, got %d", len(snap))
	}
	if !snap[0].LastSeen.Equal(now) {
		t.Errorf("lastSeen = %v, want the newer %v", snap[0].LastSeen, now)
	}
}

func TestRawStoreLoadRoundTripsSnapshot(t *testing.T) {
	src := newRawStore(testHalfLife)
	now := time.Now().Truncate(time.Second)
	src.record([]string{"cd PATH"}, "cargo build", "cargo build --release", now)
	src.record(nil, "ls", "ls -la", now)

	dst := newRawStore(testHalfLife)
	dst.load(src.snapshot())

	if got, want := len(dst.snapshot()), len(src.snapshot()); got != want {
		t.Fatalf("reloaded %d records, want %d", got, want)
	}

	suggestions := []types.Suggestion{{Template: "cargo build"}}
	dst.hydrate(suggestions, []string{"cd PATH"}, "", nil, now)
	if suggestions[0].Raw != "cargo build --release" {
		t.Errorf("context was lost across the round trip: raw = %q", suggestions[0].Raw)
	}
}

func TestRawStoreReset(t *testing.T) {
	s := newRawStore(testHalfLife)
	s.record(nil, "ls", "ls -la", time.Now())
	s.reset()

	if snap := s.snapshot(); len(snap) != 0 {
		t.Errorf("snapshot returned %d records after reset", len(snap))
	}
}

func TestRawStoreDropOrphaned(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()
	s.record(nil, "keep me", "keep me now", now)
	s.record([]string{"ctx"}, "drop me", "drop me now", now)
	s.record(nil, "drop me", "drop me too", now)

	s.dropOrphaned([]string{"drop me"})

	snap := s.snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 surviving record, got %d", len(snap))
	}
	if snap[0].Template != "keep me" {
		t.Errorf("survivor = %q, want %q", snap[0].Template, "keep me")
	}
}

func TestRawStoreDropOrphanedEmptyIsNoop(t *testing.T) {
	s := newRawStore(testHalfLife)
	s.record(nil, "ls", "ls -la", time.Now())
	s.dropOrphaned(nil)

	if len(s.snapshot()) != 1 {
		t.Error("dropOrphaned(nil) removed records")
	}
}

// Guards the lock discipline: snapshot and hydrate take the read lock while
// record and reset take the write lock. Run under -race.
func TestRawStoreConcurrentAccess(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.record(nil, "ls", "ls -la", now)
				s.snapshot()
				suggestions := []types.Suggestion{{Template: "ls"}}
				s.hydrate(suggestions, nil, "", nil, now)
			}
		}(i)
	}
	wg.Wait()
}

func TestCollectArgTokens(t *testing.T) {
	parents := []string{"git"}

	tests := []struct {
		name    string
		cmds    []string
		wantAny []string
		wantNot []string
	}{
		{
			name:    "extracts file arguments",
			cmds:    []string{"vim handlers.go"},
			wantAny: []string{"handlers.go"},
		},
		{
			name:    "skips tokens under three characters",
			cmds:    []string{"cd ab"},
			wantNot: []string{"ab"},
		},
		{
			name: "empty input yields nothing",
			cmds: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectArgTokens(tt.cmds, parents)
			for _, want := range tt.wantAny {
				if !contains(got, want) {
					t.Errorf("tokens %v missing %q", got, want)
				}
			}
			for _, unwanted := range tt.wantNot {
				if contains(got, unwanted) {
					t.Errorf("tokens %v should not contain %q", got, unwanted)
				}
			}
		})
	}
}

func TestCollectArgTokensDeduplicates(t *testing.T) {
	got := collectArgTokens([]string{"vim main.go", "cat main.go"}, []string{"git"})

	var n int
	for _, tok := range got {
		if tok == "main.go" {
			n++
		}
	}
	if n > 1 {
		t.Errorf("token repeated %d times in %v", n, got)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
