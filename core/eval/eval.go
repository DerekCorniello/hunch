// Package eval measures prediction quality by replaying a command history.
// It is pure logic with no IO, depending only on graph, predict, and types.
package eval

import (
	"path/filepath"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/predict"
	"github.com/DerekCorniello/hunch/core/types"
)

// Options configures a replay. The scoring constants mirror the daemon's so
// a measurement reflects what a user would actually see.
type Options struct {
	HalfLife time.Duration
	Alpha    float64
	Beta     float64
	Gamma    float64
	Delta    float64
	Epsilon  float64

	// MinConfidence gates suggestions drawn from a generalized context, and
	// MinCount gates every suggestion on how much evidence backs it. Both
	// mirror the daemon.
	MinConfidence float64
	MinCount      int

	// Warmup is the number of leading commands to learn from without
	// scoring. Without it the cold start, where nothing can be predicted,
	// dominates the result and understates steady-state quality.
	Warmup int

	// Interval is the synthetic spacing between consecutive commands. Shell
	// history files do not reliably carry timestamps, so replay assumes a
	// constant cadence; this is what exercises the decay term.
	Interval time.Duration
}

// Command represents a single command in the replay input.
type Command struct {
	Template     string // normalized command template
	CWD          string // working directory ("" if unknown)
	PriorOutcome string // outcome of the command preceding this one
}

// DefaultOptions matches the daemon's shipped defaults.
func DefaultOptions() Options {
	return Options{
		HalfLife:      720 * time.Hour,
		Alpha:         0.5,
		Beta:          0.75,
		Gamma:         0.5,
		Delta:         0.5,
		Epsilon:       0.5,
		MinConfidence: 0.10,
		MinCount:      2,
		Warmup:        50,
		Interval:      time.Minute,
	}
}

// Result counts outcomes over the scored portion of a history.
type Result struct {
	// Scored is the number of commands the model was asked to predict.
	Scored int
	// Offered is how many of those produced at least one suggestion.
	Offered int
	// TopN counts commands whose actual template appeared in the top N.
	Top1 int
	Top3 int
	Top5 int
	// BaselineTop1 counts commands matching the single most frequent
	// template seen so far. It is the number any accuracy claim has to
	// beat to mean anything.
	BaselineTop1 int
}

// Rate is a helper for expressing a count as a fraction of Scored.
func (r Result) Rate(n int) float64 {
	if r.Scored == 0 {
		return 0
	}
	return float64(n) / float64(r.Scored)
}

// maxRank is the deepest position Run inspects, matching the widest TopN.
const maxRank = 5

// Run replays commands in order, predicting each from the ones before it and
// then learning it.
//
// The evaluation is prequential: every command is predicted by a model that
// has seen only earlier commands, which is exactly how the daemon operates.
// A held-out split would instead measure a model trained on the user's future.
func Run(commands []Command, opts Options) Result {
	g := graph.New(2)
	p := predict.New(g, opts.HalfLife, opts.Alpha, opts.Beta, opts.Gamma, opts.Delta, opts.Epsilon)

	start := time.Unix(0, 0).UTC()
	freq := make(map[string]int)
	var mostFrequent string

	var result Result
	var prev1, prev2 string

	for i, cmd := range commands {
		actual := cmd.Template
		at := start.Add(time.Duration(i) * opts.Interval)

		if i >= opts.Warmup && actual != "" {
			result.Scored++
			if mostFrequent == actual {
				result.BaselineTop1++
			}
			suggestions := predictWithFallback(p, prev1, prev2, cmd.CWD, cmd.PriorOutcome, at, opts)
			score(&result, suggestions, actual)
		}

		if actual != "" {
			// Mirror the daemon's backoff recording so a fallback query
			// has something to match.
			state := []string{prev1, prev2}
			if cmd.CWD != "" {
				state = append([]string{cmd.CWD}, state...)
			}
			for _, st := range graph.BackoffStates(state, cmd.CWD != "") {
				g.RecordObs(graph.Observation{
					State: st,
					Next:  actual,
					At:    at,
				})
			}
			if prev2 != "" {
				g.Record([]string{prev2}, actual, at)
			}
			freq[actual]++
			if freq[actual] > freq[mostFrequent] {
				mostFrequent = actual
			}
		}
		prev1, prev2 = prev2, actual
	}
	return result
}

// predictWithFallback mirrors the daemon's predictWithFallback (handlers.go).
// It queries the predictor through progressively more general state keys:
// exact context with CWD, ancestor CWDs, no CWD, shorter history windows.
func predictWithFallback(p *predict.Predictor, prev1, prev2 string, cwd, priorOutcome string, at time.Time, opts Options) []types.Suggestion {
	query := func(previous []types.Command) []types.Suggestion {
		return withMinCount(p.Predict(types.State{Previous: previous, CWD: ""}, at, maxRank), opts.MinCount)
	}
	queryWithCWD := func(previous []types.Command, dir string) []types.Suggestion {
		return withMinCount(p.Predict(types.State{Previous: previous, CWD: dir}, at, maxRank), opts.MinCount)
	}
	confident := func(s []types.Suggestion) bool {
		return len(s) > 0 && s[0].Score >= opts.MinConfidence
	}

	prev := []types.Command{{Template: prev1}, {Template: prev2}}

	// Level 1: exact directory, as learned from live sessions.
	suggestions := queryWithCWD(prev, cwd)
	if len(suggestions) > 0 {
		return suggestions
	}

	// Level 2: ancestor directories, so a workflow learned in ~/project
	// still applies in ~/project/src.
	if cwd != "" {
		for parent := filepath.Dir(cwd); parent != cwd && parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
			if s := queryWithCWD(prev, parent); confident(s) {
				return s
			}
		}
	}

	// Level 3: no directory at all, which is how imported shell history and
	// anything recorded before CWD tracking is keyed.
	if s := query(prev); confident(s) {
		return s
	}

	// Level 4: progressively shorter history windows.
	for trim := 1; trim <= len(prev); trim++ {
		if s := query(prev[trim:]); confident(s) {
			return s
		}
	}
	return nil
}

// withMinCount drops suggestions backed by fewer than minCount observations.
func withMinCount(suggestions []types.Suggestion, minCount int) []types.Suggestion {
	if minCount <= 1 {
		return suggestions
	}
	kept := suggestions[:0]
	for _, s := range suggestions {
		if s.Count >= minCount {
			kept = append(kept, s)
		}
	}
	return kept
}

func score(result *Result, suggestions []types.Suggestion, actual string) {
	if len(suggestions) == 0 {
		return
	}
	result.Offered++

	for i, s := range suggestions {
		if s.Template != actual {
			continue
		}
		switch {
		case i < 1:
			result.Top1++
			result.Top3++
			result.Top5++
		case i < 3:
			result.Top3++
			result.Top5++
		case i < maxRank:
			result.Top5++
		}
		return
	}
}
