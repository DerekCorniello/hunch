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
		suggestions = s.hydrate(suggestions, tt.state, "", nil, now, 0)
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
	suggestions = s.hydrate(suggestions, []string{"never seen", "also unseen"}, "", nil, now, 0)

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
	suggestions = s.hydrate(suggestions, nil, "git push up", nil, now, 0)

	if suggestions[0].Raw != "git push upstream main" {
		t.Errorf("raw = %q, want the prefix match despite lower count", suggestions[0].Raw)
	}
}

func TestRawStoreHydrateFallsBackWhenNoPrefixMatches(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()
	s.record(nil, "git push", "git push origin main", now)

	suggestions := []types.Suggestion{{Template: "git push"}}
	suggestions = s.hydrate(suggestions, nil, "totally different", nil, now, 0)

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
	suggestions = s.hydrate(suggestions, nil, "", nil, now, 0)

	if suggestions[0].Raw != "make current" {
		t.Errorf("raw = %q, want the recent entry to beat the decayed one", suggestions[0].Raw)
	}
}

// The reported bug: "npm ls lodash" then "npm view" was suggesting an
// unrelated, more-frequent package instead of lodash, because hydrate could
// only recall a raw it had seen before - and "npm view lodash" had never
// been typed. synthesize should build it from the just-mentioned token
// instead, and it should outrank the stale favorite.
func TestRawStoreSynthesizesUnseenSingleSlotCommand(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	for i := 0; i < 20; i++ {
		s.record(nil, "npm view STR", "npm view react", now)
	}

	suggestions := []types.Suggestion{{Template: "npm view STR"}}
	suggestions = s.hydrate(suggestions, nil, "", []string{"lodash"}, now, 0)

	if suggestions[0].Raw != "npm view lodash" {
		t.Errorf("raw = %q, want the synthesized command using the just-mentioned package", suggestions[0].Raw)
	}
}

// Synthesis must also work with zero prior history for the template - not
// just when it's competing against an unrelated favorite.
func TestRawStoreSynthesizesWithNoPriorHistory(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	suggestions := []types.Suggestion{{Template: "npm view STR"}}
	suggestions = s.hydrate(suggestions, nil, "", []string{"lodash"}, now, 0)

	if suggestions[0].Raw != "npm view lodash" {
		t.Errorf("raw = %q, want synthesized \"npm view lodash\"", suggestions[0].Raw)
	}
}

// A template with more than one collapsed placeholder ("git commit FLAG
// STR") has no safe place to put a substituted value, so it must not be
// synthesized - unlike a real recorded example, a wrong guess here would be
// actively worse than staying silent.
func TestRawStoreDoesNotSynthesizeMultiSlotTemplates(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	suggestions := []types.Suggestion{{Template: "git commit FLAG STR"}}
	suggestions = s.hydrate(suggestions, nil, "", []string{"lodash"}, now, 0)

	if suggestions[0].Raw != "" {
		t.Errorf("raw = %q, want no hydration for a multi-slot template with no history", suggestions[0].Raw)
	}
}

func TestCollectArgTokensOrdersMostRecentFirst(t *testing.T) {
	got := collectArgTokens([]string{"vim old.go", "vim new.go"}, []string{"git"})
	if len(got) == 0 || got[0] != "new.go" {
		t.Errorf("tokens = %v, want the most recently mentioned (new.go) first, got %v", got, got)
	}
}

// A bucket with many one-off values and no clear favorite (a git commit
// message, say) should offer the shared literal lead-in instead of guessing
// a full past message.
func TestRawStoreDiffuseBucketOffersSharedPrefixStub(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	s.record(nil, "git commit FLAG STR", `git commit -m "fix bug"`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "add feature"`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "typo"`, now)

	suggestions := []types.Suggestion{{Template: "git commit FLAG STR"}}
	suggestions = s.hydrate(suggestions, nil, "", nil, now, 0)

	if want := `git commit -m "`; suggestions[0].Raw != want {
		t.Errorf("raw = %q, want the shared literal stub %q", suggestions[0].Raw, want)
	}
}

