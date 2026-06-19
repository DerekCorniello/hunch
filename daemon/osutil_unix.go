//go:build unix

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// unixLocker implements Locker via flock(2).
type unixLocker struct {
	f *os.File
}

func (l *unixLocker) Lock() error {
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK {
		return ErrLocked
	}
	return err
}

func (l *unixLocker) Unlock() error {
	return syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
}

func (l *unixLocker) Close() error {
	if err := l.Unlock(); err != nil {
		return fmt.Errorf("unlock lock file: %w", err)
	}
	return l.f.Close()
}

// OpenLock opens or creates the lock file at path and returns a Locker.
func OpenLock(path string) (Locker, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	return &unixLocker{f: f}, nil
}

// processExists checks whether a process with the given PID is alive.
func processExists(pid int) (bool, error) {
	err := syscall.Kill(pid, syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if err == syscall.ESRCH {
		return false, nil
	}
	return false, err
}

func xdgDir(env, fallback string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := os.Getenv(env)
	if d == "" {
		d = filepath.Join(home, fallback)
	}
	return d, nil
}

// CacheDir returns the OS-specific cache directory for hunch.
// Uses XDG_CACHE_HOME, falling back to ~/.cache.
func CacheDir() (string, error) {
	return xdgDir("XDG_CACHE_HOME", ".cache")
}

// DataDir returns the OS-specific data directory for hunch (for the DB).
// Uses XDG_DATA_HOME, falling back to ~/.local/share.
func DataDir() (string, error) {
	return xdgDir("XDG_DATA_HOME", ".local/share")
}

// ConfigDir returns the OS-specific config directory for hunch.
// Uses XDG_CONFIG_HOME, falling back to ~/.config.
func ConfigDir() (string, error) {
	return xdgDir("XDG_CONFIG_HOME", ".config")
}
