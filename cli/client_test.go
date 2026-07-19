package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResetDatabaseFilesRemovesWALSidecars(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "hunch.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.WriteFile(db+suffix, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := resetDatabaseFiles(db); err != nil {
		t.Fatalf("resetDatabaseFiles: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(db + suffix); !os.IsNotExist(err) {
			t.Errorf("%s still exists after reset", db+suffix)
		}
	}
}

// Deleting data that is already gone is success, not an error.
func TestResetDatabaseFilesIsIdempotent(t *testing.T) {
	db := filepath.Join(t.TempDir(), "hunch.db")
	if err := resetDatabaseFiles(db); err != nil {
		t.Errorf("reset of a nonexistent database returned %v, want nil", err)
	}
}

func TestResetDatabaseFilesRejectsEmptyPath(t *testing.T) {
	if err := resetDatabaseFiles(""); err == nil {
		t.Error("expected an error when no database path is configured")
	}
}