// A bucket with one clearly dominant value is a real habit, not a diffuse
// bucket - it should still hydrate fully rather than truncating to a stub.
func TestRawStoreConcentratedBucketStillHydratesFully(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	for i := 0; i < 20; i++ {
		s.record(nil, "npm view STR", "npm view lodash", now)
	}
	s.record(nil, "npm view STR", "npm view axios", now)
	s.record(nil, "npm view STR", "npm view chalk", now)

	suggestions := []types.Suggestion{{Template: "npm view STR"}}
	suggestions = s.hydrate(suggestions, nil, "", nil, now, 0)

	if suggestions[0].Raw != "npm view lodash" {
		t.Errorf("raw = %q, want the dominant value hydrated in full", suggestions[0].Raw)
	}
}

// Once the free-form part a stub left open has been typed and closed (the
// user's own closing quote appears), and the bucket's raws share a stable
// tail beyond that point, hydrate should pick back up with that tail rather
// than staying silent for the rest of the line.
func TestRawStoreResumesAfterFreeTextCloses(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	s.record(nil, "git commit FLAG STR", `git commit -m "fix bug" --no-verify`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "add feature" --no-verify`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "typo" --no-verify`, now)

	suggestions := []types.Suggestion{{Template: "git commit FLAG STR"}}
	suggestions = s.hydrate(suggestions, nil, `git commit -m "fix the bug" `, nil, now, 0)

	want := `git commit -m "fix the bug" --no-verify`
	if suggestions[0].Raw != want {
		t.Errorf("raw = %q, want %q", suggestions[0].Raw, want)
	}
}

// Without a trailing space, "resume" should still insert exactly one -
// neither gluing the flag onto the closing quote nor leaving two.
func TestRawStoreResumesAfterFreeTextClosesNoTrailingSpace(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	s.record(nil, "git commit FLAG STR", `git commit -m "fix bug" --no-verify`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "add feature" --no-verify`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "typo" --no-verify`, now)

	suggestions := []types.Suggestion{{Template: "git commit FLAG STR"}}
	suggestions = s.hydrate(suggestions, nil, `git commit -m "fix the bug"`, nil, now, 0)

	want := `git commit -m "fix the bug" --no-verify`
	if suggestions[0].Raw != want {
		t.Errorf("raw = %q, want %q", suggestions[0].Raw, want)
	}
}

// Still mid-quote (no closing quote typed yet) must not trigger resume -
// the free-form part isn't finished, so nothing should be appended inside
// it.
func TestRawStoreDoesNotResumeBeforeQuoteCloses(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	s.record(nil, "git commit FLAG STR", `git commit -m "fix bug" --no-verify`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "add feature" --no-verify`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "typo" --no-verify`, now)

	suggestions := []types.Suggestion{{Template: "git commit FLAG STR"}}
	suggestions = s.hydrate(suggestions, nil, `git commit -m "fix the`, nil, now, 0)

	if suggestions[0].Raw != "" {
		t.Errorf("raw = %q, want no suggestion while the quoted argument is still open", suggestions[0].Raw)
	}
}

// freeContentClosed's other delimiter branch: a single-quoted argument
// closes on a matching single quote, same as the double-quote case tested
// above via TestRawStoreResumesAfterFreeTextCloses.
func TestRawStoreResumeHandlesSingleQuotedFreeText(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	s.record(nil, "sh FLAG STR", `sh -c 'echo hi' 2>&1`, now)
	s.record(nil, "sh FLAG STR", `sh -c 'ls -la' 2>&1`, now)
	s.record(nil, "sh FLAG STR", `sh -c 'whoami' 2>&1`, now)

	suggestions := []types.Suggestion{{Template: "sh FLAG STR"}}
	suggestions = s.hydrate(suggestions, nil, `sh -c 'echo hello' `, nil, now, 0)

	want := `sh -c 'echo hello' 2>&1`
	if suggestions[0].Raw != want {
		t.Errorf("raw = %q, want %q", suggestions[0].Raw, want)
	}
}

