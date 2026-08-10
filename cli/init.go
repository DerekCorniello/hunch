package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DerekCorniello/hunch/core/eval"
	"github.com/DerekCorniello/hunch/daemon"
)

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("hunch init", flag.ContinueOnError)
	autoAppend := fs.Bool("auto", false, "automatically append source line to .zshrc")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := EnsureIntegrations(); err != nil {
		return err
	}
	if err := ensureConfig(); err != nil {
		return err
	}

	integrationPath, err := findIntegration()
	if err != nil {
		return err
	}

	if err := ensureDaemonRunning(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not start daemon: %v\n", err)
	}

	offerHistoryImport()

	rcFile := zshrcPath()
	if *autoAppend {
		if err := appendToRc(integrationPath); err != nil {
			return fmt.Errorf("auto-append: %w", err)
		}
		fmt.Printf("%s: %s\n", green("Added source line"), bold(rcFile))
		fmt.Printf("%s\n", bold("Restart your shell or run: source "+rcFile))
	} else {
		fmt.Printf("Add this line to your %s, then restart your shell or run source %s:\n\n", rcFile, rcFile)
		fmt.Printf("    %s\n", teal("source "+integrationPath))
	}

	warnPath()
	return nil
}

func appendToRc(sourceLine string) error {
	rcPath := zshrcPath()

	// Get the directory containing the hunch binary
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	binDir := filepath.Dir(execPath)

	// Wrap in BEGIN/END markers for clean uninstall
	block := fmt.Sprintf("# BEGIN hunch config\nexport PATH=\"$PATH:%s\"\nsource %s\n# END hunch config", binDir, sourceLine)

	if err := os.MkdirAll(filepath.Dir(rcPath), 0755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}

	var perm os.FileMode = 0644
	data, err := os.ReadFile(rcPath)
	if err == nil {
		if info, err := os.Stat(rcPath); err == nil {
			perm = info.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	content := string(data)

	// Check if already present (with or without markers)
	if strings.Contains(content, sourceLine) {
		return nil
	}

	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n" + block + "\n"

	tmpPath := rcPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, rcPath)
}

// zshrcPath returns the path to the user's .zshrc.
func zshrcPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".zshrc"
	}
	return filepath.Join(home, ".zshrc")
}

func warnPath() {
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	execDir := filepath.Dir(execPath)
	absDir, err := filepath.Abs(execDir)
	if err != nil {
		return
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		absPathDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if absDir == absPathDir {
			return
		}
	}

	fmt.Fprintf(os.Stderr, "\nWarning: %s is not in your $PATH.\n", execPath)
	fmt.Fprintf(os.Stderr, "You won't be able to run hunch directly in a new shell.\n")
	fmt.Fprintf(os.Stderr, "\nTo fix, install it globally with:\n\n")
	fmt.Fprintf(os.Stderr, "    go install github.com/DerekCorniello/hunch@latest\n")
	fmt.Fprintf(os.Stderr, "\nOr add this directory to your $PATH:\n\n")
	fmt.Fprintf(os.Stderr, "    export PATH=\"$PATH:%s\"\n", absDir)
	fmt.Fprintln(os.Stderr)
}

