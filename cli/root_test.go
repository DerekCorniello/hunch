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

// The usage text is the only place commands are advertised, so it must at
// least name every one the dispatcher handles. Commands are not invoked here:
// several (uninstall, reset, daemon) act on real user state.
func TestUsageTextMentionsEveryCommand(t *testing.T) {
	documented := []string{
		"init", "daemon", "client", "import-history", "uninstall",
		"doctor", "update", "version", "stats", "predict", "reset",
	}
	usage := usageText()
	for _, cmd := range documented {
		if !strings.Contains(usage, cmd) {
			t.Errorf("usage text does not mention %q", cmd)
		}
	}
}
