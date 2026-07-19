package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DerekCorniello/hunch/daemon"
	"github.com/DerekCorniello/hunch/ipc"
)

// checkStatus separates problems from observations. Several things doctor
// reports are not faults: a stopped daemon or an absent database on a fresh
// install are expected, so only statusProblem affects the exit code.
type checkStatus int

const (
	statusOK checkStatus = iota
	statusInfo
	statusProblem
)

// check is one line of doctor output. indent marks a detail line belonging to
// the check above it.
type check struct {
	label  string
	status checkStatus
	detail string
	indent bool
}

// render pads the label so details line up in a column. width is the longest
// label in the report.
func (c check) render(width int) string {
	prefix := ""
	if c.indent {
		// The indent consumes part of the column, so the detail still lands
		// in the same place as every other line.
		prefix = "  "
		width -= len(prefix)
	}
	return fmt.Sprintf("%s%-*s %s", prefix, width+1, c.label+":", c.detail)
}

func cmdDoctor() error {
	fmt.Println("hunch doctor")
	fmt.Println()

	checks := runDiagnostics(daemon.LoadConfig())

	width := 0
	for _, c := range checks {
		if len(c.label) > width {
			width = len(c.label)
		}
	}

	failed := false
	for _, c := range checks {
		fmt.Println(c.render(width))
		if c.status == statusProblem {
			failed = true
		}
	}

	fmt.Println()
	if !failed {
		fmt.Println("All checks passed.")
		return nil
	}
	fmt.Println("Some checks failed - see warnings above.")
	return fmt.Errorf("doctor: some checks failed")
}

// runDiagnostics gathers every check in display order. It performs no output,
// so the decision logic can be tested without capturing stdout.
func runDiagnostics(opts daemon.Options) []check {
	binary, execPath := checkBinary()
	checks := []check{binary}
	if execPath != "" {
		checks = append(checks, checkOnPath(execPath))
	}

	checks = append(checks,
		check{label: "socket", status: statusInfo, detail: opts.Socket},
		check{label: "db", status: statusInfo, detail: opts.DBPath},
	)

	daemonCheck, running := checkDaemon(opts)
	checks = append(checks, daemonCheck, checkDatabase(opts))
	if running {
		checks = append(checks, daemonStats()...)
	}
	return append(checks, checkShellIntegration()...)
}

func checkBinary() (check, string) {
	execPath, err := os.Executable()
	if err != nil {
		return check{label: "binary", status: statusProblem, detail: fmt.Sprintf("ERROR (%v)", err)}, ""
	}
	return check{label: "binary", status: statusOK, detail: fmt.Sprintf("OK (%s)", execPath)}, execPath
}

// checkOnPath reports whether the running binary's directory is on PATH.
// Both sides are resolved through symlinks so that a binary reached via, say,
// /usr/local/bin -> /opt/hunch/bin still counts as on PATH.
func checkOnPath(execPath string) check {
	execDir := resolveSymlinks(filepath.Dir(execPath))
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if resolveSymlinks(dir) == execDir {
			return check{label: "PATH", status: statusOK, detail: "OK"}
		}
	}
	return check{label: "PATH", status: statusProblem, detail: "WARNING: binary directory not in PATH"}
}

func resolveSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// checkDaemon infers liveness from the socket file. A stopped daemon is not a
// failure: shell integration starts one on the next prompt.
func checkDaemon(opts daemon.Options) (check, bool) {
	if opts.Socket == "" {
		return check{label: "daemon", status: statusInfo, detail: "UNKNOWN (no socket path)"}, false
	}
	if _, err := os.Stat(opts.Socket); err != nil {
		return check{label: "daemon", status: statusInfo, detail: "STOPPED"}, false
	}
	return check{label: "daemon", status: statusOK, detail: "RUNNING"}, true
}

func checkDatabase(opts daemon.Options) check {
	if opts.DBPath == "" {
		return check{label: "database", status: statusProblem, detail: "ERROR: no db path configured"}
	}
	switch _, err := os.Stat(opts.DBPath); {
	case os.IsNotExist(err):
		// Created lazily on the first recorded command.
		return check{label: "database", status: statusInfo, detail: "not found (first run? run some commands to create it)"}
	case err != nil:
		return check{label: "database", status: statusProblem, detail: fmt.Sprintf("ERROR (%v)", err)}
	default:
		return check{label: "database", status: statusOK, detail: "OK"}
	}
}

// daemonStats reports live graph figures. Failure to reach the daemon yields
// nothing rather than an error: checkDaemon already covers reachability.
func daemonStats() []check {
	var stats ipc.StatsResponse
	if err := unmarshalResponse(ipc.Request{Op: "stats"}, &stats); err != nil {
		return nil
	}
	return []check{
		{label: "graph size", status: statusInfo, detail: fmt.Sprintf("%d transitions", stats.Size)},
		{label: "half-life", status: statusInfo, detail: stats.HalfLife},
		{label: "alpha", status: statusInfo, detail: fmt.Sprintf("%.2f", stats.Alpha)},
	}
}

// checkShellIntegration verifies both halves of the install: that the
// integration script exists, and that the shell's rc file actually sources it.
// A present script that nothing sources is the most common silent failure.
func checkShellIntegration() []check {
	shell := detectShell()
	if shell == "" {
		return []check{{label: "shell integration", status: statusInfo, detail: "unknown (SHELL not set)"}}
	}

	integrationPath, err := findIntegration(shell)
	if err != nil {
		return []check{{label: "shell integration", status: statusProblem, detail: fmt.Sprintf("not found (run: hunch init %s)", shell)}}
	}
	if _, err := os.Stat(integrationPath); os.IsNotExist(err) {
		return []check{{label: "shell integration", status: statusProblem, detail: fmt.Sprintf("file missing (run: hunch init %s)", shell)}}
	}

	checks := []check{{
		label:  "shell integration",
		status: statusOK,
		detail: fmt.Sprintf("OK (%s, %s)", shell, integrationPath),
	}}
	return append(checks, checkRcFile(shell))
}

func checkRcFile(shell string) check {
	rcPath := rcFilePathShell(shell)
	data, err := os.ReadFile(rcPath)
	if err != nil {
		return check{label: "rc file", status: statusProblem, indent: true, detail: fmt.Sprintf("WARNING (%s not found)", rcPath)}
	}
	if !hasHunchSourceLine(string(data)) {
		return check{label: "rc file", status: statusProblem, indent: true, detail: fmt.Sprintf("WARNING (%s does not source hunch)", rcPath)}
	}
	return check{label: "rc file", status: statusOK, indent: true, detail: "OK (source line found)"}
}

// hasHunchSourceLine looks for an active source line, ignoring comments so a
// commented-out install is correctly reported as missing.
func hasHunchSourceLine(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.Contains(trimmed, "hunch") {
			continue
		}
		if strings.HasPrefix(trimmed, "source ") ||
			strings.HasPrefix(trimmed, ". ") ||
			strings.HasPrefix(trimmed, "Import-Module ") {
			return true
		}
	}
	return false
}
