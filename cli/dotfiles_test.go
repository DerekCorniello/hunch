package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectShell(t *testing.T) {
	cases := map[string]string{
		"/bin/zsh":                  "zsh",
		"/usr/bin/bash":             "bash",
		"/usr/local/bin/fish":       "fish",
		"/usr/bin/pwsh":             "powershell",
		"/opt/microsoft/powershell": "powershell",
		"/bin/tcsh":                 "", // unsupported shell
	}
	for shellPath, want := range cases {
		t.Setenv("SHELL", shellPath)
		if got := detectShell(); got != want {
			t.Errorf("detectShell() with SHELL=%q = %q, want %q", shellPath, got, want)
		}
	}
}

func TestHasHunchSourceLine(t *testing.T) {
	yes := []string{
		"source /home/u/hunch/integrations/zsh/hunch.zsh",
		"  . /home/u/hunch/integrations/bash/hunch.bash",
		"Import-Module /opt/hunch/integrations/powershell/hunch.ps1",
		"alias ls='ls --color'\nsource /x/hunch.zsh\n",
	}
	for _, c := range yes {
		if !hasHunchSourceLine(c) {
			t.Errorf("hasHunchSourceLine(%q) = false, want true", c)
		}
	}

	no := []string{
		"",
		"alias ls='ls --color'",
		"# source /x/hunch.zsh", // commented out
		"echo hunch",            // mentions hunch but not a source line
		"source /x/other.zsh",
	}
	for _, c := range no {
		if hasHunchSourceLine(c) {
			t.Errorf("hasHunchSourceLine(%q) = true, want false", c)
		}
	}
}

func TestRemoveRcLineBlock(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".zshrc")
	content := "alias x=1\n" +
		"# BEGIN hunch config\n" +
		"source /h/hunch.zsh\n" +
		"export HUNCH_BIN=hunch\n" +
		"# END hunch config\n" +
		"alias y=2\n"
	if err := os.WriteFile(rc, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := removeRcLine(rc)
	if err != nil {
		t.Fatalf("removeRcLine: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}

	got, _ := os.ReadFile(rc)
	out := string(got)
	if strings.Contains(out, "hunch") {
		t.Errorf("hunch block not fully removed: %q", out)
	}
	for _, keep := range []string{"alias x=1", "alias y=2"} {
		if !strings.Contains(out, keep) {
			t.Errorf("removed unrelated line %q; result: %q", keep, out)
		}
	}
}

func TestRemoveRcLineStandalone(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	content := "export A=1\n. /home/u/hunch/integrations/bash/hunch.bash\nexport B=2\n"
	if err := os.WriteFile(rc, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := removeRcLine(rc)
	if err != nil {
		t.Fatalf("removeRcLine: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	got, _ := os.ReadFile(rc)
	out := string(got)
	if strings.Contains(out, "hunch") {
		t.Errorf("standalone source line not removed: %q", out)
	}
	if !strings.Contains(out, "export A=1") || !strings.Contains(out, "export B=2") {
		t.Errorf("removed unrelated lines; result: %q", out)
	}
}

func TestRemoveRcLineNoHunch(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".zshrc")
	content := "alias x=1\nexport B=2\n"
	if err := os.WriteFile(rc, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := removeRcLine(rc)
	if err != nil {
		t.Fatalf("removeRcLine: %v", err)
	}
	if changed {
		t.Error("changed = true on a file with no hunch lines, want false")
	}
	got, _ := os.ReadFile(rc)
	if string(got) != content {
		t.Errorf("file modified unexpectedly: %q", string(got))
	}
}

func TestRemoveRcLineMissingFile(t *testing.T) {
	changed, err := removeRcLine(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Errorf("err = %v, want nil for missing file", err)
	}
	if changed {
		t.Error("changed = true for missing file, want false")
	}
}
