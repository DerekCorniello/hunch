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

func TestMigrateSetsVersionAndIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	st, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}

	var version int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("user_version = %d, want %d", version, len(migrations))
	}
	st.close()

	// Reopening must be a no-op: no migration re-runs, schema still usable.
	st2, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.close()

	var version2 int
	if err := st2.db.QueryRow(`PRAGMA user_version`).Scan(&version2); err != nil {
		t.Fatalf("read user_version after reopen: %v", err)
	}
	if version2 != len(migrations) {
		t.Errorf("user_version after reopen = %d, want %d", version2, len(migrations))
	}
	if _, err := st2.load(); err != nil {
		t.Errorf("load after reopen: %v", err)
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

func TestStoreCWDAndOutcomeRoundtrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := openStore(dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.close()

	seed := []graph.Transition{{
		State:        []string{"", "cmd"},
		Next:         "make",
		Count:        7,
		CWDs:         map[string]int{"/proj": 5, "/other": 2},
		NextSuccess:  4,
		NextFailure:  3,
		PriorSuccess: 6,
		PriorFailure: 1,
	}}
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
	got := loaded[0]
	if got.NextSuccess != 4 || got.NextFailure != 3 || got.PriorSuccess != 6 || got.PriorFailure != 1 {
		t.Errorf("outcome counters = (%d,%d,%d,%d), want (4,3,6,1)",
			got.NextSuccess, got.NextFailure, got.PriorSuccess, got.PriorFailure)
	}
	if got.CWDs["/proj"] != 5 || got.CWDs["/other"] != 2 {
		t.Errorf("cwd histogram = %v, want map[/proj:5 /other:2]", got.CWDs)
	}

	// Pruning a transition must also drop its CWD histogram rows.
	if err := st.prune(seed, nil); err != nil {
		t.Fatalf("prune: %v", err)
	}
	loaded, err = st.load()
	if err != nil {
		t.Fatalf("load after prune: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 transitions after prune, got %d", len(loaded))
	}
	var cwdRows int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM transition_cwd`).Scan(&cwdRows); err != nil {
		t.Fatalf("count transition_cwd: %v", err)
	}
	if cwdRows != 0 {
		t.Errorf("transition_cwd rows after prune = %d, want 0", cwdRows)
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
