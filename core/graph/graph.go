// Package graph tracks state → next-command transitions with counts and
// last-seen timestamps. It is pure logic with no IO, no shell awareness,
// and no external dependencies beyond the standard library.
//
// The graph stores transitions in a two-level map keyed first by a
// null-joined state key, then by the next command template. Concurrency
// is managed by a single sync.RWMutex.
package graph

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Transition represents a single observed state → next transition.
type Transition struct {
	State    []string  // last N templates, most recent last
	Next     string    // normalized template that followed
	Count    int       // times this (state, next) pair was recorded
	LastSeen time.Time // most recent observation
}

// entry is the internal storage unit for a single (state, next) pair.
type entry struct {
	count    int
	lastSeen time.Time
}

// Graph stores observed command transitions.
//
// The zero value is not usable; use New to construct a Graph.
type Graph struct {
	mu         sync.RWMutex
	windowSize int
	m          map[string]map[string]*entry
}

// New creates a Graph with the given window size.
func New(windowSize int) *Graph {
	return &Graph{
		windowSize: windowSize,
		m:          make(map[string]map[string]*entry),
	}
}

// Record records a transition from state to next at time at.
//
// The state slice is used as-is; callers are responsible for padding
// it to the window size (e.g. with empty-string sentinels for the
// first command of a session).
func (g *Graph) Record(state []string, next string, at time.Time) {
	if next == "" {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	sk := stateKey(state)
	inner, ok := g.m[sk]
	if !ok {
		inner = make(map[string]*entry)
		g.m[sk] = inner
	}
	e, ok := inner[next]
	if !ok {
		e = &entry{}
		inner[next] = e
	}
	e.count++
	if at.After(e.lastSeen) {
		e.lastSeen = at
	}
}

// Transitions returns all recorded transitions from the given state,
// or nil if the state has no transitions.
func (g *Graph) Transitions(state []string) []Transition {
	g.mu.RLock()
	defer g.mu.RUnlock()

	inner, ok := g.m[stateKey(state)]
	if !ok {
		return nil
	}

	result := make([]Transition, 0, len(inner))
	for next, e := range inner {
		stateCopy := make([]string, len(state))
		copy(stateCopy, state)
		result = append(result, Transition{
			State:    stateCopy,
			Next:     next,
			Count:    e.count,
			LastSeen: e.lastSeen,
		})
	}
	return result
}

// Decay is a hook for future compaction. In v1 it is a no-op.
//
// The actual decay formula is applied ephemerally at query time by the
// predict package (weight = exp(-age / halfLife)). Keeping raw counts
// in storage is deliberate: it preserves signal from old-but-once-strong
// transitions and avoids destructive compaction. Storage is bounded at
// ~10k–100k entries for a single user, so there is no compaction pressure.
//
// If compaction is added in a future version (pruning entries where
// effective weight has fallen below epsilon), this method will acquire
// the write lock and perform the sweep.
func (g *Graph) Decay(at time.Time, halfLife time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
}

// Merge incorporates a set of seed transitions into the graph.
//
// For each transition:
//   - Counts are additive (count += seed.Count).
//   - LastSeen is the max of the existing and seed timestamps.
func (g *Graph) Merge(seed []Transition) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, t := range seed {
		sk := stateKey(t.State)
		inner, ok := g.m[sk]
		if !ok {
			inner = make(map[string]*entry)
			g.m[sk] = inner
		}
		e, ok := inner[t.Next]
		if !ok {
			e = &entry{}
			inner[t.Next] = e
		}
		e.count += t.Count
		if t.LastSeen.After(e.lastSeen) {
			e.lastSeen = t.LastSeen
		}
	}
	return nil
}

// All returns every distinct transition in the graph, sorted by
// (state, next) lexicographically for stable export.
func (g *Graph) All() []Transition {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.m) == 0 {
		return nil
	}

	type twk struct {
		tr       Transition
		stateKey string
	}

	var buf []twk
	for sk, inner := range g.m {
		state := strings.Split(sk, "\x00")
		for next, e := range inner {
			stateCopy := make([]string, len(state))
			copy(stateCopy, state)
			buf = append(buf, twk{
				tr: Transition{
					State:    stateCopy,
					Next:     next,
					Count:    e.count,
					LastSeen: e.lastSeen,
				},
				stateKey: sk,
			})
		}
	}

	sort.Slice(buf, func(i, j int) bool {
		if buf[i].stateKey != buf[j].stateKey {
			return buf[i].stateKey < buf[j].stateKey
		}
		return buf[i].tr.Next < buf[j].tr.Next
	})

	result := make([]Transition, len(buf))
	for i, t := range buf {
		result[i] = t.tr
	}
	return result
}

// Size returns the number of distinct (state, next) pairs in the graph.
func (g *Graph) Size() int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	n := 0
	for _, inner := range g.m {
		n += len(inner)
	}
	return n
}

// stateKey joins the state slice with a null byte separator. The null
// byte cannot appear in normalized templates (tokens are space-separated
// identifiers), so it is a safe separator that preserves order.
func stateKey(state []string) string {
	return strings.Join(state, "\x00")
}
