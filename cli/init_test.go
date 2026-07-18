package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// appendToRc writes to $HOME, so every test here redirects HOME at a temp dir
// first. Without that these would edit the developer's real shell rc file.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	return home
}

func TestAppendToRcCreatesFileWithMarkers(t *testing.T) {
	home := withTempHome(t)

	if err := appendToRc("zsh", "/opt/hunch/hunch.zsh"); err != nil {
		t.Fatalf("appendToRc: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatalf("read .zshrc: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		"# BEGIN hunch config",
		"# END hunch config",
		"source /opt/hunch/hunch.zsh",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rc file missing %q, got:\n%s", want, got)
		}
	}
}

func TestAppendToRcIsIdempotent(t *testing.T) {
	home := withTempHome(t)
	const line = "/opt/hunch/hunch.zsh"

	for i := 0; i < 3; i++ {
		if err := appendToRc("zsh", line); err != nil {
			t.Fatalf("appendToRc call %d: %v", i, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "BEGIN hunch config"); n != 1 {
		t.Errorf("wrote the block %d times, want 1:\n%s", n, data)
	}
}

func TestAppendToRcPreservesExistingContent(t *testing.T) {
	home := withTempHome(t)
	rc := filepath.Join(home, ".bashrc")
	const existing = "export EDITOR=vim\nalias ll='ls -la'\n"
	if err := os.WriteFile(rc, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := appendToRc("bash", "/opt/hunch/hunch.bash"); err != nil {
		t.Fatalf("appendToRc: %v", err)
	}

	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), existing) {
		t.Errorf("existing content was not preserved, got:\n%s", data)
	}
}

// A missing trailing newline must not glue the marker onto the user's last line.
func TestAppendToRcSeparatesUnterminatedLastLine(t *testing.T) {
	home := withTempHome(t)
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte("export EDITOR=vim"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := appendToRc("bash", "/opt/hunch/hunch.bash"); err != nil {
		t.Fatalf("appendToRc: %v", err)
	}

	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "vim# BEGIN") {
		t.Errorf("marker was appended to the last line without a newline:\n%s", data)
	}
}

func TestAppendToRcCreatesParentDirectory(t *testing.T) {
	home := withTempHome(t)

	// fish's rc lives at ~/.config/fish/config.fish, which will not exist.
	if err := appendToRc("fish", "/opt/hunch/hunch.fish"); err != nil {
		t.Fatalf("appendToRc: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".config", "fish", "config.fish")); err != nil {
		t.Errorf("fish config was not created: %v", err)
	}
}

func TestAppendToRcPreservesFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	home := withTempHome(t)
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("export EDITOR=vim\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := appendToRc("zsh", "/opt/hunch/hunch.zsh"); err != nil {
		t.Fatalf("appendToRc: %v", err)
	}

	info, err := os.Stat(rc)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("permissions changed to %v, want 0600", got)
	}
}

func TestRcFilePathShellPerShell(t *testing.T) {
	home := withTempHome(t)

	tests := []struct {
		shell string
		want  string
	}{
		{"zsh", filepath.Join(home, ".zshrc")},
		{"bash", filepath.Join(home, ".bashrc")},
		{"fish", filepath.Join(home, ".config", "fish", "config.fish")},
		{"unrecognized", filepath.Join(home, ".profile")},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			if got := rcFilePathShell(tt.shell); got != tt.want {
				t.Errorf("rcFilePathShell(%q) = %q, want %q", tt.shell, got, tt.want)
			}
		})
	}
}
