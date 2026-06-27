package predict

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
)

// scoredTransition pairs a graph transition with its computed score.
type scoredTransition struct {
	next  string
	count int
	score float64
}

// scoreParams bundles the scoring constants and query context.
type scoreParams struct {
	halfLife     time.Duration
	alpha        float64       // additive smoothing
	beta         float64       // CWD-affinity boost strength
	gamma        float64       // failure-rate suppression strength
	delta        float64       // prior-outcome boost strength
	epsilon      float64       // confirmed-acceptance boost strength
	cwd          string        // query working directory ("" if unknown)
	priorOutcome graph.Outcome // outcome of the most recent command
	at           time.Time
}

// scoreTransitions applies the additive-smoothed decay formula with soft
// multiplicative adjustments for working-directory affinity, prior-command
// outcome, and the suggestion's own failure rate.
//
//	eff   = count * decay
//	      * (1 + beta    * cwdAffinity)      // boost same-directory habits
//	      * (1 + delta   * priorAffinity)    // boost prior-outcome context
//	      * (1 + epsilon * acceptRate)       // boost confirmed suggestions
//	      * (1 - gamma   * failureRate)      // suppress chronically-failing
//	score = (eff + alpha) / (total + alpha * N)
//
// Each adjustment is the identity (factor 1) when its signal is absent, so a
// transition with no CWD/outcome data ranks exactly as it would without these
// terms — the additions never penalize cross-directory or unobserved cases.
// Additive smoothing still prevents cold-start collapse and bounds scores to
// (0, 1]. Results are sorted descending by score, then count, then next.
func scoreTransitions(transitions []graph.Transition, p scoreParams) []scoredTransition {
	if len(transitions) == 0 {
		return nil
	}

	effCounts := make([]float64, len(transitions))
	var total float64

	for i, t := range transitions {
		age := p.at.Sub(t.LastSeen)
		// True half-life: weight is exactly 0.5 at age == halfLife.
		weight := math.Exp(-math.Ln2 * float64(age) / float64(p.halfLife))
		eff := float64(t.Count) * weight

		if p.beta > 0 {
			eff *= 1 + p.beta*cwdAffinity(t.CWDs, t.Count, p.cwd)
		}
		if p.delta > 0 && p.priorOutcome != graph.OutcomeUnknown {
			eff *= 1 + p.delta*priorAffinity(t, p.priorOutcome)
		}
		if p.epsilon > 0 && t.Count > 0 {
			rate := float64(t.Accepted) / float64(t.Count)
			if rate > 1 {
				rate = 1
			}
			eff *= 1 + p.epsilon*rate
		}
		if p.gamma > 0 {
			eff *= 1 - p.gamma*failureRate(t)
		}

		effCounts[i] = eff
		total += eff
	}

	n := len(transitions)
	denom := total + p.alpha*float64(n)
	if denom <= 0 {
		return nil
	}

	result := make([]scoredTransition, n)
	for i, t := range transitions {
		sc := (effCounts[i] + p.alpha) / denom
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

// cwdAffinity is the fraction of a transition's observations that occurred in
// the query directory or one of its ancestors, in [0, 1]. A workflow learned
// in ~/project therefore still boosts when the user is in ~/project/src.
func cwdAffinity(cwds map[string]int, count int, queryCWD string) float64 {
	if queryCWD == "" || count == 0 || len(cwds) == 0 {
		return 0
	}
	matched := 0
	for cwd, c := range cwds {
		if cwd == queryCWD || strings.HasPrefix(queryCWD, cwd+"/") {
			matched += c
		}
	}
	if matched > count {
		matched = count
	}
	return float64(matched) / float64(count)
}

// priorAffinity is the fraction of a transition's observations that followed a
// prior command with the given outcome, in [0, 1].
func priorAffinity(t graph.Transition, prior graph.Outcome) float64 {
	if t.Count == 0 {
		return 0
	}
	var match int
	switch prior {
	case graph.OutcomeSuccess:
		match = t.PriorSuccess
	case graph.OutcomeFailure:
		match = t.PriorFailure
	default:
		return 0
	}
	if match > t.Count {
		match = t.Count
	}
	return float64(match) / float64(t.Count)
}

// failureRate is the fraction of a transition's next-command runs that failed,
// in [0, 1], or 0 when no outcome was ever recorded.
func failureRate(t graph.Transition) float64 {
	total := t.NextSuccess + t.NextFailure
	if total == 0 {
		return 0
	}
	return float64(t.NextFailure) / float64(total)
}
