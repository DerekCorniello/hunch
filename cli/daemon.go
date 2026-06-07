package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/DerekCorniello/hunch/daemon"
)

func cmdDaemon(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hunch daemon <action>\n\nactions: run, start, stop, status")
	}

	switch args[0] {
	case "run":
		return cmdDaemonRun(args[1:])
	case "start":
		return cmdDaemonStart()
	case "stop":
		return cmdDaemonStop()
	case "status":
		return cmdDaemonStatus()
	default:
		return fmt.Errorf("unknown daemon action: %q\n\nactions: run, start, stop, status", args[0])
	}
}

func cmdDaemonRun(args []string) error {
	opts := daemon.LoadConfig()
	if opts.Socket == "" {
		return fmt.Errorf("could not determine socket path; set HUNCH_SOCKET")
	}
	if opts.DBPath == "" {
		return fmt.Errorf("could not determine database path; set HUNCH_DB_PATH")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := daemon.Run(ctx, opts); err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	return nil
}

func cmdDaemonStart() error {
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}

	opts := daemon.LoadConfig()
	if opts.Socket == "" {
		return fmt.Errorf("could not determine socket path; set HUNCH_SOCKET")
	}

	cmd := exec.Command(selfPath, "daemon", "run")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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

func cmdDaemonStop() error {
	opts := daemon.LoadConfig()
	pidPath := filepath.Join(filepath.Dir(opts.DBPath), "hunch.pid")

	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("hunch daemon is not running")
		}
		return fmt.Errorf("read pid file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		return fmt.Errorf("invalid pid: %w", err)
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal daemon: %w", err)
	}

	if err := waitForSocketRemoval(opts.Socket, 5*time.Second); err != nil {
		return fmt.Errorf("daemon did not stop: %w", err)
	}

	fmt.Println("hunch daemon stopped")
	return nil
}

func cmdDaemonStatus() error {
	opts := daemon.LoadConfig()
	pidPath := filepath.Join(filepath.Dir(opts.DBPath), "hunch.pid")
	socketPath := opts.Socket

	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		fmt.Println("hunch daemon is stopped")
		return nil
	}

	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("hunch daemon is stopped (no pid file)")
		return nil
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		fmt.Println("hunch daemon is stopped (invalid pid)")
		return nil
	}

	alive, err := daemon.ProcessExists(pid)
	if err != nil {
		return fmt.Errorf("check process: %w", err)
	}
	if !alive {
		fmt.Println("hunch daemon is stopped (stale lock)")
		return nil
	}

	fmt.Printf("hunch daemon is running (pid: %d, socket: %s)\n", pid, socketPath)
	return nil
}

func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("socket %s did not appear within %v", path, timeout)
}

func waitForSocketRemoval(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("socket %s was not removed within %v", path, timeout)
}
