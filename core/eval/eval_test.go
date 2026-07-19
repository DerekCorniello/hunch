package eval

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func testOptions(warmup int) Options {
	opts := DefaultOptions()
	opts.Warmup = warmup
	return opts
}

// A strictly repeating workflow is the easiest thing a Markov model can
// learn, so anything less than near-perfect prediction here is a real defect.
func TestRunLearnsARepeatingCycle(t *testing.T) {
	var history []string
	for i := 0; i < 200; i++ {
		history = append(history, "git status", "git add PATH", "git commit FLAG STR", "git push")
	}

	r := Run(history, testOptions(20))
	if r.Scored == 0 {
		t.Fatal("nothing was scored")
	}
	if got := r.Rate(r.Top1); got < 0.95 {
		t.Errorf("top-1 = %.1f%% on a fully deterministic cycle, want >= 95%%", 100*got)
	}
	if r.Offered != r.Scored {
		t.Errorf("offered %d of %d; a learned cycle should always have a suggestion", r.Offered, r.Scored)
	}
}

// Input with no structure to learn should collapse toward chance. This is the
// guard against a scoring change that manufactures signal out of noise. The
// sequence is drawn from a fixed seed so the test stays deterministic.
func TestRunFindsNoSignalInUnpredictableInput(t *testing.T) {
	const vocab = 40
	rng := rand.New(rand.NewSource(1))

	history := make([]string, 4000)
	for i := range history {
		history[i] = fmt.Sprintf("cmd%02d", rng.Intn(vocab))
	}

	// Every command is equally likely regardless of what preceded it, so
	// top-1 should sit near 1/vocab (2.5%). Allow generous headroom for
	// smoothing and small-sample noise while still failing loudly if the
	// model starts claiming real predictive power.
	r := Run(history, testOptions(50))
	if got := r.Rate(r.Top1); got > 0.15 {
		t.Errorf("top-1 = %.1f%% on structureless input, want near %.1f%%", 100*got, 100.0/vocab)
	}
}

func TestRunRespectsWarmup(t *testing.T) {
	history := make([]string, 100)
	for i := range history {
		history[i] = "ls"
	}

	for _, warmup := range []int{0, 10, 50} {
		r := Run(history, testOptions(warmup))
		if want := len(history) - warmup; r.Scored != want {
			t.Errorf("warmup %d: scored %d, want %d", warmup, r.Scored, want)
		}
	}
}

func TestRunSkipsEmptyTemplates(t *testing.T) {
	history := []string{"ls", "", "ls", "", "ls", "ls"}
	r := Run(history, testOptions(0))

	if want := 4; r.Scored != want {
		t.Errorf("scored %d, want %d (empty templates are not scored)", r.Scored, want)
	}
}

// Top-1 hits must also count as top-3 and top-5, or the rates can invert.
func TestRunRanksAreMonotonic(t *testing.T) {
	var history []string
	for i := 0; i < 100; i++ {
		history = append(history, "a", "b", "c", "d", "e", "f")
	}

	r := Run(history, testOptions(10))
	if r.Top1 > r.Top3 || r.Top3 > r.Top5 {
		t.Errorf("ranks not monotonic: top1=%d top3=%d top5=%d", r.Top1, r.Top3, r.Top5)
	}
	if r.Top5 > r.Scored {
		t.Errorf("top5=%d exceeds scored=%d", r.Top5, r.Scored)
	}
}

// The baseline is what any accuracy claim has to beat, so it must actually
// track the most frequent command rather than being a constant.
func TestRunBaselineTracksMostFrequentCommand(t *testing.T) {
	var history []string
	for i := 0; i < 100; i++ {
		history = append(history, "dominant")
		if i%10 == 0 {
			history = append(history, "rare")
		}
	}

	r := Run(history, testOptions(20))
	rate := r.Rate(r.BaselineTop1)
	if rate < 0.8 {
		t.Errorf("baseline = %.1f%%, want >= 80%% when one command dominates", 100*rate)
	}
}

func TestRunIsDeterministic(t *testing.T) {
	var history []string
	for i := 0; i < 50; i++ {
		history = append(history, "make build", "make test", "git commit FLAG STR")
	}

	first := Run(history, testOptions(10))
	for i := 0; i < 3; i++ {
		if got := Run(history, testOptions(10)); got != first {
			t.Fatalf("run %d differed: %+v vs %+v", i, got, first)
		}
	}
}

func TestRunHandlesShortAndEmptyHistories(t *testing.T) {
	tests := []struct {
		name    string
		history []string
		warmup  int
	}{
		{name: "empty", history: nil, warmup: 0},
		{name: "shorter than warmup", history: []string{"ls", "cd PATH"}, warmup: 50},
		{name: "single command", history: []string{"ls"}, warmup: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Run(tt.history, testOptions(tt.warmup))
			if r.Scored < 0 || r.Top1 > r.Scored {
				t.Errorf("nonsensical result: %+v", r)
			}
		})
	}
}

func TestRateIsZeroWhenNothingScored(t *testing.T) {
	var r Result
	if got := r.Rate(r.Top1); got != 0 {
		t.Errorf("Rate on an empty result = %v, want 0", got)
	}
}

func TestDefaultOptionsMatchDaemonDefaults(t *testing.T) {
	// These mirror the daemon's shipped configuration. If the daemon's
	// defaults change, eval stops measuring what users experience.
	opts := DefaultOptions()
	if opts.HalfLife != 720*time.Hour {
		t.Errorf("HalfLife = %v, want 720h", opts.HalfLife)
	}
	if opts.Alpha != 0.5 || opts.Beta != 0.75 || opts.Gamma != 0.5 || opts.Delta != 0.5 || opts.Epsilon != 0.5 {
		t.Errorf("scoring constants drifted from the daemon defaults: %+v", opts)
	}
}
