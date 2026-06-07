package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DerekCorniello/hunch/daemon"
)

// startTestDaemon starts a daemon with isolated temp paths and returns the
// socket path and a cancel function. It sets HUNCH_SOCKET and HUNCH_DB_PATH
// so that CLI commands can discover the daemon via env.
func startTestDaemon(t *testing.T) (string, func()) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "hunch.sock")
	dbPath := filepath.Join(t.TempDir(), "hunch.db")

	t.Setenv("HUNCH_SOCKET", socket)
	t.Setenv("HUNCH_DB_PATH", dbPath)

	opts := daemon.LoadConfig()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := daemon.Run(ctx, opts); err != nil && ctx.Err() == nil {
			t.Logf("daemon error: %v", err)
		}
	}()

	// Wait for socket to appear.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(opts.Socket); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	return opts.Socket, func() {
		cancel()
		time.Sleep(100 * time.Millisecond)
	}
}

func TestVersion(t *testing.T) {
	orig := Version
	Version = "test-version-1.0"
	defer func() { Version = orig }()

	err := Run([]string{"--version"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVersionShortFlag(t *testing.T) {
	orig := Version
	Version = "test-version-1.0"
	defer func() { Version = orig }()

	err := Run([]string{"-v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHelp(t *testing.T) {
	err := Run([]string{"--help"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitPrintsSourceLine(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			err := Run([]string{"init", shell})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestInitUnknownShell(t *testing.T) {
	err := Run([]string{"init", "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown shell")
	}
	if !strings.Contains(err.Error(), "unknown shell") {
		t.Errorf("error = %q, want 'unknown shell'", err)
	}
}

func TestInitMissingShell(t *testing.T) {
	err := Run([]string{"init"})
	if err == nil {
		t.Fatal("expected error for missing shell")
	}
}

func TestUnknownCommand(t *testing.T) {
	err := Run([]string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestClientRecordRoundtrip(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	// Record a transition.
	err := Run([]string{"client", "record", "--state", ",git add PATH", "--next", "git commit FLAG STR"})
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}

	// Predict it back.
	err = Run([]string{"client", "predict", "--state", ",git add PATH", "--limit", "5"})
	if err != nil {
		t.Fatalf("predict failed: %v", err)
	}

	// Export.
	err = Run([]string{"client", "export"})
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
}

func TestClientReset(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	// Record.
	err := Run([]string{"client", "record", "--state", "", "--next", "some-cmd"})
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}

	// Reset.
	err = Run([]string{"client", "reset"})
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}

	// Export should show empty.
	err = Run([]string{"client", "export"})
	if err != nil {
		t.Fatalf("export after reset failed: %v", err)
	}
}

func TestClientExportEmpty(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "export"})
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
}

func TestClientDaemonNotRunning(t *testing.T) {
	t.Setenv("HUNCH_SOCKET", filepath.Join(t.TempDir(), "nonexistent.sock"))

	err := Run([]string{"client", "export"})
	if err == nil {
		t.Fatal("expected error when daemon is not running")
	}
	if !strings.Contains(err.Error(), "connect to daemon") {
		t.Errorf("error = %q, want to contain 'connect to daemon'", err)
	}
}

func TestPredictFiltersByPrefix(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "record", "--state", ",cmd", "--next", "git push origin main"})
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}

	err = Run([]string{"client", "predict", "--state", ",cmd", "--prefix", "git", "--limit", "3"})
	if err != nil {
		t.Fatalf("predict with prefix failed: %v", err)
	}
}

func TestDaemonStatus(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"daemon", "status"})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
}
