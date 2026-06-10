//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32    = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = modkernel32.NewProc("LockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

type windowsLocker struct {
	f *os.File
}

func (l *windowsLocker) Lock() error {
	var bytesToLockLow uint32 = 1
	var bytesToLockHigh uint32 = 0

	_OVERLAPPED := [8]byte{}

	ret, _, err := procLockFileEx.Call(
		uintptr(l.f.Fd()),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		uintptr(bytesToLockLow),
		uintptr(bytesToLockHigh),
		uintptr(unsafe.Pointer(&_OVERLAPPED[0])),
	)
	if ret == 0 {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
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

	if err := windows.UnlockFileEx(
		windows.Handle(l.f.Fd()),
		0,
		bytesToUnlockLow,
		bytesToUnlockHigh,
		&windows.Overlapped{},
	); err != nil {
		return fmt.Errorf("UnlockFileEx: %w", err)
	}
	return nil
}

func (l *windowsLocker) Close() error {
	l.Unlock()
	return l.f.Close()
}

func OpenLock(path string) (Locker, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	return &windowsLocker{f: f}, nil
}

func processExists(pid int) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, err
	}
	return exitCode == 259, nil // STILL_ACTIVE
}

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

func ConfigDir() (string, error) {
	d := os.Getenv("AppData")
	if d == "" {
		return "", errors.New("AppData is not set")
	}
	return d, nil
}
