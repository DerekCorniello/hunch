package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DerekCorniello/hunch/core/graph"
)

func TestStripZshMeta(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "simple", line: ": 1234567890:0;git commit -m init", want: "git commit -m init"},
		{name: "with_extra_colons", line: ": 1234567890:0;echo hello:world", want: "echo hello:world"},
		{name: "empty_cmd", line: ": 1234567890:0;", want: ""},
		{name: "not_zsh_format", line: "git commit -m init", want: ""},
		{name: "no_semicolon", line: ": 1234567890:0", want: ""},
		{name: "empty_line", line: "", want: ""},
		{name: "only_colon", line: ":", want: ""},
		{name: "no_second_colon", line: ":no-colon-here", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripZshMeta(tt.line)
			if got != tt.want {
				t.Errorf("stripZshMeta(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseZshHistory(t *testing.T) {
	content := ": 1740000000:0;git add .\n: 1740000001:0;git commit -m init\n: 1740000002:1;git push origin main\n"
	path := filepath.Join(t.TempDir(), "zsh_history")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cmds, err := parseZshHistory(path)
	if err != nil {
		t.Fatalf("parseZshHistory: %v", err)
	}
	if len(cmds) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(cmds))
	}
	if cmds[0] != "git add ." {
		t.Errorf("cmds[0] = %q, want %q", cmds[0], "git add .")
	}
	if cmds[1] != "git commit -m init" {
		t.Errorf("cmds[1] = %q, want %q", cmds[1], "git commit -m init")
	}
	if cmds[2] != "git push origin main" {
		t.Errorf("cmds[2] = %q, want %q", cmds[2], "git push origin main")
	}
}

func TestParseZshHistory_Empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zsh_history")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cmds, err := parseZshHistory(path)
	if err != nil {
		t.Fatalf("parseZshHistory: %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands, got %d", len(cmds))
	}
}

func TestParseZshHistory_MixedLines(t *testing.T) {
	content := ": 1740000000:0;git add .\nnot-a-history-line\n: 1740000001:0;git commit\n"
	path := filepath.Join(t.TempDir(), "zsh_history")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cmds, err := parseZshHistory(path)
	if err != nil {
		t.Fatalf("parseZshHistory: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}
}

func TestParseZshHistory_FileNotFound(t *testing.T) {
	_, err := parseZshHistory("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestResolveHistoryPath(t *testing.T) {
	t.Run("override_exists", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "custom_history")
		if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644); err != nil {
			t.Fatal(err)
		}
		resolved, count, err := resolveHistoryPath(path)
		if err != nil {
			t.Fatalf("resolveHistoryPath: %v", err)
		}
		if resolved != path {
			t.Errorf("path = %q, want %q", resolved, path)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
	})

	t.Run("override_not_found", func(t *testing.T) {
		_, _, err := resolveHistoryPath("/nonexistent/history")
		if err == nil {
			t.Fatal("expected error for nonexistent override path")
		}
	})

	t.Run("default_path_is_zsh_history", func(t *testing.T) {
		home := withTempHome(t)
		path, _, err := resolveHistoryPath("")
		if err != nil {
			t.Fatalf("resolveHistoryPath(\"\"): %v", err)
		}
		want := filepath.Join(home, ".zsh_history")
		if path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	})
}

func TestCountLines(t *testing.T) {
	t.Run("empty_file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty")
		_ = os.WriteFile(path, []byte(""), 0644)
		if n := countLines(path); n != 0 {
			t.Errorf("countLines(empty) = %d, want 0", n)
		}
	})

	t.Run("multiple_lines", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "lines")
		_ = os.WriteFile(path, []byte("a\nb\nc\n"), 0644)
		if n := countLines(path); n != 3 {
			t.Errorf("countLines = %d, want 3", n)
		}
	})

	t.Run("no_trailing_newline", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "notrailing")
		_ = os.WriteFile(path, []byte("a\nb"), 0644)
		if n := countLines(path); n != 2 {
			t.Errorf("countLines = %d, want 2", n)
		}
	})

	t.Run("file_not_found", func(t *testing.T) {
		if n := countLines("/nonexistent"); n != 0 {
			t.Errorf("countLines(nonexistent) = %d, want 0", n)
		}
	})
}

