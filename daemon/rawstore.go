package daemon

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DerekCorniello/hunch/core/normalize"
	"github.com/DerekCorniello/hunch/core/types"
	"github.com/DerekCorniello/hunch/ipc"
)

// The graph stores normalized templates, which are privacy-safe but not
// runnable: "git commit FLAG STR" is not something a user can accept. rawStore
// keeps the concrete commands that produced each template so a suggestion can
// be shown verbatim.
//
// Raws are keyed by the workflow context they were seen in, not by template
// alone, so `cd` after `git clone` suggests the directory just cloned rather
// than whichever directory is globally most popular.
type rawStore struct {
	mu sync.RWMutex
	// m maps outerKey -> raw command -> entry, where outerKey combines the
	// prior-command templates with the next-command template.
	m        map[string]map[string]rawEntry
	halfLife time.Duration
}

// rawEntry tracks the accumulated count and most recent observation time
// for one (stateKey, template, raw) triple.
type rawEntry struct {
	count    int
	lastSeen time.Time
}

func newRawStore(halfLife time.Duration) *rawStore {
	return &rawStore{
		m:        make(map[string]map[string]rawEntry),
		halfLife: halfLife,
	}
}

// keySeparator joins the state templates; keyDelimiter separates the joined
// state from the next-command template. Both are NUL-based because normalized
// templates contain only alphanumeric tokens and spaces, so neither can appear
// inside a key component.
const (
	keySeparator = "\x00"
	keyDelimiter = "\x00\x00"
)

