//go:build windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DerekCorniello/hunch/daemon"
	"golang.org/x/sys/windows"
)

func cmdDaemonStart() error {
	opts := daemon.LoadConfig()
	if opts.Socket == "" {
		return fmt.Errorf("could not determine socket path; set HUNCH_SOCKET")
	}

	if err := EnsureIntegrations(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not install integrations: %v\n", err)
	}

	selfPath := opts.DaemonBin
	if selfPath == "" {
		var err error
		selfPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve binary path: %w", err)
		}
	}

	logPath := filepath.Join(filepath.Dir(opts.DBPath), "hunch.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open log file %s: %v\n", logPath, err)
		logFile = nil
	}

	cmd := exec.Command(selfPath, "daemon", "run")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	if err := waitForSocket(opts.Socket, 2*time.Second); err != nil {
		return fmt.Errorf("daemon did not start: %w", err)
	}

	fmt.Printf("hunch daemon started (socket: %s, pid: %d)\n", opts.Socket, cmd.Process.Pid)
	return nil
}
