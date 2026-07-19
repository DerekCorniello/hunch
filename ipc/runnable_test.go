package ipc

import "testing"

func TestDisplayCommandPrefersRaw(t *testing.T) {
	if got := DisplayCommand(`git commit -m "wip"`, "git commit FLAG STR"); got != `git commit -m "wip"` {
		t.Errorf("got %q, want the concrete command", got)
	}
}

// Showing a template with placeholders hands the user something they cannot
// run. Silence is the correct output.
func TestDisplayCommandSuppressesUnrunnableTemplates(t *testing.T) {
	for _, template := range []string{
		"git commit FLAG STR",
		"cd PATH",
		"vim PATH",
		"git checkout HASH",
		"sleep NUM",
		"git clone REPO",
		"cargo build KWARGS",
		"ls FLAG",
	} {
		if got := DisplayCommand("", template); got != "" {
			t.Errorf("DisplayCommand(\"\", %q) = %q, want \"\"", template, got)
		}
	}
}

// Templates that survived normalization unchanged are real commands and are
// safe to show when no raw was recorded.
func TestDisplayCommandAllowsLiteralTemplates(t *testing.T) {
	for _, template := range []string{
		"git status",
		"ls",
		"cargo run",
		"make test",
		"docker compose up",
	} {
		if got := DisplayCommand("", template); got != template {
			t.Errorf("DisplayCommand(\"\", %q) = %q, want the template", template, got)
		}
	}
}

func TestDisplayCommandEmptyInputs(t *testing.T) {
	if got := DisplayCommand("", ""); got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

// A command that legitimately contains a placeholder-like word as an argument
// value would already have been normalized away, but guard the obvious case
// that lowercase words are not treated as placeholders.
func TestDisplayCommandIsCaseSensitive(t *testing.T) {
	if got := DisplayCommand("", "echo path"); got != "echo path" {
		t.Errorf("got %q, want the lowercase word treated as literal", got)
	}
}
