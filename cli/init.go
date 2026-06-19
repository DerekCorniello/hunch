package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DerekCorniello/hunch/daemon"
)

var shells = []string{"zsh", "bash", "fish", "powershell"}

func cmdInit(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hunch init <shell>\n\nshells: zsh, bash, fish, powershell")
	}

	shell := strings.ToLower(args[0])
	if !validShell(shell) {
		return fmt.Errorf("unknown shell: %q\n\nsupported shells: zsh, bash, fish, powershell", shell)
	}

	if err := EnsureIntegrations(); err != nil {
		return err
	}
	if err := ensureConfig(); err != nil {
		return err
	}

	offerHistoryImport(shell)

	integrationPath, err := findIntegration(shell)
	if err != nil {
		return err
	}

	fmt.Printf("Add this line to your ~/.%s, then restart your shell or run source ~/.%s:\n\n", rcFile(shell), rcFile(shell))
	fmt.Printf("    source %s\n", integrationPath)

	warnPath()
	return nil
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

func offerHistoryImport(shell string) {
	if !isTerminal() {
		return
	}

	historyPath, cmdCount, err := resolveHistoryPath(shell, "")
	if err != nil || cmdCount <= 0 || historyPath == "" {
		return
	}

	fmt.Fprintf(os.Stderr, "\nFound %s (%d commands).\n", historyPath, cmdCount)
	if cmdCount > 50000 {
		fmt.Fprintf(os.Stderr, "Large history detected — importing may take a moment.\n")
	}

	fmt.Fprintf(os.Stderr, "\nImport your command history to jump-start predictions? [Y/n]: ")

	var answer string
	fmt.Scanln(&answer)
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer == "" || answer == "y" || answer == "yes" {
		fmt.Fprintln(os.Stderr)
		threads := runtime.NumCPU()
		if err := runImport(shell, historyPath, threads, func(msg string) {
			fmt.Fprint(os.Stderr, msg)
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: import failed: %v\n", err)
		}
	} else {
		fmt.Fprintln(os.Stderr)
	}
}

func validShell(shell string) bool {
	switch shell {
	case "zsh", "bash", "fish", "powershell":
		return true
	}
	return false
}

func rcFile(shell string) string {
	switch shell {
	case "zsh":
		return "zshrc"
	case "bash":
		return "bashrc"
	case "fish":
		return "config/fish/config.fish"
	case "powershell":
		return "Microsoft.PowerShell_profile.ps1"
	default:
		return "profile"
	}
}

// EnsureIntegrations copies embedded integration scripts to the data
// directory. Existing files are overwritten when the embedded version
// differs, so go install @latest always delivers fresh integrations.
func EnsureIntegrations() error {
	if IntegrationFS == nil {
		return nil
	}

	dataDir, err := daemon.DataDir()
	if err != nil {
		return fmt.Errorf("locate data dir: %w", err)
	}
	destDir := filepath.Join(dataDir, "hunch", "integrations")

	for _, shell := range shells {
		srcPath := filepath.Join(shell, "hunch."+shellFileExt(shell))
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
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("create dir for %s: %w", destPath, err)
		}
		if err := os.WriteFile(destPath, embedded, 0644); err != nil {
			return fmt.Errorf("write %s: %w", destPath, err)
		}
	}
	return nil
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
# zsh/fish/pwsh: right, end. bash: tab.
# accept_keys = ["right", "end"]

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

func findIntegration(shell string) (string, error) {
	dataDir, dataDirErr := daemon.DataDir()
	if dataDirErr == nil {
		p := filepath.Join(dataDir, "hunch", "integrations", shell, "hunch."+shellFileExt(shell))
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Fallback: search relative to the executable.
	relative := filepath.Join("integrations", shell, fmt.Sprintf("hunch.%s", shellFileExt(shell)))
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

	// Fallback: relative to working directory.
	if pwd, err := os.Getwd(); err == nil {
		p := filepath.Join(pwd, relative)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Last resort: show expected data dir path.
	if dataDirErr == nil {
		return filepath.Join(dataDir, "hunch", "integrations", shell, "hunch."+shellFileExt(shell)), nil
	}
	return "", fmt.Errorf("cannot locate integration files; run hunch init first, then retry")
}

func shellFileExt(shell string) string {
	switch shell {
	case "powershell":
		return "ps1"
	default:
		return shell
	}
}
