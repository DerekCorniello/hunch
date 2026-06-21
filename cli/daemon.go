package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/DerekCorniello/hunch/daemon"
)

// ErrDaemonNotRunning is returned by cmdDaemonStatus when the daemon is not
// running, so callers (e.g. the shell integration) can detect this via exit
// code alone. The human-readable reason is already printed to stdout.
var ErrDaemonNotRunning = errors.New("hunch daemon is not running")

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

	fs := flag.NewFlagSet("hunch daemon run", flag.ContinueOnError)
	seedPath := fs.String("seed", "", "path to seed JSON for initial training")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts.SeedPath = *seedPath

	if opts.Socket == "" {
		return fmt.Errorf("could not determine socket path; set HUNCH_SOCKET")
	}
	if opts.DBPath == "" {
		return fmt.Errorf("could not determine database path; set HUNCH_DB_PATH")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := daemon.Run(ctx, opts); err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
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

	if err := stopProcess(pid); err != nil {
		return fmt.Errorf("signal daemon: %w", err)
	}

	if err := waitForSocketRemoval(opts.Socket, 5*time.Second); err != nil {
		_ = forceStopProcess(pid)
		if err2 := waitForSocketRemoval(opts.Socket, 2*time.Second); err2 != nil {
			return fmt.Errorf("daemon did not stop: %w", err)
		}
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
		return ErrDaemonNotRunning
	}

	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("hunch daemon is stopped (no pid file)")
		return ErrDaemonNotRunning
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		fmt.Println("hunch daemon is stopped (invalid pid)")
		return ErrDaemonNotRunning
	}

	alive, err := daemon.ProcessExists(pid)
	if err != nil {
		return fmt.Errorf("check process: %w", err)
	}
	if !alive {
		fmt.Println("hunch daemon is stopped (stale lock)")
		return ErrDaemonNotRunning
	}

	fmt.Printf("hunch daemon is running (pid: %d, socket: %s)\n", pid, socketPath)
	return nil
}

// maxDaemonLogSize is the size threshold at which the daemon log is rotated
// before each daemon start, to bound unattended growth (e.g. from a daemon
// that crash-loops and keeps appending to the same file).
const maxDaemonLogSize = 10 * 1024 * 1024 // 10 MiB

// openDaemonLogFile opens the daemon log for appending, rotating it to
// hunch.log.old first if it has grown past maxDaemonLogSize.
func openDaemonLogFile(logPath string) (*os.File, error) {
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxDaemonLogSize {
		_ = os.Rename(logPath, logPath+".old")
	}
	return os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
}

func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := daemon.Dial(path, 50*time.Millisecond)
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
