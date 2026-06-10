//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	modkernel32    = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = modkernel32.NewProc("LockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

// windowsLocker implements Locker via LockFileEx.
type windowsLocker struct {
	f *os.File
}

func (l *windowsLocker) Lock() error {
	var bytesToLockLow uint32 = 1
	var bytesToLockHigh uint32 = 0

	_OVERLAPPED := [8]byte{} // OVERLAPPED structure (simplified for advisory lock)

	ret, _, err := procLockFileEx.Call(
		uintptr(l.f.Fd()),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		uintptr(bytesToLockLow),
		uintptr(bytesToLockHigh),
		uintptr(unsafe.Pointer(&_OVERLAPPED[0])),
	)
	if ret == 0 {
		if errors.Is(err, syscall.ERROR_LOCK_VIOLATION) || errors.Is(err, syscall.ERROR_INVALID_PARAMETER) {
			return ErrLocked
		}
		return fmt.Errorf("LockFileEx: %w", err)
	}
	return nil
}

func (l *windowsLocker) Unlock() error {
	var bytesToUnlockLow uint32 = 1
	var bytesToUnlockHigh uint32 = 0

	_OVERLAPPED := [8]byte{}

	ret, _, err := syscall.UnlockFileEx(
		uintptr(l.f.Fd()),
		0,
		uintptr(bytesToUnlockLow),
		uintptr(bytesToUnlockHigh),
		uintptr(unsafe.Pointer(&_OVERLAPPED[0])),
	)
	if ret == 0 {
		return fmt.Errorf("UnlockFileEx: %w", err)
	}
	return nil
}

func (l *windowsLocker) Close() error {
	l.Unlock()
	return l.f.Close()
}

// OpenLock opens or creates the lock file at path and returns a Locker.
func OpenLock(path string) (Locker, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	return &windowsLocker{f: f}, nil
}

// processExists checks whether a process with the given PID is alive.
func processExists(pid int) (bool, error) {
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, syscall.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		return false, err
	}
	defer syscall.CloseHandle(handle)

	var exitCode uint32
	err = syscall.GetExitCodeProcess(handle, &exitCode)
	if err != nil {
		return false, err
	}
	// exitCode == STILL_ACTIVE (259) means process is still running
	return exitCode == 259, nil
}

// CacheDir returns the Windows cache directory.
func CacheDir() (string, error) {
	d := os.Getenv("LocalAppData")
	if d == "" {
		d = os.Getenv("TEMP")
		if d == "" {
			return "", errors.New("neither LocalAppData nor TEMP is set")
		}
	}
	return d, nil
}

// DataDir returns the Windows data directory.
func DataDir() (string, error) {
	d := os.Getenv("LocalAppData")
	if d == "" {
		d = os.Getenv("TEMP")
		if d == "" {
			return "", errors.New("neither LocalAppData nor TEMP is set")
		}
	}
	return d, nil
}

// ConfigDir returns the Windows config directory.
func ConfigDir() (string, error) {
	d := os.Getenv("AppData")
	if d == "" {
		return "", errors.New("AppData is not set")
	}
	return d, nil
}
