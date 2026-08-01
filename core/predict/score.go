package predict

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/types"
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
	priorOutcome types.Outcome // outcome of the most recent command
	at           time.Time
}

// ScoreBreakdown exposes the individual components that fed a transition's
// final score, so a surprising suggestion can be diagnosed (see
// Predictor.Explain) instead of taken on faith. Fields default to their
// identity value (0 for affinities/rates, 1 for DecayWeight would need no
// decay) when the corresponding boost is disabled or its signal absent.
type ScoreBreakdown struct {
	Next  string // the suggested template
	Count int    // raw observation count backing this transition

	DecayWeight   float64 // time-decay factor in (0, 1]; 1 = just observed
	CWDAffinity   float64 // fraction of observations in this/an ancestor dir, in [0, 1]
	PriorAffinity float64 // fraction that followed the same prior-command outcome, in [0, 1]
	AcceptRate    float64 // fraction the user actually accepted when shown, in [0, 1]
	FailureRate   float64 // fraction of times Next itself failed, in [0, 1]

	EffCount float64 // count * decay * all multiplicative boosts, pre-smoothing
	Score    float64 // final (eff + alpha) / (total + alpha*N); what ranking uses
}

// scoreTransitionsDetailed is the scoring engine: it applies the
// additive-smoothed decay formula with soft multiplicative adjustments for
// working-directory affinity, prior-command outcome, acceptance history, and
// the suggestion's own failure rate.
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
// terms - the additions never penalize cross-directory or unobserved cases.
// Additive smoothing still prevents cold-start collapse and bounds scores to
// (0, 1]. Results are sorted descending by score, then count, then next.
func scoreTransitionsDetailed(transitions []graph.Transition, p scoreParams) []ScoreBreakdown {
	if len(transitions) == 0 {
		return nil
	}

	breakdowns := make([]ScoreBreakdown, len(transitions))
	var total float64

	for i, t := range transitions {
		age := p.at.Sub(t.LastSeen)
		// True half-life: weight is exactly 0.5 at age == halfLife.
		weight := math.Exp(-math.Ln2 * float64(age) / float64(p.halfLife))
		eff := float64(t.Count) * weight

		b := ScoreBreakdown{Next: t.Next, Count: t.Count, DecayWeight: weight}

		if p.beta > 0 {
			b.CWDAffinity = cwdAffinity(t.CWDs, t.Count, p.cwd)
			eff *= 1 + p.beta*b.CWDAffinity
		}
		if p.delta > 0 && p.priorOutcome != types.OutcomeUnknown {
			b.PriorAffinity = priorAffinity(t, p.priorOutcome)
			eff *= 1 + p.delta*b.PriorAffinity
		}
		if p.epsilon > 0 && t.Count > 0 {
			rate := float64(t.Accepted) / float64(t.Count)
			if rate > 1 {
				rate = 1
			}
			b.AcceptRate = rate
			eff *= 1 + p.epsilon*rate
		}
		if p.gamma > 0 {
			b.FailureRate = failureRate(t)
			eff *= 1 - p.gamma*b.FailureRate
		}

		b.EffCount = eff
		breakdowns[i] = b
		total += eff
	}

	n := len(transitions)
	denom := total + p.alpha*float64(n)
	if denom <= 0 {
		return nil
	}
	for i := range breakdowns {
		breakdowns[i].Score = (breakdowns[i].EffCount + p.alpha) / denom
	}

	sort.Slice(breakdowns, func(i, j int) bool {
		if breakdowns[i].Score != breakdowns[j].Score {
			return breakdowns[i].Score > breakdowns[j].Score
		}
		if breakdowns[i].Count != breakdowns[j].Count {
			return breakdowns[i].Count > breakdowns[j].Count
		}
		return breakdowns[i].Next < breakdowns[j].Next
	})
	return breakdowns
}

// scoreTransitions is scoreTransitionsDetailed narrowed to what Predict
// needs, so the ranking formula has exactly one implementation.
func scoreTransitions(transitions []graph.Transition, p scoreParams) []scoredTransition {
	detailed := scoreTransitionsDetailed(transitions, p)
	if detailed == nil {
		return nil
	}
	result := make([]scoredTransition, len(detailed))
	for i, b := range detailed {
		result[i] = scoredTransition{next: b.Next, count: b.Count, score: b.Score}
	}
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
func priorAffinity(t graph.Transition, prior types.Outcome) float64 {
	if t.Count == 0 {
		return 0
	}
	var match int
	switch prior {
	case types.OutcomeSuccess:
		match = t.PriorSuccess
	case types.OutcomeFailure:
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
