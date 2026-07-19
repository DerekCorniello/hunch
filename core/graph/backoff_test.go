package graph

import (
	"strings"
	"testing"
)

// The generalizations are what make the prediction fallbacks reachable at all:
// state keys compare by exact join, so a narrower query only matches if the
// narrower key was actually recorded.
func TestBackoffStates(t *testing.T) {
	tests := []struct {
		name   string
		state  []string
		hasCWD bool
		want   [][]string
	}{
		{
			name:   "no cwd, two prior commands",
			state:  []string{"git add PATH", "git commit"},
			hasCWD: false,
			want: [][]string{
				{"git add PATH", "git commit"},
				{"git commit"},
			},
		},
		{
			name:   "with cwd, two prior commands",
			state:  []string{"/proj", "git add PATH", "git commit"},
			hasCWD: true,
			want: [][]string{
				{"/proj", "git add PATH", "git commit"},
				{"git add PATH", "git commit"},
				{"git commit"},
			},
		},
		{
			name:   "with cwd, no prior commands",
			state:  []string{"/proj"},
			hasCWD: true,
			want:   [][]string{{"/proj"}},
		},
		{
			name:   "no cwd, single prior command",
			state:  []string{"git commit"},
			hasCWD: false,
			want:   [][]string{{"git commit"}},
		},
		{
			// Session start: padding is all empty, so the shorter key carries
			// no information and is not worth a row.
			name:   "empty padding is not recorded",
			state:  []string{"", ""},
			hasCWD: false,
			want:   [][]string{{"", ""}},
		},
		{
			name:   "leading empty padding still generalizes",
			state:  []string{"", "git commit"},
			hasCWD: false,
			want: [][]string{
				{"", "git commit"},
				{"git commit"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BackoffStates(tt.state, tt.hasCWD)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d states %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if strings.Join(got[i], "\x00") != strings.Join(tt.want[i], "\x00") {
					t.Errorf("state %d = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Merge rejects transitions with empty state, so a generalization must never
// be an empty key. The exact context at index 0 is exempt: it is passed
// through as the caller supplied it, preserving existing behavior, and an
// empty one is harmless because stateKey joins it to "" which reads back as
// a one-element [""].
func TestBackoffGeneralizationsAreNeverEmpty(t *testing.T) {
	inputs := []struct {
		state  []string
		hasCWD bool
	}{
		{[]string{"/proj"}, true},
		{[]string{"/proj", "ls"}, true},
		{[]string{"/proj", "", ""}, true},
		{[]string{"ls"}, false},
		{[]string{"", ""}, false},
		{nil, false},
	}

	for _, in := range inputs {
		got := BackoffStates(in.state, in.hasCWD)
		for i, state := range got[1:] {
			if len(state) == 0 {
				t.Errorf("BackoffStates(%v, %v) generalization %d is an empty key", in.state, in.hasCWD, i+1)
			}
			if allEmpty(state) {
				t.Errorf("BackoffStates(%v, %v) generalization %d carries no context: %v", in.state, in.hasCWD, i+1, state)
			}
		}
	}
}

func TestBackoffStatesKeepsExactContextFirst(t *testing.T) {
	state := []string{"/proj", "a", "b"}
	got := BackoffStates(state, true)
	if len(got) == 0 || strings.Join(got[0], "\x00") != strings.Join(state, "\x00") {
		t.Errorf("first state = %v, want the exact context %v", got[0], state)
	}
}

func TestAllEmpty(t *testing.T) {
	tests := []struct {
		state []string
		want  bool
	}{
		{nil, true},
		{[]string{}, true},
		{[]string{""}, true},
		{[]string{"", ""}, true},
		{[]string{"", "ls"}, false},
		{[]string{"ls"}, false},
	}
	for _, tt := range tests {
		if got := allEmpty(tt.state); got != tt.want {
			t.Errorf("allEmpty(%v) = %v, want %v", tt.state, got, tt.want)
		}
	}
}
