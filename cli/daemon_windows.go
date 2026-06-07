//go:build windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/DerekCorniello/hunch/daemon"
)

func cmdDaemonStart() error {
	opts := daemon.LoadConfig()
	if opts.Socket == "" {
		return fmt.Errorf("could not determine socket path; set HUNCH_SOCKET")
	}

	selfPath := opts.DaemonBin
	if selfPath == "" {
		var err error
		selfPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve binary path: %w", err)
		}
	}

	cmd := exec.Command(selfPath, "daemon", "run")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Stdout = nil
	cmd.Stderr = nil
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
