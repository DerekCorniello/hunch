//go:build windows

package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// processExists checks whether a process with the given PID is alive.
func processExists(pid int) (bool, error) {
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == syscall.ERROR_INVALID_PARAMETER {
			return false, nil
		}
		return false, err
	}
	syscall.CloseHandle(handle)
	return true, nil
}

// windowsLocker implements Locker via LockFileEx.
type windowsLocker struct {
	f *os.File
}

func (l *windowsLocker) Lock() error {
	return errors.New("file locking not yet implemented on Windows")
}

func (l *windowsLocker) Unlock() error {
	return nil
}

func (l *windowsLocker) Close() error {
	return l.f.Close()
}

// OpenLock opens or creates the lock file at path and returns a Locker.
func OpenLock(path string) (Locker, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	return &windowsLocker{f: f}, nil
}

// SocketURL returns the named pipe URL for Windows.
func SocketURL(path string) string {
	return `\\.\pipe\` + path
}

// CacheDir returns the Windows cache directory for hunch.
// Uses %LocalAppData%, falling back to %TEMP%.
func CacheDir() (string, error) {
	d := os.Getenv("LocalAppData")
	if d == "" {
		d = os.Getenv("TEMP")
		if d == "" {
			return "", errors.New("neither LocalAppData nor TEMP is set")
		}
	}
	return filepath.Join(d, "hunch"), nil
}

// DataDir returns the Windows data directory for hunch.
func DataDir() (string, error) {
	d := os.Getenv("LocalAppData")
	if d == "" {
		d = os.Getenv("TEMP")
		if d == "" {
			return "", errors.New("neither LocalAppData nor TEMP is set")
		}
	}
	return filepath.Join(d, "hunch"), nil
}

// ConfigDir returns the Windows config directory for hunch.
func ConfigDir() (string, error) {
	d := os.Getenv("AppData")
	if d == "" {
		return "", errors.New("AppData is not set")
	}
	return filepath.Join(d, "hunch"), nil
}
