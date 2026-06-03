package predict

import (
	"testing"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/types"
)

func TestPredictEmptyGraphReturnsNil(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5)

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
	p := New(g, 720*time.Hour, 0.5)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)

	suggestions := p.Predict(types.State{}, now, 0)
	if suggestions != nil {
		t.Errorf("Predict on empty state with empty graph = %v, want nil", suggestions)
	}
}

func TestPredictSingleTransition(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5)

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
	p := New(g, 720*time.Hour, 0.5)

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
	p := New(g, 720*time.Hour, 0.5)

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
	p := New(g, 720*time.Hour, 0.5)

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
	p := New(g, 720*time.Hour, 0.5)

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
	p := New(g, 720*time.Hour, 0.5)

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

func TestPredictCWDDoesNotAffectScore(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}
	g.Record(state, "next", now)

	withCWD := types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
		CWD: "/some/deep/path",
	}
	withoutCWD := types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}

	got1 := p.Predict(withCWD, now, 0)
	got2 := p.Predict(withoutCWD, now, 0)

	if len(got1) != len(got2) {
		t.Fatalf("Different suggestion count with/without CWD: %d vs %d", len(got1), len(got2))
	}
	for i := range got1 {
		if got1[i].Score != got2[i].Score {
			t.Errorf("Score differs with CWD at index %d: %f vs %f", i, got1[i].Score, got2[i].Score)
		}
	}
}

func TestPredictOutcomeDoesNotAffectScore(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5)

	now := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	state := []string{"", "cmd"}

	g.Record(state, "success-cmd", now)

	suggestions := p.Predict(types.State{
		Previous: []types.Command{
			{Template: ""},
			{Template: "cmd"},
		},
	}, now, 0)

	for _, s := range suggestions {
		if s.Count != 1 {
			t.Errorf("Outcome leak: count = %d, want 1 (all transitions count equally)", s.Count)
		}
	}
}

func TestPredictScoreBounds(t *testing.T) {
	g := graph.New(2)
	p := New(g, 720*time.Hour, 0.5)

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
	p := New(g, 720*time.Hour, 0.5)

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