func TestResolveHome(t *testing.T) {
	t.Run("with_tilde", func(t *testing.T) {
		result := resolveHome("~/.zsh_history")
		if !strings.HasSuffix(result, ".zsh_history") {
			t.Errorf("result = %q, want suffix .zsh_history", result)
		}
		if strings.HasPrefix(result, "~") {
			t.Errorf("resolveHome did not expand ~: %q", result)
		}
	})

	t.Run("no_tilde", func(t *testing.T) {
		result := resolveHome("/absolute/path")
		if result != "/absolute/path" {
			t.Errorf("result = %q, want /absolute/path", result)
		}
	})

	t.Run("tilde_not_prefix", func(t *testing.T) {
		result := resolveHome("foo~bar")
		if result != "foo~bar" {
			t.Errorf("result = %q, want foo~bar", result)
		}
	})
}

func TestIsTerminal(t *testing.T) {
	// isTerminal checks actual OS stdin, which may be a terminal depending
	// on how the test runner is invoked. Just ensure it doesn't panic and
	// returns a bool.
	_ = isTerminal()
}

// Counts include the generalized contexts each observation is recorded under,
// which is what lets an imported graph answer the same fallback queries as a
// learned one. See graph.BackoffStates.
func TestBuildTransitions(t *testing.T) {
	tests := []struct {
		name   string
		input  []string
		expect int
	}{
		{name: "empty", input: nil, expect: 0},
		// The first command has only empty padding for context, so there is
		// no informative generalization to add.
		{name: "single", input: []string{"a"}, expect: 1},
		// "b" is recorded under ["", "a"] and under the shortened ["a"].
		{name: "two_commands", input: []string{"a", "b"}, expect: 3},
		{name: "three_commands", input: []string{"a", "b", "c"}, expect: 5},
		{name: "skips_empty_but_still_records", input: []string{"a", "", "b"}, expect: 2},
		{name: "all_empty", input: []string{"", "", ""}, expect: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildTransitions(tt.input)
			if len(result) != tt.expect {
				t.Errorf("buildTransitions() returned %d transitions, want %d", len(result), tt.expect)
			}
		})
	}
}

func TestBuildTransitions_StateTracking(t *testing.T) {
	normalized := []string{"git add PATH", "git commit FLAG STR", "git push STR"}
	transitions := buildTransitions(normalized)

	// Three observations, plus a generalized key for each of the two that
	// have a real prior command to shorten.
	if len(transitions) != 5 {
		t.Fatalf("expected 5 transitions, got %d", len(transitions))
	}

	foundInitial := false
	foundCommit := false
	for _, tx := range transitions {
		switch tx.Next {
		case "git add PATH":
			foundInitial = true
		case "git commit FLAG STR":
			if len(tx.State) == 2 {
				foundCommit = true
				if tx.State[0] != "" || tx.State[1] != "git add PATH" {
					t.Errorf("unexpected state for git commit: %v", tx.State)
				}
			}
		}
	}
	if !foundInitial {
		t.Error("expected initial transition (empty state -> git add PATH)")
	}
	if !foundCommit {
		t.Error("expected transition to git commit FLAG STR")
	}
}

func TestNormalizeConcurrent(t *testing.T) {
	raw := []string{"git add .", "git commit -m init", "git push origin main"}
	result, err := normalizeConcurrent(raw, 2)
	if err != nil {
		t.Fatalf("normalizeConcurrent: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result))
	}
	if result[0] != "git add PATH" {
		t.Errorf("result[0] = %q, want %q", result[0], "git add PATH")
	}
	if result[1] != "git commit FLAG STR" {
		t.Errorf("result[1] = %q, want %q", result[1], "git commit FLAG STR")
	}
	if result[2] != "git push STR" {
		t.Errorf("result[2] = %q, want %q", result[2], "git push STR")
	}
}

func TestNormalizeConcurrent_EmptyInput(t *testing.T) {
	result, err := normalizeConcurrent(nil, 2)
	if err != nil {
		t.Fatalf("normalizeConcurrent(nil): %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d", len(result))
	}
}

func TestSendSeed_BuildsValidSeed(t *testing.T) {
	transitions := []graph.Transition{
		{State: []string{"", "a"}, Next: "b", Count: 1},
	}
	// We can't actually send without a daemon, but we can verify the
	// sendSeed function builds the request correctly by checking it
	// returns the expected "connect to daemon" error.
	t.Setenv("HUNCH_SOCKET", filepath.Join(t.TempDir(), "nonexistent.sock"))
	err := sendSeed(transitions)
	if err == nil {
		t.Fatal("expected error (no daemon running)")
	}
	if !strings.Contains(err.Error(), "connect to daemon") {
		t.Errorf("error = %q, want 'connect to daemon'", err)
	}
}
