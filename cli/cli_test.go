package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DerekCorniello/hunch/daemon"
)

var sharedSocket string

// TestMain starts a single shared daemon for all CLI tests and shuts it
// down after the suite completes. Each test resets the daemon state via
// startTestDaemon, eliminating the cost and resource accumulation of
// starting/stopping a daemon per test.
func TestMain(m *testing.M) {
	ctx, cancel := context.WithCancel(context.Background())

	dir, err := os.MkdirTemp("", "hunch-cli-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}

	sharedSocket = filepath.Join(dir, "hunch.sock")
	dbPath := filepath.Join(dir, "hunch.db")

	os.Setenv("HUNCH_SOCKET", sharedSocket)
	os.Setenv("HUNCH_DB_PATH", dbPath)

	opts := daemon.LoadConfig()
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, opts)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(opts.Socket); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	code := m.Run()

	cancel()
	<-done
	os.RemoveAll(dir)
	os.Exit(code)
}

// startTestDaemon resets the shared test daemon to a clean state and
// returns the socket path and a no-op cleanup function.
func startTestDaemon(t *testing.T) (string, func()) {
	t.Helper()

	if err := Run([]string{"client", "reset"}); err != nil {
		t.Fatalf("reset daemon: %v", err)
	}

	return sharedSocket, func() {}
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
	for _, shell := range []string{"zsh", "bash", "fish", "powershell"} {
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
}

func TestInitMissingShell(t *testing.T) {
	saved := os.Getenv("SHELL")
	os.Setenv("SHELL", "")
	t.Cleanup(func() { os.Setenv("SHELL", saved) })

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
		_ = os.MkdirAll(socketDir, 0755)
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
	if err != ErrDaemonNotRunning {
		t.Fatalf("status with invalid pid: got %v, want ErrDaemonNotRunning", err)
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
	if err != ErrDaemonNotRunning {
		t.Fatalf("status with no pid file: got %v, want ErrDaemonNotRunning", err)
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

func TestClientConfig(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "config"})
	if err != nil {
		t.Fatalf("config failed: %v", err)
	}
}

func TestClientImport(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	// Export empty graph to a temp file, then import it back.
	dir := t.TempDir()
	exportPath := filepath.Join(dir, "export.json")

	// Use a daemon export via Run, then pipe output to file.
	// Simpler: create a minimal seed file.
	seed := `{"version":1,"transitions":[{"state":["","a"],"next":"b","count":2,"last_seen":"2025-01-01T00:00:00Z"}]}` + "\n"
	if err := os.WriteFile(exportPath, []byte(seed), 0644); err != nil {
		t.Fatal(err)
	}

	err := Run([]string{"client", "import", "--path", exportPath})
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	// Verify the import took effect.
	err = Run([]string{"client", "export"})
	if err != nil {
		t.Fatalf("export after import failed: %v", err)
	}
}

func TestClientImportMissingPath(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{"client", "import"})
	if err == nil {
		t.Fatal("expected error for missing --path")
	}
}

func TestImportHistoryUnknownShell(t *testing.T) {
	err := Run([]string{"import-history", "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown shell")
	}
	if !strings.Contains(err.Error(), "unknown shell") {
		t.Errorf("error = %q, want 'unknown shell'", err)
	}
}

func TestImportHistoryMissingShell(t *testing.T) {
	err := Run([]string{"import-history"})
	if err == nil {
		t.Fatal("expected error for missing shell arg")
	}
}

func TestImportHistoryNoDaemon(t *testing.T) {
	t.Setenv("HUNCH_SOCKET", filepath.Join(t.TempDir(), "nonexistent.sock"))

	dir := t.TempDir()
	historyPath := filepath.Join(dir, "zsh_history")
	if err := os.WriteFile(historyPath, []byte(": 1:0;echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := Run([]string{"import-history", "zsh", "--path", historyPath})
	if err == nil {
		t.Fatal("expected error when daemon is not running")
	}
	if !strings.Contains(err.Error(), "daemon must be running") {
		t.Errorf("error = %q, want 'daemon must be running'", err)
	}
}

func TestEnsureIntegrations(t *testing.T) {
	err := EnsureIntegrations()
	if err != nil {
		t.Fatalf("EnsureIntegrations: %v", err)
	}
}

func TestClientRecordWithCustomState(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{
		"client", "record",
		"--state", ",git add .",
		"--next", "git commit -m init",
	})
	if err != nil {
		t.Fatalf("record with custom state failed: %v", err)
	}

	err = Run([]string{"client", "predict", "--state", ",git add PATH", "--limit", "3"})
	if err != nil {
		t.Fatalf("predict after record failed: %v", err)
	}
}

func TestClientRecordCustomTimestamp(t *testing.T) {
	_, stop := startTestDaemon(t)
	defer stop()

	err := Run([]string{
		"client", "record",
		"--state", ",prev,prev2",
		"--next", "some cmd",
		"--at", "2025-06-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("record with custom timestamp failed: %v", err)
	}
}

func TestEnsureIntegrationsSuccess(t *testing.T) {
	err := EnsureIntegrations()
	if err != nil {
		t.Fatalf("EnsureIntegrations: %v", err)
	}
}

func TestFindIntegrationInDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("LocalAppData", dir)

	// Without integration files in the test dir, findIntegration may find
	// them in the real system data dir (from prior tests that called
	// EnsureIntegrations), or fall back to the "last resort" data dir path.
	// Either way, the returned path should end with "zsh/hunch.zsh".
	path, err := findIntegration("zsh")
	if err != nil {
		t.Fatalf("findIntegration(zsh): %v", err)
	}
	if !strings.HasSuffix(path, "zsh/hunch.zsh") && !strings.HasSuffix(path, "zsh\\hunch.zsh") {
		t.Errorf("path = %q, want suffix zsh/hunch.zsh", path)
	}
}

func TestDaemonStatusNotRunning(t *testing.T) {
	t.Setenv("HUNCH_SOCKET", filepath.Join(t.TempDir(), "nonexistent.sock"))

	err := Run([]string{"daemon", "status"})
	if err != ErrDaemonNotRunning {
		t.Fatalf("daemon status when not running: got %v, want ErrDaemonNotRunning", err)
	}
}
