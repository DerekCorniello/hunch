// Package eval measures prediction quality by replaying a command history.
// It is pure logic with no IO, depending only on graph, predict, and types.
package eval

import (
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

	// MinConfidence gates suggestions drawn from a generalized context,
	// mirroring the daemon.
	MinConfidence float64

	// Warmup is the number of leading commands to learn from without
	// scoring. Without it the cold start, where nothing can be predicted,
	// dominates the result and understates steady-state quality.
	Warmup int

	// Interval is the synthetic spacing between consecutive commands. Shell
	// history files do not reliably carry timestamps, so replay assumes a
	// constant cadence; this is what exercises the decay term.
	Interval time.Duration
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
		MinConfidence: 0.20,
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

// Run replays templates in order, predicting each command from the ones
// before it and then learning it.
//
// The evaluation is prequential: every command is predicted by a model that
// has seen only earlier commands, which is exactly how the daemon operates.
// A held-out split would instead measure a model trained on the user's future.
func Run(templates []string, opts Options) Result {
	g := graph.New(2)
	p := predict.New(g, opts.HalfLife, opts.Alpha, opts.Beta, opts.Gamma, opts.Delta, opts.Epsilon)

	start := time.Unix(0, 0).UTC()
	freq := make(map[string]int)
	var mostFrequent string

	var result Result
	var prev1, prev2 string

	for i, actual := range templates {
		at := start.Add(time.Duration(i) * opts.Interval)

		if i >= opts.Warmup && actual != "" {
			result.Scored++
			if mostFrequent == actual {
				result.BaselineTop1++
			}
			score(&result, rank(p, prev1, prev2, at, opts.MinConfidence), actual)
		}

		if actual != "" {
			// Mirror the daemon's backoff recording so a fallback query
			// has something to match.
			g.Record([]string{prev1, prev2}, actual, at)
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

// rank mirrors the daemon's fallback: the exact context is trusted as-is,
// while a narrower one must clear minConfidence before it is offered.
func rank(p *predict.Predictor, prev1, prev2 string, at time.Time, minConfidence float64) []types.Suggestion {
	if s := p.Predict(types.State{Previous: []types.Command{{Template: prev1}, {Template: prev2}}}, at, maxRank); len(s) > 0 {
		return s
	}
	if prev2 == "" {
		return nil
	}
	s := p.Predict(types.State{Previous: []types.Command{{Template: prev2}}}, at, maxRank)
	if len(s) > 0 && s[0].Score >= minConfidence {
		return s
	}
	return nil
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
