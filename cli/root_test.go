package cli

import (
	"strings"
	"testing"
)

func TestRunVersionAliases(t *testing.T) {
	// "version" was added alongside the flags because it is what people
	// reach for first; all three must stay in sync.
	for _, arg := range []string{"version", "--version", "-v"} {
		if err := Run([]string{arg}); err != nil {
			t.Errorf("Run(%q) returned error: %v", arg, err)
		}
	}
}

func TestRunHelpAliases(t *testing.T) {
	for _, args := range [][]string{{}, {"--help"}, {"-h"}} {
		if err := Run(args); err != nil {
			t.Errorf("Run(%v) returned error: %v", args, err)
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	err := Run([]string{"definitely-not-a-command"})
	if err == nil {
		t.Fatal("expected an error for an unknown command")
	}
	if !strings.Contains(err.Error(), "definitely-not-a-command") {
		t.Errorf("error should name the bad command, got %q", err)
	}
	if !strings.Contains(err.Error(), "Usage:") {
		t.Errorf("error should include usage text, got %q", err)
	}
}

// Every user-facing command must be named in the help. `client` and `predict`
// are deliberately excluded: `client` is the raw protocol the shell
// integration speaks to the daemon, and `predict` is a debugging alias for it,
// so neither belongs in help a person reads. Both still dispatch. Commands are
// not invoked here: several (uninstall, reset, daemon) act on real user state.
func TestUsageTextMentionsUserCommands(t *testing.T) {
	documented := []string{
		"init", "daemon", "import-history", "uninstall",
		"doctor", "update", "version", "stats", "eval", "reset",
	}
	usage := usageText()
	for _, cmd := range documented {
		if !strings.Contains(usage, cmd) {
			t.Errorf("usage text does not mention %q", cmd)
		}
	}

	// The hidden commands must still be handled by Run, just not advertised.
	for _, hidden := range []string{"client", "predict"} {
		if err := Run([]string{hidden}); err != nil && strings.Contains(err.Error(), "unknown") {
			t.Errorf("%q is hidden from help but no longer dispatches", hidden)
		}
	}
}
