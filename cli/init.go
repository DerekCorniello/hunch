package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func cmdInit(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hunch init <shell>\n\nshells: zsh, bash, fish, pwsh")
	}

	shell := strings.ToLower(args[0])
	switch shell {
	case "zsh", "bash", "fish", "pwsh":
	default:
		return fmt.Errorf("unknown shell: %q\n\nsupported shells: zsh, bash, fish, pwsh", shell)
	}

	integrationPath, err := findIntegration(shell)
	if err != nil {
		return err
	}

	fmt.Printf("Add this line to your ~/.%s, then restart your shell or run source ~/.%s:\n\n", rcFile(shell), rcFile(shell))
	fmt.Printf("    source %s\n", integrationPath)
	return nil
}

func rcFile(shell string) string {
	switch shell {
	case "zsh":
		return "zshrc"
	case "bash":
		return "bashrc"
	case "fish":
		return "config/fish/config.fish"
	case "pwsh":
		return "Microsoft.PowerShell_profile.ps1"
	default:
		return "profile"
	}
}

func findIntegration(shell string) (string, error) {
	relative := filepath.Join("integrations", shell, fmt.Sprintf("hunch.%s", shellFileExt(shell)))

	// Try relative to the executable.
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		candidates := []string{
			filepath.Join(execDir, relative),
			filepath.Join(execDir, "..", relative),
			filepath.Join(execDir, "..", "share", "hunch", relative),
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

	// Fall back to the relative path from the working directory.
	if pwd, err := os.Getwd(); err == nil {
		p := filepath.Join(pwd, relative)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Last resort: show the expected location relative to the binary.
	if err == nil {
		execDir := filepath.Dir(execPath)
		return filepath.Join(execDir, relative), nil
	}
	return "", fmt.Errorf("cannot locate integration files; install hunch or run from source tree")
}

func shellFileExt(shell string) string {
	switch shell {
	case "pwsh":
		return "ps1"
	default:
		return shell
	}
}
