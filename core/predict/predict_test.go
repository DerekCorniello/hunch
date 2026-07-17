package predict

import (
	"testing"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/types"
)

func TestPredictEmptyGraphReturnsNil(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	state := types.State{
		Previous: []types.Command{
			{Template: "git add PATH"},
		},
	}
	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)

	suggestions := p.Predict(state, now, 0)
	if suggestions != nil {
		t.Errorf("Predict on empty graph = %v, want nil", suggestions)
	}
}

func TestPredictEmptyPreviousOnEmptyGraphReturnsNil(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)

	suggestions := p.Predict(types.State{}, now, 0)
	if suggestions != nil {
		t.Errorf("Predict on empty state with empty graph = %v, want nil", suggestions)
	}
}

func TestPredictSingleTransition(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	g.Record([]string{"", "git add PATH"}, "git commit FLAG STR", now)

	state := types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "git add PATH"},
		},
	}

	suggestions := p.Predict(state, now, 0)
	if len(suggestions) != 1 {
		t.Fatalf("Predict returned %d suggestions, want 1", len(suggestions))
	}
	if suggestions[0].Template != "git commit FLAG STR" {
		t.Errorf("Template = %q, want %q", suggestions[0].Template, "git commit FLAG STR")
	}
	if suggestions[0].Score <= 0 {
		t.Errorf("Score = %f, want > 0", suggestions[0].Score)
	}
	if suggestions[0].Count != 1 {
		t.Errorf("Count = %d, want 1", suggestions[0].Count)
	}
}

func TestPredictMultipleTransitionsRankedByScore(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "git add PATH"}

	g.Record(state, "git commit FLAG STR", now)
	g.Record(state, "git push STR", now)
	g.Record(state, "git push STR", now)

	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "git add PATH"},
		},
	}, now, 0)

	if len(suggestions) != 2 {
		t.Fatalf("Predict returned %d suggestions, want 2", len(suggestions))
	}

	if suggestions[0].Template != "git push STR" {
		t.Errorf("Top suggestion = %q, want %q", suggestions[0].Template, "git push STR")
	}
	if suggestions[0].Score < suggestions[1].Score {
		t.Errorf("Top score %f < second score %f", suggestions[0].Score, suggestions[1].Score)
	}
}

func TestPredictOlderTransitionsRankLower(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	state := []string{"", "cmd"}

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)

	g.Record(state, "old-cmd", old)
	g.Record(state, "recent-cmd", recent)

	queryAt := time.Date(2025, 12, 15, 0, 0, 0, 0, time.UTC)
	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, queryAt, 0)

	if len(suggestions) != 2 {
		t.Fatalf("Predict returned %d suggestions, want 2", len(suggestions))
	}

	if suggestions[0].Template != "recent-cmd" {
		t.Errorf("Top suggestion = %q, want %q", suggestions[0].Template, "recent-cmd")
	}
}

func TestPredictLimitTruncates(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}
	g.Record(state, "a", now)
	g.Record(state, "b", now)
	g.Record(state, "c", now)

	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, now, 2)

	if len(suggestions) != 2 {
		t.Errorf("Predict with limit 2 returned %d, want 2", len(suggestions))
	}

	all := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, now, 0)

	if len(all) != 3 {
		t.Errorf("Predict with limit 0 returned %d, want 3", len(all))
	}
}

func TestPredictSmoothingTieBreakerDeterministic(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}
	g.Record(state, "alpha", now)
	g.Record(state, "beta", now)

	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, now, 0)

	if len(suggestions) != 2 {
		t.Fatalf("Predict returned %d, want 2", len(suggestions))
	}

	expectedScore := (1.0 + 0.5) / (2.0 + 0.5*2.0)
	for i, s := range suggestions {
		if s.Score != expectedScore {
			t.Errorf("Suggestion %d score = %f, want %f", i, s.Score, expectedScore)
		}
	}

	for range 50 {
		got := p.Predict(types.State{
			Previous: []types.Command{
				{Template: ""},
				{Template: "cmd"},
			},
		}, now, 0)
		if got[0].Template != suggestions[0].Template {
			t.Fatalf("Tie-breaker non-deterministic: first run had %q first, later had %q first",
				suggestions[0].Template, got[0].Template)
		}
	}
}

