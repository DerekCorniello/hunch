package predict

import (
	"math"
	"sort"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
)

// scoredTransition pairs a graph transition with its computed score.
type scoredTransition struct {
	next  string
	count int
	score float64
}

// scoreTransitions applies the additive-smoothed decay formula.
//
//	score(t) = (effCount + alpha) / (total + alpha * N)
//
// Additive smoothing prevents cold-start collapse (a single observation
// gets a non-trivial score) and bounds all scores to (0, 1].
// Results are sorted descending by score, then descending by count,
// then ascending by next for a fully deterministic order.
func scoreTransitions(transitions []graph.Transition, halfLife time.Duration, alpha float64, at time.Time) []scoredTransition {
	if len(transitions) == 0 {
		return nil
	}

	effCounts := make([]float64, len(transitions))
	var total float64

	for i, t := range transitions {
		age := at.Sub(t.LastSeen)
		weight := math.Exp(-float64(age) / float64(halfLife))
		effCounts[i] = float64(t.Count) * weight
		total += effCounts[i]
	}

	n := len(transitions)
	denom := total + alpha*float64(n)

	result := make([]scoredTransition, n)
	for i, t := range transitions {
		sc := (effCounts[i] + alpha) / denom
		result[i] = scoredTransition{next: t.Next, count: t.Count, score: sc}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].score != result[j].score {
			return result[i].score > result[j].score
		}
		if result[i].count != result[j].count {
			return result[i].count > result[j].count
		}
		return result[i].next < result[j].next
	})
	return result
}
