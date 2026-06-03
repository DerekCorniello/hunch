// Package predict scores and ranks transitions for a given state,
// returning suggestions to the caller. It is pure logic with no IO
// and depends only on the graph and types packages.
package predict

import (
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/types"
)

// Predictor scores transitions from a graph and returns ranked suggestions.
type Predictor struct {
	g        *graph.Graph
	halfLife time.Duration
	alpha    float64
}

// New constructs a Predictor.
//
// halfLife is the duration for an observation to halve in effective weight.
// alpha is the additive smoothing constant (default 0.5 in the daemon config).
func New(g *graph.Graph, halfLife time.Duration, alpha float64) *Predictor {
	return &Predictor{
		g:        g,
		halfLife: halfLife,
		alpha:    alpha,
	}
}

// Predict returns ranked suggestions for the given state, ordered by
// descending score then descending count as tie-breaker.
//
// limit caps the number of suggestions returned; 0 means no limit.
// CWD on the state is never used in scoring (locked design decision).
// All transitions are scored regardless of outcome (locked design decision).
func (p *Predictor) Predict(state types.State, at time.Time, limit int) []types.Suggestion {
	templates := make([]string, len(state.Previous))
	for i, cmd := range state.Previous {
		templates[i] = cmd.Template
	}

	transitions := p.g.Transitions(templates)
	if len(transitions) == 0 {
		return nil
	}

	scored := scoreTransitions(transitions, p.halfLife, p.alpha, at)

	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}

	suggestions := make([]types.Suggestion, len(scored))
	for i, s := range scored {
		suggestions[i] = types.Suggestion{
			Template: s.next,
			Score:    s.score,
			Count:    s.count,
		}
	}
	return suggestions
}