func TestPredictDifferentStatesNoCrossContamination(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	g.Record([]string{"", "git add PATH"}, "git commit FLAG STR", now)
	g.Record([]string{"git commit FLAG STR", "git push STR"}, "gh pr STR FLAG STR", now)

	stateA := types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "git add PATH"},
		},
	}
	stateB := types.State{
		Previous: []types.Command{
			{Template: "git commit FLAG STR"},
			{Template: "git push STR"},
		},
	}

	gotA := p.Predict(stateA, now, 0)
	gotB := p.Predict(stateB, now, 0)

	if len(gotA) != 1 || gotA[0].Template != "git commit FLAG STR" {
		t.Errorf("State A predictions wrong: %v", gotA)
	}
	if len(gotB) != 1 || gotB[0].Template != "gh pr STR FLAG STR" {
		t.Errorf("State B predictions wrong: %v", gotB)
	}
}

// windowState builds the two-command-window state used across the
// boost/suppression tests.
func windowState(cwd string, prior types.Outcome) types.State {
	return types.State{
		Previous:     []types.Command{{Template: ""}, {Template: "cmd"}},
		CWD:          cwd,
		PriorOutcome: prior,
	}
}

func TestPredictCWDStateKey(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 1.0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)

	// "ls" recorded without CWD (general state key).
	general := []string{"", "cmd"}
	g.RecordObs(graph.Observation{State: general, Next: "ls", At: now})
	g.RecordObs(graph.Observation{State: general, Next: "ls", At: now})
	g.RecordObs(graph.Observation{State: general, Next: "ls", At: now})

	// "make" recorded WITH CWD "/proj" — state key includes CWD.
	withCWD := []string{"/proj", "", "cmd"}
	for range 2 {
		g.RecordObs(graph.Observation{State: withCWD, Next: "make", At: now})
	}

	// Without CWD: general key ["", "cmd"] → "ls" wins.
	noCWD := p.Predict(windowState("", types.OutcomeUnknown), now, 0)
	if noCWD[0].Template != "ls" {
		t.Fatalf("without CWD, top = %q, want ls", noCWD[0].Template)
	}

	// With CWD "/proj": state key ["/proj", "", "cmd"] → "make".
	inProj := p.Predict(windowState("/proj", types.OutcomeUnknown), now, 0)
	if inProj[0].Template != "make" {
		t.Errorf("in /proj, top = %q, want make (CWD state key)", inProj[0].Template)
	}

	// With CWD "/other": no CWD-specific data → no match (fallback is
	// handled by the daemon, not the core Predict).
	inOther := p.Predict(windowState("/other", types.OutcomeUnknown), now, 0)
	if inOther != nil {
		t.Errorf("in /other, expected nil (no data for this CWD), got %v", inOther)
	}
}

func TestPredictFailureSuppression(t *testing.T) {
	g := graph.New(2)
	// gamma>0 so failing suggestions are suppressed.
	p := New(g, 720*time.Hour, 0.5, 0, 1.0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}

	// "flaky" is seen more often but almost always fails; "good" less often
	// but always succeeds.
	for range 4 {
		g.RecordObs(graph.Observation{State: state, Next: "flaky", At: now, NextOutcome: graph.OutcomeFailure})
	}
	for range 3 {
		g.RecordObs(graph.Observation{State: state, Next: "good", At: now, NextOutcome: graph.OutcomeSuccess})
	}

	got := p.Predict(windowState("", types.OutcomeUnknown), now, 0)
	if got[0].Template != "good" {
		t.Errorf("top = %q, want good (flaky suppressed by failure rate)", got[0].Template)
	}
}

