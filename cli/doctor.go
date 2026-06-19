package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DerekCorniello/hunch/daemon"
	"github.com/DerekCorniello/hunch/ipc"
)

func cmdDoctor() error {
	ok := true

	fmt.Println("hunch doctor")
	fmt.Println()

	execPath, err := os.Executable()
	fmt.Print("binary: ")
	if err != nil {
		fmt.Printf("ERROR (%v)\n", err)
		ok = false
	} else {
		fmt.Printf("OK (%s)\n", execPath)
	}

	fmt.Print("PATH: ")
	if execPath != "" {
		execDir := filepath.Dir(execPath)
		absExec, err := filepath.EvalSymlinks(execDir)
		if err != nil {
			absExec = execDir
		}
		onPath := false
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			absDir, err := filepath.EvalSymlinks(dir)
			if err != nil {
				absDir = dir
			}
			if absDir == absExec {
				onPath = true
				break
			}
		}
		if onPath {
			fmt.Println("OK")
		} else {
			fmt.Println("WARNING: binary directory not in PATH")
			ok = false
		}
	}

	opts := daemon.LoadConfig()
	fmt.Printf("socket: %s\n", opts.Socket)
	fmt.Printf("db:    %s\n", opts.DBPath)

	fmt.Print("daemon: ")
	daemonRunning := false
	if opts.Socket != "" {
		if _, err := os.Stat(opts.Socket); err == nil {
			daemonRunning = true
			fmt.Println("RUNNING")
		} else {
			fmt.Println("STOPPED")
		}
	} else {
		fmt.Println("UNKNOWN (no socket path)")
	}

	fmt.Print("database: ")
	if opts.DBPath == "" {
		fmt.Println("ERROR: no db path configured")
		ok = false
	} else if _, err := os.Stat(opts.DBPath); os.IsNotExist(err) {
		fmt.Println("not found (first run? run some commands to create it)")
	} else if err != nil {
		fmt.Printf("ERROR (%v)\n", err)
		ok = false
	} else {
		fmt.Println("OK")
	}

	if daemonRunning {
		var stats ipc.StatsResponse
		if err := unmarshalResponse(ipc.Request{Op: "stats"}, &stats); err == nil {
			fmt.Printf("graph size: %d transitions\n", stats.Size)
			fmt.Printf("half-life:  %s\n", stats.HalfLife)
			fmt.Printf("alpha:      %.2f\n", stats.Alpha)
		}
	}

	fmt.Print("shell integration: ")
	shell := detectShell()
	if shell == "" {
		fmt.Println("unknown (SHELL not set)")
	} else {
		integrationPath, err := findIntegration(shell)
		if err != nil {
			fmt.Printf("not found (run: hunch init %s)\n", shell)
			ok = false
		} else if _, err := os.Stat(integrationPath); os.IsNotExist(err) {
			fmt.Printf("file missing (run: hunch init %s)\n", shell)
			ok = false
		} else {
			fmt.Printf("OK (%s, %s)\n", shell, integrationPath)

			rcPath := rcFilePathShell(shell)
			if _, err := os.Stat(rcPath); err == nil {
				data, _ := os.ReadFile(rcPath)
				if hasHunchSourceLine(string(data)) {
					fmt.Println("  rc file: OK (source line found)")
				} else {
					fmt.Printf("  rc file: WARNING (%s does not source hunch)\n", rcPath)
					ok = false
				}
			} else {
				fmt.Printf("  rc file: WARNING (%s not found)\n", rcPath)
				ok = false
			}
		}
	}

	fmt.Println()
	if ok {
		fmt.Println("All checks passed.")
		return nil
	}
	fmt.Println("Some checks failed — see warnings above.")
	return fmt.Errorf("doctor: some checks failed")
}

func hasHunchSourceLine(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "hunch") {
			if strings.HasPrefix(trimmed, "source ") ||
				strings.HasPrefix(trimmed, ". ") ||
				strings.HasPrefix(trimmed, "Import-Module ") {
				return true
			}
		}
	}
	return false
}
