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
	var diffuseIdx int = -1
	if len(ranked) == 0 {
		out = append(out, top)
	} else {
		for i := 0; i <= extra; i++ {
			sug := top
			sug.Raw = ranked[i].Raw
			out = append(out, sug)
			if ranked[i].Diffuse && diffuseIdx == -1 {
				diffuseIdx = len(out) - 1
			}
		}
	}

	for _, sug := range suggestions[1:] {
		if raw := s.bestLocked(stateTemplates, sug.Template, prefix, argTokens, at); raw != "" {
			sug.Raw = raw
		}
		out = append(out, sug)
	}

	// Promote a diffuse stub to the front: when a bucket has high variance,
	// the stub (e.g. `git commit -m "`) is more useful than a specific raw
	// from a different template that happens to score higher.
	if diffuseIdx > 0 {
		stub := out[diffuseIdx]
		copy(out[1:diffuseIdx+1], out[0:diffuseIdx])
		out[0] = stub
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
	Raw     string
	Score   float64
	Diffuse bool // true when this raw is a stub from a diffuse bucket
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
		if ranked := s.rankBucket(template, inner, prefix, argTokens, at, n); len(ranked) > 0 {
			return ranked
		}
	}
	// Nothing recorded for this template in any context - if it has exactly
	// one value slot and a just-mentioned token can fill it, offer that
	// synthesized command instead of nothing. See synthesize.
	if syn, ok := synthesize(template, argTokens); ok && (prefix == "" || strings.HasPrefix(syn, prefix)) {
		return []rawScore{{Raw: syn, Score: tokenBoost}}
	}
	return nil
}

// rankBucket scores every raw in a bucket and returns the top n, descending
// by score. With a non-empty prefix it considers only raws that literally
// start with it, when at least one does, falling back to the overall
// ranking otherwise - the same preference selectBest used to apply.
//
// Two cases get special handling before that ordinary ranking:
//
//   - A single-value-slot template (see synthesize) gets a synthesized
//     candidate built from a just-mentioned argument token, competing
//     alongside whatever was actually recorded. This is what lets "npm ls
//     lodash" followed by "npm view" suggest "npm view lodash" the first
//     time, not only after that exact pair has been typed before.
//
//   - A diffuse bucket - many raws, no one of them a real habit (see
//     isDiffuse) - is exactly the case where guessing a full value is more
//     likely wrong than right (a git commit message, a grep pattern). It
//     offers the raws' shared literal prefix instead, so ghost text stops
//     right where the free-form part begins rather than asserting a stale
//     example. Once prefix has moved past that shared part - the user typed
//     their own value and closed it - resumeAfterBlank looks for a shared
//     tail to resume with instead.
func (s *rawStore) rankBucket(template string, inner map[string]rawEntry, prefix string, argTokens []string, at time.Time, n int) []rawScore {
	scored := make([]rawScore, 0, len(inner)+1)
	var total float64
	// baseTotal tracks the sum of scores without token boost, used for
	// diffuse detection. Token boost reflects recency, not habit strength,
	// so including it would let one recently-mentioned candidate dominate
	// and prevent diffuse detection from triggering.
	var baseTotal float64
	for raw, entry := range inner {
		baseSc := s.baseScore(entry, at)
		sc := baseSc
		// Apply token boost separately
		if len(argTokens) > 0 {
			rawTokens := make(map[string]struct{}, len(argTokens))
			for _, t := range normalize.Tokenize(raw) {
				rawTokens[t] = struct{}{}
			}
			for i, tok := range argTokens {
				if _, ok := rawTokens[tok]; ok {
					weight := 1.0 - (float64(i)/float64(len(argTokens)))*0.8
					sc += tokenBoost * weight
				}
			}
		}
		scored = append(scored, rawScore{Raw: raw, Score: sc})
		total += sc
		baseTotal += baseSc
	}
	if syn, ok := synthesize(template, argTokens); ok {
		if _, exists := inner[syn]; !exists {
			scored = append(scored, rawScore{Raw: syn, Score: tokenBoost})
			total += tokenBoost
			// Synthesized commands have no base score (never recorded)
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })

	// Use baseTotal for diffuse detection to avoid token boost skewing results
	// We need base scores for the top candidate too, not just the total
	baseScored := make([]rawScore, 0, len(inner))
	for raw, entry := range inner {
		baseSc := s.baseScore(entry, at)
		baseScored = append(baseScored, rawScore{Raw: raw, Score: baseSc})
	}
	sort.Slice(baseScored, func(i, j int) bool { return baseScored[i].Score > baseScored[j].Score })
	diffuse := isDiffuse(baseScored, baseTotal)
	if diffuse {
		if stub, ok := diffuseStub(topRawKeys(inner, 20)); ok && (prefix == "" || strings.HasPrefix(stub, prefix)) {
			return []rawScore{{Raw: stub, Score: scored[0].Score, Diffuse: true}}
		}
	}

	if prefix == "" {
		if n > 0 && len(scored) > n {
			scored = scored[:n]
		}
		return scored
	}

	var prefixScored []rawScore
	for _, rs := range scored {
		if strings.HasPrefix(rs.Raw, prefix) {
			prefixScored = append(prefixScored, rs)
		}
	}
	switch {
	case len(prefixScored) > 0:
		scored = prefixScored
	case diffuse:
		if resumed, ok := s.resumeAfterBlank(inner, prefix); ok {
			return []rawScore{{Raw: resumed, Score: 1}}
		}
		// A diffuse bucket already means no single value here is
		// trustworthy; falling through to "best overall regardless of
		// prefix" below would just reproduce the original bug in a new
		// spot - an unrelated literal that happens to score well but
		// doesn't extend what's actually been typed. Silence is correct
		// until either a real prefix match or a resume point.
		return nil
	}

	if n > 0 && len(scored) > n {
		scored = scored[:n]
	}
	return scored
}

