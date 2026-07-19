package daemon

import (
	"testing"

	"github.com/DerekCorniello/hunch/core/types"
)

func TestWithMinCount(t *testing.T) {
	suggestions := []types.Suggestion{
		{Template: "a", Count: 5},
		{Template: "b", Count: 2},
		{Template: "c", Count: 1},
	}

	tests := []struct {
		name     string
		minCount int
		want     []string
	}{
		// A threshold of 1 or 0 disables the filter entirely.
		{name: "disabled at 0", minCount: 0, want: []string{"a", "b", "c"}},
		{name: "disabled at 1", minCount: 1, want: []string{"a", "b", "c"}},
		{name: "drops single observations", minCount: 2, want: []string{"a", "b"}},
		{name: "stricter threshold", minCount: 5, want: []string{"a"}},
		{name: "everything filtered", minCount: 99, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Copy: the filter reuses the backing array.
			input := append([]types.Suggestion(nil), suggestions...)
			got := withMinCount(input, tt.minCount)
			if len(got) != len(tt.want) {
				t.Fatalf("kept %d, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i].Template != tt.want[i] {
					t.Errorf("kept[%d] = %q, want %q", i, got[i].Template, tt.want[i])
				}
			}
		})
	}
}

func TestWithMinCountPreservesOrder(t *testing.T) {
	got := withMinCount([]types.Suggestion{
		{Template: "first", Count: 9},
		{Template: "skipped", Count: 1},
		{Template: "second", Count: 3},
	}, 2)

	if len(got) != 2 || got[0].Template != "first" || got[1].Template != "second" {
		t.Errorf("ranking order was not preserved: %+v", got)
	}
}

// A command run once in a context nobody revisits is the only candidate for
// that state, so additive smoothing scores it 1.0 and MinConfidence cannot
// help: it is maximally confident and almost entirely unevidenced. This is
// the case that put a one-off shell tweak into someone's ghost text.
func TestSingleObservationIsNotSuggested(t *testing.T) {
	opts := LoadConfig()
	opts.MinCount = 2
	_, _, socket := startDaemon(t, opts)

	state := []string{"hunch eval", "git status"}
	recordObs(t, socket, map[string]interface{}{
		"state": state, "next": "zstyle ':autosuggest:*' disabled yes",
	})

	if got := predictTop(t, socket, state, "", ""); got != "" {
		t.Errorf("a single observation was suggested as %q; want nothing", got)
	}

	// A second observation makes it a repeated habit, which is the whole
	// premise of the tool, so now it should be offered.
	recordObs(t, socket, map[string]interface{}{
		"state": state, "next": "zstyle ':autosuggest:*' disabled yes",
	})

	if got := predictTop(t, socket, state, "", ""); got != "zstyle STR" {
		t.Errorf("after two observations top = %q, want %q", got, "zstyle STR")
	}
}

func TestMinCountOfOneRestoresEveryOneOff(t *testing.T) {
	opts := LoadConfig()
	opts.MinCount = 1
	_, _, socket := startDaemon(t, opts)

	state := []string{"", "make"}
	recordObs(t, socket, map[string]interface{}{"state": state, "next": "rare command"})

	if got := predictTop(t, socket, state, "", ""); got != "rare STR" {
		t.Errorf("with MinCount=1 top = %q, want the single observation", got)
	}
}