// offerHistoryImport prompts to import ~/.zsh_history and, on completion,
// prints a self-test summary computed from that same history so the user
// sees real accuracy numbers before they've decided whether to trust hunch.
func offerHistoryImport() {
	if !isTerminal() {
		return
	}

	historyPath, cmdCount, err := resolveHistoryPath("")
	if err != nil || cmdCount <= 0 || historyPath == "" {
		return
	}

	fmt.Fprintf(os.Stderr, "\nFound %s (%d commands).\n", historyPath, cmdCount)
	if cmdCount > 50000 {
		fmt.Fprintf(os.Stderr, "Large history detected - importing may take a moment.\n")
	}

	fmt.Fprintf(os.Stderr, "\nImport your command history to jump-start predictions? [Y/n]: ")

	var answer string
	_, _ = fmt.Scanln(&answer)
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer != "" && answer != "y" && answer != "yes" {
		fmt.Fprintln(os.Stderr)
		return
	}

	fmt.Fprintln(os.Stderr)
	threads := runtime.NumCPU()
	if err := runImport(historyPath, threads, func(msg string) {
		fmt.Fprint(os.Stderr, msg)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: import failed: %v\n", err)
		return
	}

	printImportEvalSummary(historyPath)
}

// printImportEvalSummary replays the just-imported history through the same
// scoring the daemon uses and prints the resulting accuracy, so "it learns
// your workflows" is a number the user watched get computed from their own
// data rather than a claim they have to take on faith.
func printImportEvalSummary(historyPath string) {
	rawCmds, err := parseZshHistory(historyPath)
	if err != nil || len(rawCmds) == 0 {
		return
	}

	opts := eval.DefaultOptions()
	if len(rawCmds) <= opts.Warmup {
		// Too little history for a meaningful self-test; the import itself
		// already succeeded, so say nothing rather than print a noisy 0%.
		return
	}

	templates, err := normalizeConcurrent(rawCmds, runtime.NumCPU())
	if err != nil {
		return
	}
	commands := make([]eval.Command, len(templates))
	for i, tmpl := range templates {
		commands[i] = eval.Command{Template: tmpl}
	}

	r := eval.Run(commands, opts)
	if r.Scored == 0 {
		return
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Self-test on your history: top-1 %.0f%%, top-3 %.0f%% (vs. %.0f%% always guessing your most frequent command).\n",
		100*r.Rate(r.Top1), 100*r.Rate(r.Top3), 100*r.Rate(r.BaselineTop1))
	fmt.Fprintln(os.Stderr, "Run 'hunch eval' any time to recheck this against your live history.")
}

func EnsureIntegrations() error {
	if IntegrationFS == nil {
		return nil
	}

	dataDir, err := daemon.DataDir()
	if err != nil {
		return fmt.Errorf("locate data dir: %w", err)
	}
	destDir := filepath.Join(dataDir, "hunch", "integrations")

	srcPath := filepath.Join("zsh", "hunch.zsh")
	destPath := filepath.Join(destDir, srcPath)

	src, err := IntegrationFS.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open embedded %s: %w", srcPath, err)
	}

	embedded, err := io.ReadAll(src)
	src.Close()
	if err != nil {
		return fmt.Errorf("read embedded %s: %w", srcPath, err)
	}

	existing, err := os.ReadFile(destPath)
	if err == nil && string(existing) == string(embedded) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("create dir for %s: %w", destPath, err)
	}
	return os.WriteFile(destPath, embedded, 0644)
}

func ensureConfig() error {
	cfgDir, err := daemon.ConfigDir()
	if err != nil {
		return fmt.Errorf("locate config dir: %w", err)
	}
	cfgPath := filepath.Join(cfgDir, "hunch", "config.toml")

	if _, err := os.Stat(cfgPath); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	defaults := []byte(`# Hunch configuration (TOML)
# See https://github.com/DerekCorniello/hunch for docs.

# Override IPC socket path (default: <CacheDir>/hunch.sock)
# socket = "/run/user/1000/hunch.sock"

# Override SQLite database path (default: <DataDir>/hunch.db)
# db_path = "/var/lib/hunch/hunch.db"

# Keys that accept the current ghost-text suggestion
# accept_keys = ["right", "end"]
# Alt-n / Alt-p cycle through ranked suggestions (not configurable here; see
# the zsh integration for keybinding overrides).

# Path to the daemon binary
# daemon_bin = "/usr/local/bin/hunch"

# Decay half-life in hours (default 720 = 30 days)
# half_life_hours = 720

# Additive smoothing constant (default 0.5)
# alpha = 0.5

# Extra parent commands whose subcommand is preserved during normalization
# extra_parents = ["mycli", "teamtool"]

# Log level: debug, info, warn, error (default info)
# log_level = "info"
`)
	if err := os.WriteFile(cfgPath, defaults, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// findIntegration locates the zsh integration script, preferring the copy
// EnsureIntegrations wrote to the data directory, then falling back to a
// build-relative path (for a `go run`/local-clone workflow).
func findIntegration() (string, error) {
	dataDir, dataDirErr := daemon.DataDir()
	if dataDirErr == nil {
		p := filepath.Join(dataDir, "hunch", "integrations", "zsh", "hunch.zsh")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	relative := filepath.Join("integrations", "zsh", "hunch.zsh")
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		candidates := []string{
			filepath.Join(execDir, relative),
			filepath.Join(execDir, "..", relative),
		}
		for _, p := range candidates {
			abs, err := filepath.Abs(p)
			if err == nil {
				if _, err := os.Stat(abs); err == nil {
					return abs, nil
				}
			}
		}
	}

	if pwd, err := os.Getwd(); err == nil {
		p := filepath.Join(pwd, relative)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	if dataDirErr == nil {
		return filepath.Join(dataDir, "hunch", "integrations", "zsh", "hunch.zsh"), nil
	}
	return "", fmt.Errorf("cannot locate integration files; run hunch init first, then retry")
}