// hydrationDiffuseMinRaws is the smallest bucket isDiffuse and
// resumeAfterBlank will act on. Below this, ordinary ranking (and hydrate's
// own ambiguous-top cycling) already does the right thing - there's nothing
// meaningfully "diffuse" about two candidates.
const hydrationDiffuseMinRaws = 3

// hydrationDiffuseThreshold is how much of a bucket's total score its top
// candidate must hold to count as a real habit rather than one guess among
// many. Below this, no answer holds even two-fifths of the observed weight,
// so nothing in the bucket is trustworthy as a literal guess.
const hydrationDiffuseThreshold = 0.4

// isDiffuse reports whether a bucket has no candidate strong enough to trust
// as a full guess. scored must already be sorted descending by score.
func isDiffuse(scored []rawScore, total float64) bool {
	if len(scored) < hydrationDiffuseMinRaws || total <= 0 {
		return false
	}
	return scored[0].Score/total < hydrationDiffuseThreshold
}

// diffuseStub returns the literal prefix shared by every raw in a diffuse
// bucket, trimmed back to the last clean word/quote boundary so it never
// stops mid-word. Ok is false when there's no shared boundary to offer (the
// raws diverge immediately).
func diffuseStub(raws []string) (string, bool) {
	stub := trimPrefixToBoundary(commonPrefix(raws))
	return stub, stub != ""
}

// resumeAfterBlank picks suggestions back up once a diffuse bucket's shared
// prefix has been typed past and its free-form part looks finished - the
// user closed the quote they opened, or typed a full word after an unquoted
// blank. If the bucket's raws still share a stable tail beyond that point
// (a flag that's almost always appended, say), it offers prefix+that tail,
// rather than guessing the free part or going silent for the rest of the
// line.
func (s *rawStore) resumeAfterBlank(inner map[string]rawEntry, prefix string) (string, bool) {
	if len(inner) < hydrationDiffuseMinRaws {
		return "", false
	}
	raws := rawKeys(inner)
	stub := trimPrefixToBoundary(commonPrefix(raws))
	if stub == "" || !freeContentClosed(stub, prefix) {
		return "", false
	}
	suffix := strings.TrimLeft(trimSuffixToBoundary(commonSuffix(raws)), " \"'")
	if suffix == "" {
		return "", false
	}
	if !strings.HasSuffix(prefix, " ") {
		suffix = " " + suffix
	}
	return prefix + suffix, true
}

