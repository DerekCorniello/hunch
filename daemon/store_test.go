package daemon

import (
	"os"
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

func TestOpenStore_BadPath(t *testing.T) {
	// A path to a directory instead of a file should fail.
	_, err := openStore(t.TempDir())
	if err == nil {
		t.Fatal("expected error opening store with directory path")
	}
}

func TestOpenStore_CorruptDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(dbPath, []byte("not a valid sqlite database"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := openStore(dbPath)
	if err == nil {
		t.Error("expected error opening corrupt database")
	}
}

func TestStoreSave_EmptyTransitions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	st, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.close()

	if err := st.save(nil); err != nil {
		t.Fatalf("save(nil): %v", err)
	}

	if err := st.save([]graph.Transition{}); err != nil {
		t.Fatalf("save(empty): %v", err)
	}

	loaded, err := st.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 transitions, got %d", len(loaded))
	}
}

func TestStorePruneRemovesRowsPermanently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	st, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.close()

	seed := []graph.Transition{
		{State: []string{"", "a"}, Next: "stale", Count: 1},
		{State: []string{"", "b"}, Next: "fresh", Count: 1},
	}
	if err := st.save(seed); err != nil {
		t.Fatalf("save: %v", err)
	}
	raws := map[string]map[string]int{
		"stale": {"stale cmd": 1},
		"fresh": {"fresh cmd": 1},
	}
	if err := st.saveRawExamples(raws); err != nil {
		t.Fatalf("saveRawExamples: %v", err)
	}

	pruned := []graph.Transition{{State: []string{"", "a"}, Next: "stale"}}
	if err := st.prune(pruned, []string{"stale"}); err != nil {
		t.Fatalf("prune: %v", err)
	}

	// Pruned rows must stay gone on reload (no upsert resurrection).
	loaded, err := st.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("transitions after prune = %d, want 1", len(loaded))
	}
	if loaded[0].Next != "fresh" {
		t.Errorf("surviving next = %q, want fresh", loaded[0].Next)
	}

	rawsLoaded, err := st.loadRawExamples()
	if err != nil {
		t.Fatalf("loadRawExamples: %v", err)
	}
	if _, ok := rawsLoaded["stale"]; ok {
		t.Error("orphaned raw examples for 'stale' were not pruned")
	}
	if _, ok := rawsLoaded["fresh"]; !ok {
		t.Error("raw examples for 'fresh' should survive")
	}
}

func TestStorePruneEmptyIsNoop(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	st, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.close()

	if err := st.prune(nil, nil); err != nil {
		t.Fatalf("prune(nil, nil): %v", err)
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