func TestRawStoreDoesNotResumeBeforeSingleQuoteCloses(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	s.record(nil, "sh FLAG STR", `sh -c 'echo hi' 2>&1`, now)
	s.record(nil, "sh FLAG STR", `sh -c 'ls -la' 2>&1`, now)
	s.record(nil, "sh FLAG STR", `sh -c 'whoami' 2>&1`, now)

	suggestions := []types.Suggestion{{Template: "sh FLAG STR"}}
	suggestions = s.hydrate(suggestions, nil, `sh -c 'echo hello`, nil, now, 0)

	if suggestions[0].Raw != "" {
		t.Errorf("raw = %q, want no suggestion while the single-quoted argument is still open", suggestions[0].Raw)
	}
}

// resumeAfterBlank's own size guard, isolated from isDiffuse's - a bucket
// can look diffuse once a synthesized candidate is added to scored (see
// rankBucket) while still having too few *recorded* raws for a resume tail
// to be trustworthy.
func TestResumeAfterBlankRequiresMinimumBucketSize(t *testing.T) {
	s := newRawStore(testHalfLife)
	inner := map[string]rawEntry{
		`git commit -m "a"`: {count: 1},
		`git commit -m "b"`: {count: 1},
	}
	if _, ok := s.resumeAfterBlank(inner, `git commit -m "c" `); ok {
		t.Error("expected no resume with fewer than the minimum bucket size")
	}
}

func TestCommonPrefixNoSharedContent(t *testing.T) {
	if got := commonPrefix([]string{"abc", "xyz"}); got != "" {
		t.Errorf("commonPrefix = %q, want empty", got)
	}
}

func TestCommonPrefixEmptyInput(t *testing.T) {
	if got := commonPrefix(nil); got != "" {
		t.Errorf("commonPrefix(nil) = %q, want empty", got)
	}
}

func TestCommonSuffixNoSharedContent(t *testing.T) {
	if got := commonSuffix([]string{"abc", "xyz"}); got != "" {
		t.Errorf("commonSuffix = %q, want empty", got)
	}
}

func TestCommonSuffixEmptyInput(t *testing.T) {
	if got := commonSuffix(nil); got != "" {
		t.Errorf("commonSuffix(nil) = %q, want empty", got)
	}
}

func TestTrimPrefixToBoundaryNoBoundary(t *testing.T) {
	if got := trimPrefixToBoundary("nowhitespace"); got != "" {
		t.Errorf("trimPrefixToBoundary = %q, want empty (no safe boundary)", got)
	}
}

func TestTrimSuffixToBoundaryNoBoundary(t *testing.T) {
	if got := trimSuffixToBoundary("nowhitespace"); got != "" {
		t.Errorf("trimSuffixToBoundary = %q, want empty (no safe boundary)", got)
	}
}

// singleSlot's other two rejections, alongside the "more than one
// placeholder" case already covered by TestRawStoreDoesNotSynthesizeMultiSlotTemplates.
func TestSingleSlotRejectsFlagOnlyTemplate(t *testing.T) {
	if _, _, ok := singleSlot("ls FLAG"); ok {
		t.Error("expected a FLAG-only template to be rejected (no reusable value)")
	}
}

func TestSingleSlotRejectsTemplateWithNoPlaceholder(t *testing.T) {
	if _, _, ok := singleSlot("git status"); ok {
		t.Error("expected a template with no placeholder to be rejected")
	}
}