// freeContentClosed reports whether prefix has typed past stub's boundary
// character and supplied its own matching close: another quote of the same
// kind if stub opened one, or at least one more character (a full word) if
// stub just ended on a bare space.
func freeContentClosed(stub, prefix string) bool {
	if !strings.HasPrefix(prefix, stub) || stub == "" {
		return false
	}
	rest := prefix[len(stub):]
	if rest == "" {
		return false
	}
	switch stub[len(stub)-1] {
	case '"':
		return strings.ContainsRune(rest, '"')
	case '\'':
		return strings.ContainsRune(rest, '\'')
	default:
		return strings.ContainsRune(rest, ' ')
	}
}

// rawKeys returns the literal raw commands in a bucket, in no particular
// order.
func rawKeys(inner map[string]rawEntry) []string {
	keys := make([]string, 0, len(inner))
	for raw := range inner {
		keys = append(keys, raw)
	}
	return keys
}

// topRawKeys returns up to n literal raw commands from a bucket, sorted by
// count descending (most common first). This is used by diffuseStub to
// compute the common prefix from the most representative examples rather
// than all raws, which would be dragged down by rare outliers.
func topRawKeys(inner map[string]rawEntry, n int) []string {
	type kv struct {
		raw   string
		count int
	}
	kvs := make([]kv, 0, len(inner))
	for raw, entry := range inner {
		kvs = append(kvs, kv{raw, entry.count})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].count > kvs[j].count })
	if n > 0 && len(kvs) > n {
		kvs = kvs[:n]
	}
	keys := make([]string, len(kvs))
	for i, kv := range kvs {
		keys[i] = kv.raw
	}
	return keys
}

// trimPrefixToBoundary cuts s back to just after its last space or quote
// character, so a shared prefix never stops mid-word. Returns "" if s has no
// such boundary at all.
func trimPrefixToBoundary(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ' ', '"', '\'':
			return s[:i+1]
		}
	}
	return ""
}

// trimSuffixToBoundary cuts s forward to start at its first space or quote
// character, so a shared suffix never starts mid-word. Returns "" if s has
// no such boundary at all.
func trimSuffixToBoundary(s string) string {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '"', '\'':
			return s[i:]
		}
	}
	return ""
}

// commonPrefix returns the longest literal prefix shared by every string in
// strs, or "" if strs is empty or they share none.
func commonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	prefix := strs[0]
	for _, s := range strs[1:] {
		n := min(len(prefix), len(s))
		i := 0
		for i < n && prefix[i] == s[i] {
			i++
		}
		prefix = prefix[:i]
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

// commonSuffix returns the longest literal suffix shared by every string in
// strs, or "" if strs is empty or they share none.
func commonSuffix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	suffix := strs[0]
	for _, s := range strs[1:] {
		n := min(len(suffix), len(s))
		i := 0
		for i < n && suffix[len(suffix)-1-i] == s[len(s)-1-i] {
			i++
		}
		suffix = suffix[len(suffix)-i:]
		if suffix == "" {
			return ""
		}
	}
	return suffix
}

// valueMarkers are the placeholder types that stand for actual reusable
// content - the same set normalize.ExtractArgTokens collects. FLAG and
// KWARGS are placeholders too, but there's no single value to substitute for
// either: a flag's presence is binary and KWARGS swallows everything after
// "--" wholesale.
var valueMarkers = map[string]bool{
	"STR": true, "PATH": true, "HASH": true, "NUM": true, "REPO": true,
}

// placeholderMarkers are every token type normalize.classifyTokens collapses
// variable content into.
var placeholderMarkers = map[string]bool{
	"FLAG": true, "PATH": true, "REPO": true, "HASH": true, "NUM": true, "STR": true, "KWARGS": true,
}

// singleSlot reports whether template has exactly one placeholder and it's a
// value type, returning the literal tokens on either side. A template with
// zero or more than one placeholder is ambiguous about where a substituted
// value belongs (or whether there's anywhere safe to put one), so it's
// excluded: "cp foo.txt bar.txt" collapses to "cp STR" during normalization,
// and reconstructing that blindly from one remembered token could put a
// destination path in the source slot.
func singleSlot(template string) (before, after []string, ok bool) {
	fields := strings.Fields(template)
	idx := -1
	for i, f := range fields {
		if placeholderMarkers[f] {
			if idx != -1 {
				return nil, nil, false
			}
			idx = i
		}
	}
	if idx == -1 || !valueMarkers[fields[idx]] {
		return nil, nil, false
	}
	return fields[:idx], fields[idx+1:], true
}