// rawOuterKey builds the map key from a prior-command state slice and the
// next-command template. Empty strings in state are ignored so that
// `["", "git add PATH"]` and `["git add PATH"]` produce the same key.
func rawOuterKey(state []string, template string) string {
	var nonEmpty []string
	for _, s := range state {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	return strings.Join(nonEmpty, keySeparator) + keyDelimiter + template
}

// splitOuterKey reverses rawOuterKey. The bool reports whether the key was
// well-formed.
func splitOuterKey(outerKey string) (state []string, template string, ok bool) {
	parts := strings.SplitN(outerKey, keyDelimiter, 2)
	if len(parts) != 2 {
		return nil, "", false
	}
	if parts[0] != "" {
		state = strings.Split(parts[0], keySeparator)
	}
	return state, parts[1], true
}

// bucket returns the inner map for outerKey, creating it when absent. The
// caller must hold the write lock.
func (s *rawStore) bucket(outerKey string) map[string]rawEntry {
	inner, ok := s.m[outerKey]
	if !ok {
		inner = make(map[string]rawEntry)
		s.m[outerKey] = inner
	}
	return inner
}

// record notes one observation of raw producing template in the given state.
func (s *rawStore) record(state []string, template, raw string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inner := s.bucket(rawOuterKey(state, template))
	entry := inner[raw]
	entry.count++
	entry.lastSeen = at
	inner[raw] = entry
}

// mergeExamples folds imported examples into the store, summing counts and
// keeping the most recent timestamp. Examples missing a template or raw are
// skipped.
func (s *rawStore) mergeExamples(examples []ipc.RawExampleJSON, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ex := range examples {
		if ex.Template == "" || ex.Raw == "" {
			continue
		}
		lastSeen := now
		if ex.LastSeen > 0 {
			lastSeen = time.Unix(ex.LastSeen, 0)
		}

		inner := s.bucket(rawOuterKey(ex.State, ex.Template))
		entry := inner[ex.Raw]
		entry.count += ex.Count
		if lastSeen.After(entry.lastSeen) {
			entry.lastSeen = lastSeen
		}
		inner[ex.Raw] = entry
	}
}

// load replaces the store's contents with records read from the database.
func (s *rawStore) load(records []rawRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, rec := range records {
		s.bucket(rawOuterKey(rec.State, rec.Template))[rec.Raw] = rawEntry{
			count:    rec.Count,
			lastSeen: rec.LastSeen,
		}
	}
}

// reset drops every stored raw.
func (s *rawStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = make(map[string]map[string]rawEntry)
}

// dropOrphaned removes every raw-store bucket whose next-command template
// is in the given set of orphaned templates (templates that no longer exist
// in the graph), so decayed templates do not leak raws indefinitely.
func (s *rawStore) dropOrphaned(orphanedTemplates []string) {
	if len(orphanedTemplates) == 0 {
		return
	}
	orphaned := make(map[string]struct{}, len(orphanedTemplates))
	for _, tmpl := range orphanedTemplates {
		orphaned[tmpl] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for outerKey := range s.m {
		_, template, ok := splitOuterKey(outerKey)
		if !ok {
			continue
		}
		if _, isOrphan := orphaned[template]; isOrphan {
			delete(s.m, outerKey)
		}
	}
}

// snapshot returns a flat copy of the store, safe to iterate without further
// synchronization.
func (s *rawStore) snapshot() []rawRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var records []rawRecord
	for outerKey, inner := range s.m {
		state, template, ok := splitOuterKey(outerKey)
		if !ok {
			continue
		}
		for raw, entry := range inner {
			records = append(records, rawRecord{
				State:    state,
				Template: template,
				Raw:      raw,
				Count:    entry.count,
				LastSeen: entry.lastSeen,
			})
		}
	}
	return records
}

// hydrationTieThreshold is how close a runner-up hydration's score must be to
// the best one (as a fraction of it) to count as genuinely ambiguous rather
// than a clear win. Only then does the top suggestion give up extra slots to
// alternate literal candidates instead of every suggestion getting exactly
// one hydration.
const hydrationTieThreshold = 0.5

// maxHydrationCandidates caps how many literal alternatives the top
// suggestion can claim, even if more are tied.
const maxHydrationCandidates = 3

// hydrate fills in the Raw field of each suggestion with its best matching
// concrete command, resolved under one read lock so every suggestion sees the
// same snapshot.
//
// The top-ranked suggestion is special-cased: if its best and next-best raw
// candidates are genuinely close (see hydrationTieThreshold) - e.g. two
// recently-used directories are both plausible for "cd PATH" - it claims a
// few extra slots for those alternates instead of collapsing to one guess.
// This lets ghost-text cycling page through literal candidates, not just
// different command shapes, with no change to the cycling mechanism itself:
// it already treats the result as a flat ranked list.
//
// limit is the caller's original requested cap (0 = unlimited, matching the
// predict op's convention) and bounds how far this may grow the slice - the
// extra candidates fill unused headroom under that limit rather than
// displacing other suggestions. When there is no headroom (the caller asked
// for exactly as many suggestions as were found), the top suggestion gets
// its single best hydration, same as before this existed. Returns a new
// slice; callers must use the return value rather than relying on in-place
// mutation, and truncate to limit themselves if they need the caller's
// original bound also applied to the non-expanded case.
func (s *rawStore) hydrate(suggestions []types.Suggestion, stateTemplates []string, prefix string, argTokens []string, at time.Time, limit int) []types.Suggestion {
	if len(suggestions) == 0 {
		return suggestions
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	top := suggestions[0]
	ranked := s.topNLocked(stateTemplates, top.Template, prefix, argTokens, at, maxHydrationCandidates)

	// Extra slots the top suggestion may add (not displace): consecutive
	// runner-ups within hydrationTieThreshold of the best, capped by how much
	// room is left under limit.
	headroom := maxHydrationCandidates - 1
	if limit > 0 {
		if room := limit - len(suggestions); room < headroom {
			headroom = room
		}
	}
	extra := 0
	for extra < headroom && extra+1 < len(ranked) &&
		ranked[extra+1].Score >= ranked[0].Score*hydrationTieThreshold {
		extra++
	}

	out := make([]types.Suggestion, 0, len(suggestions)+extra)
	if len(ranked) == 0 {
		out = append(out, top)
	} else {
		for i := 0; i <= extra; i++ {
			sug := top
			sug.Raw = ranked[i].Raw
			out = append(out, sug)
		}
	}

	for _, sug := range suggestions[1:] {
		if raw := s.bestLocked(stateTemplates, sug.Template, prefix, argTokens, at); raw != "" {
			sug.Raw = raw
		}
		out = append(out, sug)
	}

	return out
}

// topCandidates returns up to n ranked raw hydration candidates for a
// template in the given context. Unlike hydrate (which picks what to show as
// ghost text), this is for callers - namely the explain op behind `hunch
// why` - that want to show hydration confidence itself: which literal
// commands were considered and how they scored, not just the winner.
func (s *rawStore) topCandidates(stateTemplates []string, template, prefix string, argTokens []string, at time.Time, n int) []rawScore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.topNLocked(stateTemplates, template, prefix, argTokens, at, n)
}

// rawScore pairs a raw command with its hydration score. topNLocked exposes
// the runner-up candidates behind bestLocked's single pick, so a template
// hunch is confident about but whose literal argument is genuinely
// ambiguous (e.g. two recently-used directories for "cd PATH") can offer
// more than one hydration instead of silently picking one.
type rawScore struct {
	Raw   string
	Score float64
}

// bestLocked returns the highest-scored raw for a template, trying
// progressively shorter state windows until a bucket matches. The read lock
// must be held.
func (s *rawStore) bestLocked(stateTemplates []string, template, prefix string, argTokens []string, at time.Time) string {
	ranked := s.topNLocked(stateTemplates, template, prefix, argTokens, at, 1)
	if len(ranked) == 0 {
		return ""
	}
	return ranked[0].Raw
}

// topNLocked returns up to n ranked raw candidates for a template, trying
// progressively shorter state windows until a bucket matches (mirroring
// bestLocked's backoff). The read lock must be held.
func (s *rawStore) topNLocked(stateTemplates []string, template, prefix string, argTokens []string, at time.Time, n int) []rawScore {
	for trim := 0; trim <= len(stateTemplates); trim++ {
		inner := s.m[rawOuterKey(stateTemplates[trim:], template)]
		if len(inner) == 0 {
			continue
		}
		if ranked := s.rankBucket(inner, prefix, argTokens, at, n); len(ranked) > 0 {
			return ranked
		}
	}
	return nil
}

// rankBucket scores every raw in a bucket and returns the top n, descending
// by score. With a non-empty prefix it considers only raws that literally
// start with it, when at least one does, falling back to the overall
// ranking otherwise - the same preference selectBest used to apply.
func (s *rawStore) rankBucket(inner map[string]rawEntry, prefix string, argTokens []string, at time.Time, n int) []rawScore {
	scored := make([]rawScore, 0, len(inner))
	prefixScored := make([]rawScore, 0, len(inner))
	for raw, entry := range inner {
		rs := rawScore{Raw: raw, Score: s.score(entry, raw, argTokens, at)}
		scored = append(scored, rs)
		if prefix != "" && strings.HasPrefix(raw, prefix) {
			prefixScored = append(prefixScored, rs)
		}
	}
	if prefix != "" && len(prefixScored) > 0 {
		scored = prefixScored
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if n > 0 && len(scored) > n {
		scored = scored[:n]
	}
	return scored
}

// tokenBoost is added per argument token shared with a recent command. It is
// large enough to override moderate frequency differences, reflecting how
// strongly a just-mentioned file or script name predicts reuse.
const tokenBoost = 100.0

// argLookback is how many of the most recent raw commands collectArgTokens
// scans for reusable argument tokens. Wide enough that a file or repo name
// mentioned a few commands ago still gets credit, not just the immediately
// preceding one.
const argLookback = 5

// score combines observation count with an exponential recency decay matching
// the graph's half-life, plus a boost for reusing recent argument tokens.
func (s *rawStore) score(entry rawEntry, raw string, argTokens []string, at time.Time) float64 {
	recency := 0.1 // floor for migrated entries that carry no timestamp
	if !entry.lastSeen.IsZero() {
		recency = math.Exp(-math.Ln2 * float64(at.Sub(entry.lastSeen)) / float64(s.halfLife))
	}
	score := float64(entry.count) * recency

	if len(argTokens) > 0 {
		rawTokens := make(map[string]struct{}, len(argTokens))
		for _, t := range normalize.Tokenize(raw) {
			rawTokens[t] = struct{}{}
		}
		for _, tok := range argTokens {
			if _, ok := rawTokens[tok]; ok {
				score += tokenBoost
			}
		}
	}
	return score
}

// collectArgTokens extracts variable-value tokens (STR, PATH, HASH, NUM, REPO)
// from the most recent raw prior commands: the file and script names the user
// is most likely to mention again. Tokens under 3 characters are skipped as
// too generic to be evidence.
func collectArgTokens(rawCmds []string, parents []string) []string {
	if len(rawCmds) == 0 {
		return nil
	}
	start := max(len(rawCmds)-argLookback, 0)

	var tokens []string
	seen := make(map[string]struct{})
	for _, raw := range rawCmds[start:] {
		for _, tok := range normalize.ExtractArgTokens(raw, parents) {
			if len(tok) < 3 {
				continue
			}
			if _, ok := seen[tok]; !ok {
				seen[tok] = struct{}{}
				tokens = append(tokens, tok)
			}
		}
	}
	return tokens
}
