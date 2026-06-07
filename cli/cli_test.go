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

func TestClientNormalize(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "normalize", "--cmd", "git commit -m \"init\""})
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
}

func TestClientNormalizeMissingCmd(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "normalize"})
	if err == nil {
		t.Fatal("expected error for missing --cmd")
	}
}

func TestClientStats(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "stats"})
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
}

func TestClientStatsNoDaemon(t *testing.T) {
	t.Setenv("HUNCH_SOCKET", filepath.Join(t.TempDir(), "nonexistent.sock"))

	err := Run([]string{"client", "stats"})
	if err == nil {
		t.Fatal("expected error when daemon is not running")
	}
}

func TestClientUnknownOp(t *testing.T) {
	err := Run([]string{"client", "badop"})
	if err == nil {
		t.Fatal("expected error for unknown client op")
	}
	if !strings.Contains(err.Error(), "unknown client op") {
		t.Errorf("error = %q, want to contain 'unknown client op'", err)
	}
}

func TestDaemonUnknownAction(t *testing.T) {
	err := Run([]string{"daemon", "badaction"})
	if err == nil {
		t.Fatal("expected error for unknown daemon action")
	}
	if !strings.Contains(err.Error(), "unknown daemon action") {
		t.Errorf("error = %q, want to contain 'unknown daemon action'", err)
	}
}

func TestClientSendRequestDaemonError(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "record", "--state", ",cmd", "--next", "x"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDaemonMissingAction(t *testing.T) {
	err := Run([]string{"daemon"})
	if err == nil {
		t.Fatal("expected error for missing daemon action")
	}
}

func TestClientPredictTemplateFlag(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "record", "--state", ",cmd", "--next", "git push origin main"})
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}

	err = Run([]string{"client", "predict", "--state", ",cmd", "--template", "--limit", "1"})
	if err != nil {
		t.Fatalf("predict with --template failed: %v", err)
	}
}

func TestClientPredictEmptyTemplate(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "predict", "--state", ",empty-state", "--template", "--limit", "1"})
	if err != nil {
		t.Fatalf("predict with --template on empty graph failed: %v", err)
	}
}

func TestClientPredictWithAt(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "record", "--state", ",x", "--next", "y"})
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}

	err = Run([]string{"client", "predict", "--state", ",x", "--at", "2025-01-01T00:00:00Z", "--limit", "1"})
	if err != nil {
		t.Fatalf("predict with --at failed: %v", err)
	}
}

func TestClientRecordMissingNext(t *testing.T) {
	err := Run([]string{"client", "record", "--state", "a,b", "--next", ""})
	if err == nil {
		t.Fatal("expected error for missing --next")
	}
	if !strings.Contains(err.Error(), "--next is required") {
		t.Errorf("error = %q, want '--next is required'", err)
	}
}

func TestClientRecordWithOptions(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{
		"client", "record",
		"--state", "prev1,prev2",
		"--next", "next-cmd",
		"--outcome", "success",
		"--cwd", "/home/user",
		"--at", "2025-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("record with options failed: %v", err)
	}
}

func TestDaemonStatusInvalidPid(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "hunch.sock")
	dbPath := filepath.Join(dir, "hunch.db")
	pidPath := filepath.Join(dir, "hunch.pid")
	socketDir := filepath.Dir(socket)
	if socketDir != "" && socketDir != "." {
		os.MkdirAll(socketDir, 0755)
	}

	// Create a socket so status finds it.
	f, err := os.Create(socket)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	t.Setenv("HUNCH_SOCKET", socket)
	t.Setenv("HUNCH_DB_PATH", dbPath)

	// Write an invalid PID.
	if err := os.WriteFile(pidPath, []byte("not-a-number"), 0644); err != nil {
		t.Fatal(err)
	}

	err = Run([]string{"daemon", "status"})
	if err != nil {
		t.Fatalf("status with invalid pid should not error: %v", err)
	}
}

func TestDaemonStatusNoPidFile(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "hunch.sock")
	dbPath := filepath.Join(dir, "hunch.db")

	f, err := os.Create(socket)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	t.Setenv("HUNCH_SOCKET", socket)
	t.Setenv("HUNCH_DB_PATH", dbPath)

	err = Run([]string{"daemon", "status"})
	if err != nil {
		t.Fatalf("status with no pid file should not error: %v", err)
	}
}

func TestClientPredictNoSuggestions(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "predict", "--state", ",never-seen", "--limit", "5"})
	if err != nil {
		t.Fatalf("predict on empty graph should print []: %v", err)
	}
}
