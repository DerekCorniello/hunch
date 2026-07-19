package cli

import (
	"strings"
	"testing"
)

// An imported graph has to support the same fallbacks as a learned one. When
// import recorded only the exact two-command context, almost every transition
// it produced had a count of 1, so the evidence threshold filtered nearly all
// of them and a freshly imported history yielded very few suggestions.
func TestBuildTransitionsRecordsGeneralizedContexts(t *testing.T) {
	// "make test" always follows "cd proj", but the pair before it varies, so
	// only the shortened context accumulates evidence.
	history := []string{
		"vim PATH", "cd PATH", "make STR",
		"git status", "cd PATH", "make STR",
		"ls", "cd PATH", "make STR",
	}

	transitions := buildTransitions(history)

	var exact, general int
	for _, tr := range transitions {
		if tr.Next != "make STR" {
			continue
		}
		switch len(nonEmpty(tr.State)) {
		case 2:
			exact++
		case 1:
			if tr.State[len(tr.State)-1] == "cd PATH" {
				general = tr.Count
			}
		}
	}

	if general < 3 {
		t.Errorf("shortened context ['cd PATH'] has count %d, want 3; the "+
			"generalization is what makes repeated workflows visible", general)
	}
	if exact == 0 {
		t.Error("the exact context should still be recorded")
	}
}

// Raw examples must expand the same way, or a prediction that arrives through
// a generalization has no concrete command and is suppressed as an unrunnable
// template.
func TestBuildRawMappingsRecordsGeneralizedContexts(t *testing.T) {
	raw := []string{"vim main.go", "cd proj", "make test"}
	normalized := []string{"vim PATH", "cd PATH", "make STR"}

	var foundGeneral bool
	for _, ex := range buildRawMappings(raw, normalized) {
		if ex.Template == "make STR" && ex.Raw == "make test" && len(ex.State) == 1 && ex.State[0] == "cd PATH" {
			foundGeneral = true
		}
	}
	if !foundGeneral {
		t.Error("no raw example keyed to the shortened context ['cd PATH']")
	}
}

func TestNonEmpty(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"", "a"}, "a"},
		{[]string{"a", "b"}, "a,b"},
		{[]string{"", ""}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := strings.Join(nonEmpty(tt.in), ","); got != tt.want {
			t.Errorf("nonEmpty(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
