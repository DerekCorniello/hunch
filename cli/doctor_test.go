package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DerekCorniello/hunch/daemon"
)

func TestCheckDatabase(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "hunch.db")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		dbPath string
		want   checkStatus
	}{
		{name: "present", dbPath: existing, want: statusOK},
		// A missing database on a fresh install must not fail the exit code.
		{name: "absent is not a failure", dbPath: filepath.Join(t.TempDir(), "nope.db"), want: statusInfo},
		{name: "unconfigured is a failure", dbPath: "", want: statusProblem},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkDatabase(daemon.Options{DBPath: tt.dbPath}).status; got != tt.want {
				t.Errorf("status = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckDaemon(t *testing.T) {
	present := filepath.Join(t.TempDir(), "hunch.sock")
	if err := os.WriteFile(present, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		socket      string
		wantRunning bool
		wantStatus  checkStatus
	}{
		{name: "socket present", socket: present, wantRunning: true, wantStatus: statusOK},
		// A stopped daemon is normal: the shell hook starts one next prompt.
		{name: "socket absent", socket: filepath.Join(t.TempDir(), "gone.sock"), wantRunning: false, wantStatus: statusInfo},
		{name: "unconfigured", socket: "", wantRunning: false, wantStatus: statusInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, running := checkDaemon(daemon.Options{Socket: tt.socket})
			if running != tt.wantRunning {
				t.Errorf("running = %v, want %v", running, tt.wantRunning)
			}
			if got.status != tt.wantStatus {
				t.Errorf("status = %v, want %v", got.status, tt.wantStatus)
			}
		})
	}
}

func TestCheckOnPath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hunch")

	t.Run("on path", func(t *testing.T) {
		t.Setenv("PATH", dir+string(os.PathListSeparator)+"/nonexistent")
		if got := checkOnPath(exe); got.status != statusOK {
			t.Errorf("status = %v, want statusOK", got.status)
		}
	})

	t.Run("not on path", func(t *testing.T) {
		t.Setenv("PATH", "/nonexistent")
		got := checkOnPath(exe)
		if got.status != statusProblem {
			t.Errorf("status = %v, want statusProblem", got.status)
		}
		if !strings.Contains(got.detail, "PATH") {
			t.Errorf("detail should explain the problem, got %q", got.detail)
		}
	})
}

func TestCheckRcFile(t *testing.T) {
	t.Run("sources hunch", func(t *testing.T) {
		home := withTempHome(t)
		rc := filepath.Join(home, ".zshrc")
		if err := os.WriteFile(rc, []byte("source /opt/hunch/hunch.zsh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := checkRcFile("zsh"); got.status != statusOK {
			t.Errorf("status = %v, want statusOK", got.status)
		}
	})

	t.Run("exists but does not source", func(t *testing.T) {
		home := withTempHome(t)
		rc := filepath.Join(home, ".zshrc")
		if err := os.WriteFile(rc, []byte("export EDITOR=vim\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := checkRcFile("zsh"); got.status != statusProblem {
			t.Errorf("status = %v, want statusProblem", got.status)
		}
	})

	t.Run("missing rc file", func(t *testing.T) {
		withTempHome(t)
		got := checkRcFile("zsh")
		if got.status != statusProblem {
			t.Errorf("status = %v, want statusProblem", got.status)
		}
		if !got.indent {
			t.Error("rc file check should render as a detail line")
		}
	})
}

// The exit code is derived purely from statusProblem, so this mapping is the
// contract scripts depend on.
func TestCheckRenderAndStatusMapping(t *testing.T) {
	checks := []check{
		{label: "binary", status: statusOK, detail: "OK"},
		{label: "shell integration", status: statusInfo, detail: "unknown"},
		{label: "rc file", status: statusProblem, detail: "WARNING", indent: true},
	}

	width := 0
	for _, c := range checks {
		if len(c.label) > width {
			width = len(c.label)
		}
	}

	// Details must start at the same column whether or not the line is indented.
	var detailCols []int
	for _, c := range checks {
		detailCols = append(detailCols, strings.Index(c.render(width), c.detail))
	}
	for i, col := range detailCols {
		if col != detailCols[0] {
			t.Errorf("check %d detail starts at column %d, want %d", i, col, detailCols[0])
		}
	}
}

func TestRunDiagnosticsCoversEveryArea(t *testing.T) {
	withTempHome(t)
	opts := daemon.Options{
		Socket: filepath.Join(t.TempDir(), "hunch.sock"),
		DBPath: filepath.Join(t.TempDir(), "hunch.db"),
	}

	labels := make(map[string]bool)
	for _, c := range runDiagnostics(opts) {
		labels[c.label] = true
	}

	for _, want := range []string{"binary", "socket", "db", "daemon", "database"} {
		if !labels[want] {
			t.Errorf("diagnostics did not report %q", want)
		}
	}
}