// A diffuse bucket with genuinely nothing in common beyond the shared
// prefix (no repeated trailing flag, no common tail at all) must resolve to
// silence, not a guess.
func TestRawStoreResumeAfterBlankNoSharedTail(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	s.record(nil, "git commit FLAG STR", `git commit -m "fix bug"`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "add feature"`, now)
	s.record(nil, "git commit FLAG STR", `git commit -m "typo"`, now)

	suggestions := []types.Suggestion{{Template: "git commit FLAG STR"}}
	suggestions = s.hydrate(suggestions, nil, `git commit -m "fix the bug" `, nil, now, 0)

	if suggestions[0].Raw != "" {
		t.Errorf("raw = %q, want no suggestion - nothing shared beyond the free-form part", suggestions[0].Raw)
	}
}

func TestFreeContentClosed(t *testing.T) {
	tests := []struct {
		name string
		stub string
		pfx  string
		want bool
	}{
		{"prefix diverges from stub entirely", `git commit -m "`, `totally different`, false},
		{"prefix exactly equals stub, nothing typed yet", `git commit -m "`, `git commit -m "`, false},
		{"quote closed", `git commit -m "`, `git commit -m "msg"`, true},
		{"quote still open", `git commit -m "`, `git commit -m "msg`, false},
		{"unquoted blank, one more word typed", `cp `, `cp foo.txt `, true},
		{"unquoted blank, still typing the word", `cp `, `cp foo`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := freeContentClosed(tt.stub, tt.pfx); got != tt.want {
				t.Errorf("freeContentClosed(%q, %q) = %v, want %v", tt.stub, tt.pfx, got, tt.want)
			}
		})
	}
}

// The prefix-matched candidate set can still exceed a caller-supplied limit;
// rankBucket must truncate it rather than returning every match.
func TestRawStoreTopCandidatesTruncatesToN(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	for i := 0; i < 50; i++ {
		s.record(nil, "git push", "git push origin main", now)
	}
	s.record(nil, "git push", "git push origin dev", now)
	s.record(nil, "git push", "git push origin release", now)
	s.record(nil, "git push", "git push origin staging", now)

	got := s.topCandidates(nil, "git push", "git push origin ", nil, now, 2)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (n should truncate the prefix-matched set)", len(got))
	}
}

// Same truncation, but with no prefix filter narrowing the set first.
func TestRawStoreTopCandidatesTruncatesWithoutPrefix(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	for i := 0; i < 50; i++ {
		s.record(nil, "ls FLAG", "ls -a", now)
	}
	s.record(nil, "ls FLAG", "ls -l", now)
	s.record(nil, "ls FLAG", "ls -h", now)
	s.record(nil, "ls FLAG", "ls -r", now)

	got := s.topCandidates(nil, "ls FLAG", "", nil, now, 2)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (n should truncate)", len(got))
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
	suggestions = s.hydrate(suggestions, nil, "", []string{"handlers.go"}, now, 0)

	if suggestions[0].Raw != "vim handlers.go" {
		t.Errorf("raw = %q, want the token-boosted entry", suggestions[0].Raw)
	}
}

// When the top suggestion's hydration is genuinely ambiguous - two
// directories close enough in score that neither clearly wins - hydrate
// should offer both as extra candidates without displacing other
// suggestions, so cycling can page between them. limit=0 (unlimited) here,
// so there's no headroom cap to worry about.
func TestRawStoreHydrateExpandsAmbiguousTop(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	for i := 0; i < 3; i++ {
		s.record(nil, "cd PATH", "cd repo-a", now)
	}
	for i := 0; i < 2; i++ {
		s.record(nil, "cd PATH", "cd repo-b", now)
	}
	s.record(nil, "ls", "ls -la", now)

	suggestions := []types.Suggestion{{Template: "cd PATH"}, {Template: "ls"}}
	got := s.hydrate(suggestions, nil, "", nil, now, 0)

	if len(got) != 3 {
		t.Fatalf("got %d suggestions, want 3 (2 hydrations of cd PATH + ls, unlimited)", len(got))
	}
	if got[0].Template != "cd PATH" || got[1].Template != "cd PATH" {
		t.Fatalf("expected the first two slots to hold the ambiguous top template, got %+v", got)
	}
	if got[0].Raw == got[1].Raw {
		t.Errorf("expected two distinct hydration candidates, got the same raw twice: %q", got[0].Raw)
	}
	raws := map[string]bool{got[0].Raw: true, got[1].Raw: true}
	if !raws["cd repo-a"] || !raws["cd repo-b"] {
		t.Errorf("expected both candidate directories represented, got raws %v", raws)
	}
	if got[2].Template != "ls" {
		t.Errorf("expected ls to survive unaffected as the third slot, got %+v", got[2])
	}
}

