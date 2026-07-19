// Package graph tracks state -> next-command transitions with counts and
// last-seen timestamps. It is pure logic with no IO, no shell awareness,
// and no external dependencies beyond the standard library.
//
// The graph stores transitions in a two-level map keyed first by a
// null-joined state key, then by the next command template. Concurrency
// is managed by a single sync.RWMutex.
package graph

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Outcome classifies a command's exit result for soft weighting. The empty
// value means "unknown" and contributes to no outcome counter, so commands
// recorded without an exit code (or interrupted by a signal) never skew the
// success/failure signal.
type Outcome string

const (
	OutcomeUnknown Outcome = ""
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// Transition represents a single observed state -> next transition.
//
// The JSON names are part of the seed file format, and match the field names
// used by the export IPC response so that exported data can be fed back in as
// a seed. Renaming one without the other silently breaks that round trip:
// unmatched fields unmarshal to their zero value rather than erroring.
type Transition struct {
	State    []string  `json:"state"`     // last N templates, most recent last
	Next     string    `json:"next"`      // normalized template that followed
	Count    int       `json:"count"`     // times this (state, next) pair was recorded
	LastSeen time.Time `json:"last_seen"` // most recent observation

	// CWDs is the histogram of working directories in which Next was run,
	// used for the location-affinity boost. May be nil.
	CWDs map[string]int `json:"cwds,omitempty"`
	// NextSuccess/NextFailure count how often Next itself exited
	// successfully or with failure, used to suppress chronically-failing
	// suggestions.
	NextSuccess int `json:"next_success,omitempty"`
	NextFailure int `json:"next_failure,omitempty"`
	// PriorSuccess/PriorFailure count how often this transition followed a
	// prior command that succeeded or failed, used to weight by prior
	// outcome context.
	PriorSuccess int `json:"prior_success,omitempty"`
	PriorFailure int `json:"prior_failure,omitempty"`
	// Accepted counts how often the executed command matched a suggestion
	// hunch had shown for this state, used to boost confirmed transitions.
	Accepted int `json:"accepted,omitempty"`
}

// Observation is a single recorded transition with its soft-signal context.
type Observation struct {
	State []string  // previous templates, most recent last
	Next  string    // normalized template that followed
	At    time.Time // observation time

	CWD          string  // directory Next ran in; "" if unknown
	NextOutcome  Outcome // exit result of Next
	PriorOutcome Outcome // exit result of the command preceding Next
	Accepted     bool    // Next matched a suggestion hunch had shown
}

// entry is the internal storage unit for a single (state, next) pair.
type entry struct {
	count        int
	lastSeen     time.Time
	cwds         map[string]int
	nextSuccess  int
	nextFailure  int
	priorSuccess int
	priorFailure int
	accepted     int
}

// Graph stores observed command transitions.
//
// The zero value is not usable; use New to construct a Graph.
type Graph struct {
	mu         sync.RWMutex
	windowSize int
	m          map[string]map[string]*entry
}

// New creates a Graph with the given window size (must be at least 1).
func New(windowSize int) *Graph {
	if windowSize < 1 {
		windowSize = 1
	}
	return &Graph{
		windowSize: windowSize,
		m:          make(map[string]map[string]*entry),
	}
}

// Record records a transition from state to next at time at, with no
// CWD or outcome context. It is a convenience wrapper around RecordObs.
//
// The state slice is used as-is; callers are responsible for padding
// it to the window size (e.g. with empty-string sentinels for the
// first command of a session).
func (g *Graph) Record(state []string, next string, at time.Time) {
	g.RecordObs(Observation{State: state, Next: next, At: at})
}

// RecordObs records an observed transition along with its soft-signal
// context (working directory and outcomes). Unknown outcomes and an empty
// CWD contribute to no counter.
func (g *Graph) RecordObs(obs Observation) {
	if obs.Next == "" {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	sk := stateKey(obs.State)
	inner, ok := g.m[sk]
	if !ok {
		inner = make(map[string]*entry)
		g.m[sk] = inner
	}
	e, ok := inner[obs.Next]
	if !ok {
		e = &entry{}
		inner[obs.Next] = e
	}
	e.count++
	if obs.At.After(e.lastSeen) {
		e.lastSeen = obs.At
	}
	if obs.CWD != "" {
		if e.cwds == nil {
			e.cwds = make(map[string]int)
		}
		e.cwds[obs.CWD]++
	}
	switch obs.NextOutcome {
	case OutcomeSuccess:
		e.nextSuccess++
	case OutcomeFailure:
		e.nextFailure++
	}
	switch obs.PriorOutcome {
	case OutcomeSuccess:
		e.priorSuccess++
	case OutcomeFailure:
		e.priorFailure++
	}
	if obs.Accepted {
		e.accepted++
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
		result = append(result, entryToTransition(state, next, e))
	}
	return result
}

// entryToTransition builds an exported Transition from an internal entry,
// deep-copying mutable fields so the caller cannot mutate graph state.
func entryToTransition(state []string, next string, e *entry) Transition {
	stateCopy := make([]string, len(state))
	copy(stateCopy, state)
	return Transition{
		State:        stateCopy,
		Next:         next,
		Count:        e.count,
		LastSeen:     e.lastSeen,
		CWDs:         copyCWDs(e.cwds),
		NextSuccess:  e.nextSuccess,
		NextFailure:  e.nextFailure,
		PriorSuccess: e.priorSuccess,
		PriorFailure: e.priorFailure,
		Accepted:     e.accepted,
	}
}

// copyCWDs returns a copy of a CWD histogram, or nil if empty.
func copyCWDs(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	c := make(map[string]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// DecayResult reports what Decay removed.
type DecayResult struct {
	// Pruned lists the (state, next) pairs removed from the graph. Only
	// State and Next are populated; Count and LastSeen are zero.
	Pruned []Transition
	// Orphaned lists templates that no longer appear as the "next" of any
	// surviving transition, so their stored raw examples can be pruned too.
	Orphaned []string
}

// Decay prunes transitions whose effective weight falls below epsilon and
// returns what was removed. The effective weight is count * 0.5^(age/halfLife),
// so a single observation halves in weight every halfLife.
func (g *Graph) Decay(at time.Time, halfLife time.Duration) DecayResult {
	g.mu.Lock()
	defer g.mu.Unlock()

	const epsilon = 0.001
	var pruned []Transition
	for sk, inner := range g.m {
		state := strings.Split(sk, "\x00")
		for next, e := range inner {
			age := at.Sub(e.lastSeen)
			weight := float64(e.count) * math.Exp(-math.Ln2*float64(age)/float64(halfLife))
			if weight < epsilon {
				stateCopy := make([]string, len(state))
				copy(stateCopy, state)
				pruned = append(pruned, Transition{State: stateCopy, Next: next})
				delete(inner, next)
			}
		}
		if len(inner) == 0 {
			delete(g.m, sk)
		}
	}

	if len(pruned) == 0 {
		return DecayResult{}
	}

	// A pruned template's raw examples are only safe to drop once no
	// surviving transition still has that template as its next.
	remaining := make(map[string]struct{})
	for _, inner := range g.m {
		for next := range inner {
			remaining[next] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	var orphaned []string
	for _, t := range pruned {
		if _, stillUsed := remaining[t.Next]; stillUsed {
			continue
		}
		if _, dup := seen[t.Next]; dup {
			continue
		}
		seen[t.Next] = struct{}{}
		orphaned = append(orphaned, t.Next)
	}

	return DecayResult{Pruned: pruned, Orphaned: orphaned}
}

// Merge incorporates a set of seed transitions into the graph.
//
// For each transition:
//   - Counts are additive (count += seed.Count).
//   - LastSeen is the max of the existing and seed timestamps.
func (g *Graph) Merge(seed []Transition) error {
	if err := validateTransitions(seed); err != nil {
		return err
	}

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
		// Counts are combined by maximum, not addition.
		//
		// A seed asserts how many times a transition has been observed, not
		// how many observations to add. The two differ because a shell
		// history file records the same commands the daemon already saw
		// live, so adding would count every one of them twice, and importing
		// the same history again would double them again. Taking the maximum
		// makes import idempotent and keeps a command run once from looking
		// like a habit.
		e.count = max(e.count, t.Count)
		if t.LastSeen.After(e.lastSeen) {
			e.lastSeen = t.LastSeen
		}
		for cwd, c := range t.CWDs {
			if e.cwds == nil {
				e.cwds = make(map[string]int)
			}
			e.cwds[cwd] = max(e.cwds[cwd], c)
		}
		e.nextSuccess = max(e.nextSuccess, t.NextSuccess)
		e.nextFailure = max(e.nextFailure, t.NextFailure)
		e.priorSuccess = max(e.priorSuccess, t.PriorSuccess)
		e.priorFailure = max(e.priorFailure, t.PriorFailure)
		e.accepted = max(e.accepted, t.Accepted)
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
			buf = append(buf, twk{
				tr:       entryToTransition(state, next, e),
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

// validateTransitions checks that all transitions have non-empty state,
// non-empty next, and positive count.
func validateTransitions(seed []Transition) error {
	for i, t := range seed {
		if len(t.State) == 0 {
			return fmt.Errorf("transition %d: empty state", i)
		}
		if t.Next == "" {
			return fmt.Errorf("transition %d: empty next", i)
		}
		if t.Count <= 0 {
			return fmt.Errorf("transition %d: count must be positive, got %d", i, t.Count)
		}
	}
	return nil
}
