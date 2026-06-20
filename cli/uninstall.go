package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DerekCorniello/hunch/daemon"
)

func cmdUninstall(skipConfirm bool) error {
	if !skipConfirm {
		fmt.Print("This will remove all hunch data and configuration. Continue? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Uninstall cancelled.")
			return nil
		}
	}

	opts := daemon.LoadConfig()

	if err := cmdDaemonStop(); err != nil {
		if !strings.Contains(err.Error(), "not running") {
			fmt.Fprintf(os.Stderr, "warning: stop daemon: %v\n", err)
		}
	}

	fmt.Println("Removing hunch files...")

	removed := 0

	if opts.Socket != "" {
		if err := os.Remove(opts.Socket); err == nil {
			removed++
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "  warning: remove socket: %v\n", err)
		}
	}

	if opts.DBPath != "" {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(opts.DBPath + suffix); err == nil {
				removed++
			}
		}
	}

	dataDir, _ := daemon.DataDir()
	if dataDir != "" {
		hunchData := filepath.Join(dataDir, "hunch")
		for _, name := range []string{"hunch.pid", "hunch.lock", "hunch.log"} {
			if err := os.Remove(filepath.Join(hunchData, name)); err == nil {
				removed++
			}
		}
		for _, shell := range shells {
			p := filepath.Join(hunchData, "integrations", shell, "hunch."+shellFileExt(shell))
			if err := os.Remove(p); err == nil {
				removed++
			}
		}
		os.Remove(filepath.Join(hunchData, "integrations"))
		os.Remove(hunchData)
	}

	cfgDir, _ := daemon.ConfigDir()
	if cfgDir != "" {
		cfgPath := filepath.Join(cfgDir, "hunch", "config.toml")
		if err := os.Remove(cfgPath); err == nil {
			removed++
		}
		os.Remove(filepath.Join(cfgDir, "hunch"))
	}

	for _, shell := range shells {
		rcPath := rcFilePathShell(shell)
		if changed, err := removeRcLine(rcPath); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: clean %s: %v\n", rcPath, err)
		} else if changed {
			removed++
		}
	}

	if removed > 0 {
		fmt.Println("  done.")
	} else {
		fmt.Println("  nothing to remove.")
	}

	fmt.Println()
	fmt.Println("Hunch has been uninstalled from your system.")
	if execPath, err := os.Executable(); err == nil {
		fmt.Printf("The binary at %s can be deleted manually.\n", execPath)
	}
	return nil
}

func removeRcLine(rcPath string) (bool, error) {
	data, err := os.ReadFile(rcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	var lines []string
	changed := false
	inHunchBlock := false

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)

		// Check for BEGIN marker
		if strings.Contains(trimmed, "# BEGIN hunch config") {
			inHunchBlock = true
			changed = true
			continue
		}

		// Check for END marker
		if strings.Contains(trimmed, "# END hunch config") {
			inHunchBlock = false
			continue
		}

		// Skip lines inside the hunch block
		if inHunchBlock {
			continue
		}

		// Also remove any standalone hunch source lines (for backwards compatibility)
		if strings.Contains(trimmed, "hunch") {
			if strings.HasPrefix(trimmed, "source ") ||
				strings.HasPrefix(trimmed, ". ") ||
				strings.HasPrefix(trimmed, "Import-Module ") {
				changed = true
				continue
			}
		}
		lines = append(lines, line)
	}

	if !changed {
		return false, nil
	}

	return true, os.WriteFile(rcPath, []byte(strings.Join(lines, "\n")), 0644)
}