// A tight limit with no headroom should not expand - and, critically, should
// not drop the other suggestion either. Only unlimited or generous limits
// get the extra candidate.
func TestRawStoreHydrateNoExpansionWithoutHeadroom(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	for i := 0; i < 3; i++ {
		s.record(nil, "cd PATH", "cd repo-a", now)
	}
	for i := 0; i < 2; i++ {
		s.record(nil, "cd PATH", "cd repo-b", now)
	}
	s.record(nil, "ls", "ls -la", now)

	suggestions := []types.Suggestion{{Template: "cd PATH"}, {Template: "ls"}}
	got := s.hydrate(suggestions, nil, "", nil, now, 2) // limit == len(suggestions): no room

	if len(got) != 2 {
		t.Fatalf("got %d suggestions, want 2 (limit leaves no headroom to expand)", len(got))
	}
	if got[0].Template != "cd PATH" || got[1].Template != "ls" {
		t.Fatalf("expected both original suggestions preserved, got %+v", got)
	}
}

// This is the scenario that matters most in practice: only one template is
// known for a context (so there's nothing to "borrow" from), but the caller
// asked for more suggestions than that. The ambiguous top template should
// still be allowed to expand into the request's own headroom.
func TestRawStoreHydrateExpandsIntoRequestedLimitWhenOnlyOneTemplate(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	for i := 0; i < 3; i++ {
		s.record(nil, "cd PATH", "cd repo-a", now)
	}
	for i := 0; i < 2; i++ {
		s.record(nil, "cd PATH", "cd repo-b", now)
	}

	suggestions := []types.Suggestion{{Template: "cd PATH"}}
	got := s.hydrate(suggestions, nil, "", nil, now, 2) // asked for up to 2, only 1 template exists

	if len(got) != 2 {
		t.Fatalf("got %d suggestions, want 2 (expand into the caller's requested headroom)", len(got))
	}
	raws := map[string]bool{got[0].Raw: true, got[1].Raw: true}
	if !raws["cd repo-a"] || !raws["cd repo-b"] {
		t.Errorf("expected both candidate directories represented, got raws %v", raws)
	}

	// With no limit and only one template, still expand up to
	// maxHydrationCandidates rather than staying stuck at 1.
	suggestions = []types.Suggestion{{Template: "cd PATH"}}
	got = s.hydrate(suggestions, nil, "", nil, now, 0)
	if len(got) != 2 {
		t.Fatalf("unlimited: got %d suggestions, want 2", len(got))
	}
}

