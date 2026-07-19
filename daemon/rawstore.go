package daemon

import (
	"math"
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

// dropOrphaned removes every bucket whose next-command template no longer
// exists in the graph, so decayed templates do not leak raws indefinitely.
func (s *rawStore) dropOrphaned(templates []string) {
	if len(templates) == 0 {
		return
	}
	orphaned := make(map[string]struct{}, len(templates))
	for _, tmpl := range templates {
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

// hydrate fills in the Raw field of each suggestion with the best matching
// concrete command. The whole batch is resolved under one read lock so every
// suggestion sees the same snapshot.
func (s *rawStore) hydrate(suggestions []types.Suggestion, stateTemplates []string, prefix string, argTokens []string, at time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i, sug := range suggestions {
		if raw := s.bestLocked(stateTemplates, sug.Template, prefix, argTokens, at); raw != "" {
			suggestions[i].Raw = raw
		}
	}
}

// bestLocked returns the highest-scored raw for a template, trying
// progressively shorter state windows until a bucket matches. The read lock
// must be held.
func (s *rawStore) bestLocked(stateTemplates []string, template, prefix string, argTokens []string, at time.Time) string {
	for trim := 0; trim <= len(stateTemplates); trim++ {
		inner := s.m[rawOuterKey(stateTemplates[trim:], template)]
		if len(inner) == 0 {
			continue
		}
		if raw := s.selectBest(inner, prefix, argTokens, at); raw != "" {
			return raw
		}
	}
	return ""
}

// selectBest picks the highest-scored raw from a bucket. With a non-empty
// prefix it prefers raws that literally start with it, falling back to the
// overall best when none do.
func (s *rawStore) selectBest(inner map[string]rawEntry, prefix string, argTokens []string, at time.Time) string {
	bestRaw, bestScore := "", -1.0
	bestPrefixRaw, bestPrefixScore := "", -1.0

	for raw, entry := range inner {
		score := s.score(entry, raw, argTokens, at)
		if score > bestScore {
			bestScore = score
			bestRaw = raw
		}
		if prefix != "" && strings.HasPrefix(raw, prefix) && score > bestPrefixScore {
			bestPrefixScore = score
			bestPrefixRaw = raw
		}
	}

	if prefix != "" && bestPrefixRaw != "" {
		return bestPrefixRaw
	}
	return bestRaw
}

// tokenBoost is added per argument token shared with a recent command. It is
// large enough to override moderate frequency differences, reflecting how
// strongly a just-mentioned file or script name predicts reuse.
const tokenBoost = 100.0

// score combines observation count with an exponential recency decay matching
// the graph's half-life, plus a boost for reusing recent argument tokens.
func (s *rawStore) score(entry rawEntry, raw string, argTokens []string, at time.Time) float64 {
	recency := 0.1 // floor for migrated entries that carry no timestamp
	if !entry.lastSeen.IsZero() {
		recency = math.Exp(-math.Ln2 * float64(at.Sub(entry.lastSeen)) / float64(s.halfLife))
	}
	score := float64(entry.count) * recency

	for _, tok := range argTokens {
		if strings.Contains(raw, tok) {
			score += tokenBoost
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
	start := max(len(rawCmds)-2, 0)

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
