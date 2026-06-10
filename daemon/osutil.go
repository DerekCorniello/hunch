package daemon

import (
	"errors"
	"net"
	"time"
)

// Locker abstracts advisory file locking.
// On Unix this is flock(2); on Windows it is LockFileEx.
type Locker interface {
	// Lock acquires a non-blocking exclusive lock.
	// Returns ErrLocked if the lock is already held.
	Lock() error
	// Unlock releases the lock.
	Unlock() error
	// Close releases the lock and closes the underlying file.
	Close() error
}

// ErrLocked is returned by Locker.Lock when the lock is held by another process.
var ErrLocked = errors.New("lock already held")

// ProcessExists reports whether a process with the given PID is alive.
func ProcessExists(pid int) (bool, error) {
	return processExists(pid)
}

// Dial connects to the daemon's IPC socket with a timeout.
// Uses Unix domain sockets, which are supported on all platforms
// (Linux, macOS, Windows) since Go 1.20.
func Dial(socket string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", socket, timeout)
}
