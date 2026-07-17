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
	beta     float64
	gamma    float64
	delta    float64
	epsilon  float64
}

// New constructs a Predictor.
//
// halfLife is the duration for an observation to halve in effective weight.
// alpha is the additive smoothing constant. beta, gamma, delta, and epsilon
// are the strengths of the CWD-affinity boost, failure-rate suppression,
// prior-outcome boost, and confirmed-acceptance boost respectively; pass 0 to
// disable any of them.
func New(g *graph.Graph, halfLife time.Duration, alpha, beta, gamma, delta, epsilon float64) *Predictor {
	return &Predictor{
		g:        g,
		halfLife: halfLife,
		alpha:    alpha,
		beta:     beta,
		gamma:    gamma,
		delta:    delta,
		epsilon:  epsilon,
	}
}

// Predict returns ranked suggestions for the given state, ordered by
// descending score then descending count as tie-breaker.
//
// limit caps the number of suggestions returned; 0 means no limit.
// When state.CWD is set, it is prepended to the template state key so that
// transitions learned in different directories are stored under distinct
// keys. state.PriorOutcome softly boosts transitions following the same
// prior outcome.
func (p *Predictor) Predict(state types.State, at time.Time, limit int) []types.Suggestion {
	templates := make([]string, 0, len(state.Previous)+1)
	if state.CWD != "" {
		templates = append(templates, state.CWD)
	}
	for _, cmd := range state.Previous {
		templates = append(templates, cmd.Template)
	}

	transitions := p.g.Transitions(templates)
	if len(transitions) == 0 {
		return nil
	}

	scored := scoreTransitions(transitions, scoreParams{
		halfLife:     p.halfLife,
		alpha:        p.alpha,
		beta:         p.beta,
		gamma:        p.gamma,
		delta:        p.delta,
		epsilon:      p.epsilon,
		cwd:          state.CWD,
		priorOutcome: graph.Outcome(state.PriorOutcome),
		at:           at,
	})

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