// synthesize builds a runnable command for a single-value-slot template (see
// singleSlot) by substituting the most recently mentioned argument token into
// that slot, even when that exact command was never recorded verbatim. This
// is what lets "npm ls lodash" followed by "npm view" suggest "npm view
// lodash" on the very first occurrence, instead of only ever recalling a
// literal command seen before - which, for an unrelated but more frequent
// past package, was suggesting the wrong one entirely.
func synthesize(template string, argTokens []string) (string, bool) {
	if len(argTokens) == 0 {
		return "", false
	}
	before, after, ok := singleSlot(template)
	if !ok {
		return "", false
	}
	parts := make([]string, 0, len(before)+1+len(after))
	parts = append(parts, before...)
	parts = append(parts, argTokens[0])
	parts = append(parts, after...)
	return strings.Join(parts, " "), true
}

// tokenBoost is the maximum boost for a matching argument token at position 0
// (most recent). The actual boost decays linearly with position in argTokens,
// so position 0 gets 15, position 1 gets ~11, position 2 gets ~7, etc.
// Kept modest so it breaks ties between similar candidates without overriding
// established habits (base scores from count * recency).
const tokenBoost = 15.0

// argLookback is how many of the most recent raw commands collectArgTokens
// scans for reusable argument tokens. Wide enough that a file or repo name
// mentioned a few commands ago still gets credit, not just the immediately
// preceding one.
const argLookback = 5

// score combines observation count with an exponential recency decay matching
// the graph's half-life, plus a boost for reusing recent argument tokens.
// The token boost decays linearly with position: the most recently mentioned
// token gets full boost, older tokens get progressively less.
func (s *rawStore) score(entry rawEntry, raw string, argTokens []string, at time.Time) float64 {
	return s.baseScore(entry, at) + s.tokenBoostForRaw(raw, argTokens)
}

// baseScore returns the count * recency decay without any token boost.
// Used by diffuse detection to avoid token boost skewing the results.
func (s *rawStore) baseScore(entry rawEntry, at time.Time) float64 {
	recency := 0.1 // floor for migrated entries that carry no timestamp
	if !entry.lastSeen.IsZero() {
		recency = math.Exp(-math.Ln2 * float64(at.Sub(entry.lastSeen)) / float64(s.halfLife))
	}
	return float64(entry.count) * recency
}

// tokenBoostForRaw calculates the token boost for a raw command based on
// matching argument tokens. The boost decays linearly with position in
// argTokens: position 0 gets full boost, older positions get less.
func (s *rawStore) tokenBoostForRaw(raw string, argTokens []string) float64 {
	if len(argTokens) == 0 {
		return 0
	}
	rawTokens := make(map[string]struct{}, len(argTokens))
	for _, t := range normalize.Tokenize(raw) {
		rawTokens[t] = struct{}{}
	}
	var boost float64
	for i, tok := range argTokens {
		if _, ok := rawTokens[tok]; ok {
			weight := 1.0 - (float64(i)/float64(len(argTokens)))*0.8
			boost += tokenBoost * weight
		}
	}
	return boost
}

// collectArgTokens extracts variable-value tokens (STR, PATH, HASH, NUM, REPO)
// from the most recent raw prior commands: the file and script names the user
// is most likely to mention again. Tokens under 3 characters are skipped as
// too generic to be evidence.
//
// The scan walks newest command first, so the returned slice is ordered most-
// recently-mentioned first; dedup keeps that first (freshest) occurrence.
// score's membership check doesn't care about order, but synthesize does - it
// wants the token from what you just typed, not one from four commands ago.
func collectArgTokens(rawCmds []string, parents []string) []string {
	if len(rawCmds) == 0 {
		return nil
	}
	start := max(len(rawCmds)-argLookback, 0)

	var tokens []string
	seen := make(map[string]struct{})
	for i := len(rawCmds) - 1; i >= start; i-- {
		for _, tok := range normalize.ExtractArgTokens(rawCmds[i], parents) {
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
