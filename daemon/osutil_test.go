package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessExists(t *testing.T) {
	t.Run("self_exists", func(t *testing.T) {
		alive, err := ProcessExists(os.Getpid())
		if err != nil {
			t.Fatalf("ProcessExists(self): %v", err)
		}
		if !alive {
			t.Error("ProcessExists(self) = false, want true")
		}
	})

	t.Run("nonexistent_pid", func(t *testing.T) {
		alive, err := ProcessExists(999999999)
		if err != nil {
			t.Fatalf("ProcessExists(nonexistent): %v", err)
		}
		if alive {
			t.Error("ProcessExists(nonexistent) = true, want false")
		}
	})
}

func TestDial(t *testing.T) {
	t.Run("no_socket", func(t *testing.T) {
		_, err := Dial("/nonexistent/socket", 100*time.Millisecond)
		if err == nil {
			t.Fatal("expected error dialing nonexistent socket")
		}
	})

	t.Run("connect_to_listener", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "test.sock")
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()

		conn, err := Dial(path, time.Second)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		conn.Close()
	})
}

func TestOpenLock(t *testing.T) {
	t.Run("create_and_lock", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "lock")
		lock, err := OpenLock(path)
		if err != nil {
			t.Fatalf("OpenLock: %v", err)
		}
		defer lock.Close()

		if err := lock.Lock(); err != nil {
			t.Fatalf("first Lock: %v", err)
		}

		if err := lock.Unlock(); err != nil {
			t.Fatalf("Unlock: %v", err)
		}
	})

	t.Run("lock_contention", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "lock2")

		lock1, err := OpenLock(path)
		if err != nil {
			t.Fatal(err)
		}
		defer lock1.Close()

		if err := lock1.Lock(); err != nil {
			t.Fatalf("lock1.Lock: %v", err)
		}

		lock2, err := OpenLock(path)
		if err != nil {
			t.Fatal(err)
		}
		defer lock2.Close()

		if err := lock2.Lock(); err != ErrLocked {
			t.Fatalf("lock2.Lock: expected ErrLocked, got %v", err)
		}

		_ = lock1.Unlock()

		if err := lock2.Lock(); err != nil {
			t.Fatalf("lock2.Lock after unlock: %v", err)
		}
		_ = lock2.Unlock()
	})
}

func TestOpenLock_FileCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new_lock_file")

	lock, err := OpenLock(path)
	if err != nil {
		t.Fatalf("OpenLock on new path: %v", err)
	}
	lock.Close()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("OpenLock should create the lock file")
	}
}