func TestPredictPriorOutcomeBoost(t *testing.T) {
	g := graph.New(2)
	// delta>0 so prior-outcome context boosts matching transitions.
	p := New(g, 720*time.Hour, 0.5, 0, 0, 1.0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}

	// "retry" usually follows a failed prior command; "next-step" usually
	// follows a successful one. Equal overall counts.
	for range 3 {
		g.RecordObs(graph.Observation{State: state, Next: "retry", At: now, PriorOutcome: graph.OutcomeFailure})
		g.RecordObs(graph.Observation{State: state, Next: "next-step", At: now, PriorOutcome: graph.OutcomeSuccess})
	}

	afterFail := p.Predict(windowState("", types.OutcomeFailure), now, 0)
	if afterFail[0].Template != "retry" {
		t.Errorf("after failure, top = %q, want retry", afterFail[0].Template)
	}
	afterSuccess := p.Predict(windowState("", types.OutcomeSuccess), now, 0)
	if afterSuccess[0].Template != "next-step" {
		t.Errorf("after success, top = %q, want next-step", afterSuccess[0].Template)
	}
}

func TestPredictAcceptanceBoost(t *testing.T) {
	g := graph.New(2)
	// epsilon>0 so confirmed-acceptance boosts matching transitions.
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 1.0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}

	// Both seen 3 times; "chosen" was accepted every time, "ignored" never.
	for range 3 {
		g.RecordObs(graph.Observation{State: state, Next: "chosen", At: now, Accepted: true})
		g.RecordObs(graph.Observation{State: state, Next: "ignored", At: now})
	}

	got := p.Predict(windowState("", types.OutcomeUnknown), now, 0)
	if got[0].Template != "chosen" {
		t.Errorf("top = %q, want chosen (acceptance boost)", got[0].Template)
	}
}

func TestPredictBoostsKeepScoreBounded(t *testing.T) {
	g := graph.New(2)
	// All boosts active at full strength.
	p := New(g, 720*time.Hour, 0.5, 1.0, 1.0, 1.0, 1.0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	// Record with CWD in the state key so the CWD-augmented lookup finds it.
	state := []string{"/proj", "", "cmd"}
	g.RecordObs(graph.Observation{State: state, Next: "only", At: now, CWD: "/proj", NextOutcome: graph.OutcomeSuccess, PriorOutcome: graph.OutcomeFailure, Accepted: true})

	got := p.Predict(windowState("/proj", types.OutcomeFailure), now, 0)
	if len(got) != 1 {
		t.Fatalf("got %d suggestions, want 1", len(got))
	}
	if got[0].Score <= 0 || got[0].Score > 1 {
		t.Errorf("score %f not in (0, 1] with all boosts active", got[0].Score)
	}
}

func TestPredictScoreBounds(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}
	g.Record(state, "only-one", now)

	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, now, 0)

	if len(suggestions) != 1 {
		t.Fatalf("Predict returned %d, want 1", len(suggestions))
	}

	if suggestions[0].Score <= 0 || suggestions[0].Score > 1 {
		t.Errorf("Score %f not in (0, 1]", suggestions[0].Score)
	}
}

func TestPredictWithLimitOne(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}
	g.Record(state, "top-cmd", now)
	g.Record(state, "second-cmd", now)

	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, now, 1)

	if len(suggestions) != 1 {
		t.Errorf("Predict with limit 1 returned %d, want 1", len(suggestions))
	}
}

func TestPredictZeroAlpha(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}
	g.Record(state, "next", now)

	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, now, 0)

	if len(suggestions) != 1 {
		t.Fatalf("Predict returned %d, want 1", len(suggestions))
	}
	if suggestions[0].Score <= 0 || suggestions[0].Score > 1 {
		t.Errorf("Score %f with zero alpha not in (0, 1]", suggestions[0].Score)
	}
}

func TestPredictNearZeroHalfLife(t *testing.T) {
	g := graph.New(2)
	p := New(g, 1*time.Nanosecond, 0.5, 0, 0, 0, 0)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}
	g.Record(state, "next", now)

	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, now, 0)

	if len(suggestions) != 1 {
		t.Fatalf("Predict returned %d, want 1", len(suggestions))
	}
	if suggestions[0].Score <= 0 || suggestions[0].Score > 1 {
		t.Errorf("Score %f with near-zero halfLife not in (0, 1]", suggestions[0].Score)
	}
}

func TestPredictFutureAt(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5, 0, 0, 0, 0)

	past := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	state := []string{"", "cmd"}
	g.Record(state, "next", past)

	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, future, 0)

	if len(suggestions) != 1 {
		t.Fatalf("Predict with future at returned %d, want 1", len(suggestions))
	}
}