// When one hydration candidate clearly dominates, the top suggestion should
// not claim extra slots from the rest of the list.
func TestRawStoreHydrateNoExpansionWhenClearWinner(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	for i := 0; i < 10; i++ {
		s.record(nil, "cd PATH", "cd repo-a", now)
	}
	s.record(nil, "cd PATH", "cd repo-b", now)
	s.record(nil, "ls", "ls -la", now)

	suggestions := []types.Suggestion{{Template: "cd PATH"}, {Template: "ls"}}
	got := s.hydrate(suggestions, nil, "", nil, now, 0)

	if len(got) != 2 {
		t.Fatalf("got %d suggestions, want 2 (no expansion)", len(got))
	}
	if got[0].Template != "cd PATH" || got[0].Raw != "cd repo-a" {
		t.Errorf("top suggestion = %+v, want cd PATH -> cd repo-a", got[0])
	}
	if got[1].Template != "ls" {
		t.Errorf("second suggestion template = %q, want ls (should not be displaced)", got[1].Template)
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
	suggestions = dst.hydrate(suggestions, []string{"cd PATH"}, "", nil, now, 0)
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
				_ = s.hydrate(suggestions, nil, "", nil, now, 0)
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

// TestTokenBoostRecencyWeightsMostRecentHighest verifies that the token boost
// decays linearly with position: the most recently mentioned token gets the
// strongest signal, overriding established habits when context is relevant.
func TestTokenBoostRecencyWeightsMostRecentHighest(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	// Establish a strong habit: "cd /home/user/projects" run 50 times
	for i := 0; i < 50; i++ {
		s.record(nil, "cd STR", "cd /home/user/projects", now)
	}

	// Record a one-off: "cd /tmp/test"
	s.record(nil, "cd STR", "cd /tmp/test", now)

	// Now simulate: user just ran "mkdir /tmp/test" (mentions /tmp/test)
	// and types "cd". The arg token is /tmp/test (position 0).
	// Score both candidates
	habitEntry := s.m[rawOuterKey(nil, "cd STR")]["cd /home/user/projects"]
	recentEntry := s.m[rawOuterKey(nil, "cd STR")]["cd /tmp/test"]

	habitScore := s.score(habitEntry, "cd /home/user/projects", []string{"/tmp/test"}, now)
	recentScore := s.score(recentEntry, "cd /tmp/test", []string{"/tmp/test"}, now)

	// The recent entry should score higher because:
	// - It matches the token boost at position 0 (200 points)
	// - The habit entry doesn't match any token, so no boost
	// - Even with count=1 vs count=50, the 200-point boost should win
	if recentScore <= habitScore {
		t.Errorf("recent entry score %.1f should beat habit score %.1f; "+
			"recent=%.1f (count=1, token match), habit=%.1f (count=50, no token match)",
			recentScore, habitScore, recentScore, habitScore)
	}
}

// TestTokenBoostDecaysWithPosition verifies that older tokens get less boost.
func TestTokenBoostDecaysWithPosition(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	// Record a command
	s.record(nil, "npm view STR", "npm view lodash", now)

	entry := s.m[rawOuterKey(nil, "npm view STR")]["npm view lodash"]

	// Score with lodash at position 0 (most recent)
	scorePos0 := s.score(entry, "npm view lodash", []string{"lodash", "express", "react"}, now)

	// Score with lodash at position 2 (least recent)
	scorePos2 := s.score(entry, "npm view lodash", []string{"express", "react", "lodash"}, now)

	// Position 0 should get higher boost than position 2
	if scorePos0 <= scorePos2 {
		t.Errorf("position 0 score %.1f should be > position 2 score %.1f", scorePos0, scorePos2)
	}

	// Verify the boost difference is significant (not just rounding)
	boostDiff := scorePos0 - scorePos2
	if boostDiff < 50 { // At least 50 point difference
		t.Errorf("boost difference %.1f should be > 50 for meaningful recency weighting", boostDiff)
	}
}

// TestDiffuseDetectionNotSkewedByTokenBoost verifies that diffuse detection
// uses base scores (without token boost) so that one recently-mentioned
// candidate doesn't prevent diffuse detection from triggering.
func TestDiffuseDetectionNotSkewedByTokenBoost(t *testing.T) {
	s := newRawStore(testHalfLife)
	now := time.Now()

	// Record 5 different commit messages, each once
	messages := []string{
		`git commit -m "fix bug"`,
		`git commit -m "add feature"`,
		`git commit -m "typo"`,
		`git commit -m "update docs"`,
		`git commit -m "refactor code"`,
	}
	for _, msg := range messages {
		s.record(nil, "git commit FLAG STR", msg, now)
	}

	// Simulate: user just typed something that mentions "fix bug"
	// This would put "fix bug" in argTokens
	argTokens := []string{"fix bug"}

	// Get the bucket
	inner := s.m[rawOuterKey(nil, "git commit FLAG STR")]
	if len(inner) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(inner))
	}

	// Use rankBucket to get scored results (this is what actually runs)
	scored := s.rankBucket("git commit FLAG STR", inner, "", argTokens, now, 5)

	// If diffuse detection works correctly, we should get the stub
	// instead of a specific commit message
	if len(scored) != 1 {
		t.Fatalf("expected 1 result (the stub), got %d", len(scored))
	}

	// The result should be the shared prefix, not a specific commit message
	expectedStub := `git commit -m "`
	if scored[0].Raw != expectedStub {
		t.Errorf("expected stub %q, got %q", expectedStub, scored[0].Raw)
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
