package daemon

import (
	"path/filepath"
	"testing"

	"github.com/DerekCorniello/hunch/core/graph"
)

func TestOpenStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	st, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.close()

	if st.db == nil {
		t.Fatal("store db is nil")
	}
}

func TestStoreSaveAndLoad(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	st, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.close()

	transitions, err := st.load()
	if err != nil {
		t.Fatalf("load on empty: %v", err)
	}
	if len(transitions) != 0 {
		t.Errorf("expected 0 transitions on empty db, got %d", len(transitions))
	}

	seed := []graph.Transition{
		{
			State: []string{"", "git add PATH"},
			Next:  "git commit STR",
			Count: 5,
		},
	}
	if err := st.save(seed); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := st.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(loaded))
	}
	if loaded[0].Next != "git commit STR" {
		t.Errorf("next = %q, want %q", loaded[0].Next, "git commit STR")
	}
	if loaded[0].Count != 5 {
		t.Errorf("count = %d, want 5", loaded[0].Count)
	}
}

func TestStoreSaveUpserts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	st, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.close()

	seed := []graph.Transition{
		{
			State: []string{"", "cmd"},
			Next:  "next-cmd",
			Count: 3,
		},
	}
	if err := st.save(seed); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Upsert with higher count.
	seed[0].Count = 10
	if err := st.save(seed); err != nil {
		t.Fatalf("upsert save: %v", err)
	}

	loaded, err := st.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 transition after upsert, got %d", len(loaded))
	}
	if loaded[0].Count != 10 {
		t.Errorf("count after upsert = %d, want 10", loaded[0].Count)
	}
}

func TestStoreClear(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	st, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.close()

	seed := []graph.Transition{
		{
			State: []string{"", "cmd"},
			Next:  "next",
			Count: 1,
		},
	}
	if err := st.save(seed); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := st.clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	loaded, err := st.load()
	if err != nil {
		t.Fatalf("load after clear: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 transitions after clear, got %d", len(loaded))
	}
}
