package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func startDaemon(t *testing.T, opts Options) (context.Context, context.CancelFunc, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	opts.Socket = filepath.Join(t.TempDir(), "hunch.sock")
	opts.DBPath = filepath.Join(t.TempDir(), "hunch.db")

	go func() {
		if err := Run(ctx, opts); err != nil && ctx.Err() == nil {
			t.Logf("daemon error: %v", err)
		}
	}()

	waitForSocket(t, opts.Socket, 5*time.Second)
	return ctx, cancel, opts.Socket
}

func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("socket %s did not become available within %v", path, timeout)
}

func dial(t *testing.T, socket string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func writeJSON(t *testing.T, conn net.Conn, v interface{}) {
	t.Helper()
	if err := json.NewEncoder(conn).Encode(v); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readJSON(t *testing.T, conn net.Conn, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(conn).Decode(v); err != nil {
		t.Fatalf("read: %v", err)
	}
}

func TestDaemonRecordPredictRoundtrip(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "record",
		"state": []string{"", "git add ."},
		"next":  "git commit -m \"init\"",
	})
	var resp map[string]interface{}
	readJSON(t, conn, &resp)
	if resp["ok"] != true {
		t.Fatalf("record response: %v", resp)
	}
	conn.Close()

	conn = dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "predict",
		"state": []string{"", "git add PATH"},
		"limit": 5,
	})
	var predResp struct {
		Suggestions []struct {
			Template string  `json:"template"`
			Score    float64 `json:"score"`
			Count    int     `json:"count"`
		} `json:"suggestions"`
	}
	readJSON(t, conn, &predResp)
	conn.Close()

	if len(predResp.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(predResp.Suggestions))
	}
	if predResp.Suggestions[0].Template != "git commit FLAG STR" {
		t.Errorf("template = %q, want %q", predResp.Suggestions[0].Template, "git commit FLAG STR")
	}
	if predResp.Suggestions[0].Score <= 0 {
		t.Errorf("score = %f, want > 0", predResp.Suggestions[0].Score)
	}
	if predResp.Suggestions[0].Count != 1 {
		t.Errorf("count = %d, want 1", predResp.Suggestions[0].Count)
	}
}

func TestDaemonPersistence(t *testing.T) {
	opts := LoadConfig()
	dbPath := filepath.Join(t.TempDir(), "hunch.db")
	sockPath := filepath.Join(t.TempDir(), "hunch.sock")
	opts.DBPath = dbPath
	opts.Socket = sockPath

	ctx, cancel := context.WithCancel(context.Background())
	go Run(ctx, opts)
	waitForSocket(t, sockPath, 5*time.Second)

	conn := dial(t, sockPath)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "record",
		"state": []string{"", "cmd"},
		"next":  "next-cmd",
	})
	var resp map[string]interface{}
	readJSON(t, conn, &resp)
	conn.Close()

	cancel()
	time.Sleep(200 * time.Millisecond)

	ctx2, cancel2 := context.WithCancel(context.Background())
	go Run(ctx2, opts)
	waitForSocket(t, sockPath, 5*time.Second)

	conn = dial(t, sockPath)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "predict",
		"state": []string{"", "cmd"},
		"limit": 5,
	})
	var predResp struct {
		Suggestions []struct {
			Template string  `json:"template"`
			Score    float64 `json:"score"`
			Count    int     `json:"count"`
		} `json:"suggestions"`
	}
	readJSON(t, conn, &predResp)
	conn.Close()

	if len(predResp.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion after restart, got %d", len(predResp.Suggestions))
	}
	if predResp.Suggestions[0].Count < 1 {
		t.Errorf("count = %d after restart, want >= 1", predResp.Suggestions[0].Count)
	}

	cancel2()
}

func TestDaemonReset(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "record",
		"state": []string{"", "cmd"},
		"next":  "next-cmd",
	})
	var resp map[string]interface{}
	readJSON(t, conn, &resp)
	conn.Close()

	conn = dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "predict",
		"state": []string{"", "cmd"},
		"limit": 5,
	})
	var predResp struct {
		Suggestions []interface{} `json:"suggestions"`
	}
	readJSON(t, conn, &predResp)
	conn.Close()
	if len(predResp.Suggestions) == 0 {
		t.Fatal("expected suggestions before reset")
	}

	conn = dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op": "reset",
	})
	readJSON(t, conn, &resp)
	conn.Close()

	conn = dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "predict",
		"state": []string{"", "cmd"},
		"limit": 5,
	})
	var emptyResp struct {
		Suggestions []interface{} `json:"suggestions"`
	}
	readJSON(t, conn, &emptyResp)
	conn.Close()

	if len(emptyResp.Suggestions) != 0 {
		t.Errorf("expected 0 suggestions after reset, got %d", len(emptyResp.Suggestions))
	}
}

func TestDaemonExport(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "record",
		"state": []string{"", "git add ."},
		"next":  "git commit -m \"init\"",
	})
	var resp map[string]interface{}
	readJSON(t, conn, &resp)
	conn.Close()

	conn = dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op": "export",
	})
	var exportResp struct {
		Transitions []struct {
			State    []string `json:"state"`
			Next     string   `json:"next"`
			Count    int      `json:"count"`
			LastSeen string   `json:"last_seen"`
		} `json:"transitions"`
	}
	readJSON(t, conn, &exportResp)
	conn.Close()

	if len(exportResp.Transitions) != 1 {
		t.Fatalf("expected 1 transition in export, got %d", len(exportResp.Transitions))
	}
	if exportResp.Transitions[0].Next != "git commit FLAG STR" {
		t.Errorf("exported next = %q, want %q", exportResp.Transitions[0].Next, "git commit FLAG STR")
	}
}

func TestDaemonExportEmpty(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op": "export",
	})
	var exportResp struct {
		Transitions []interface{} `json:"transitions"`
	}
	readJSON(t, conn, &exportResp)
	conn.Close()

	if exportResp.Transitions == nil {
		t.Error("export on empty graph should return empty array, not null")
	}
	if len(exportResp.Transitions) != 0 {
		t.Errorf("expected 0 transitions, got %d", len(exportResp.Transitions))
	}
}

func TestDaemonPredictFiltersByPrefix(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "record",
		"state": []string{"", "cmd"},
		"next":  "git push origin main",
	})
	var resp map[string]interface{}
	readJSON(t, conn, &resp)
	conn.Close()

	conn = dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":     "predict",
		"state":  []string{"", "cmd"},
		"prefix": "git pus",
		"limit":  5,
	})
	var predResp struct {
		Suggestions []struct {
			Template string `json:"template"`
		} `json:"suggestions"`
	}
	readJSON(t, conn, &predResp)
	conn.Close()

	if len(predResp.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion matching prefix, got %d", len(predResp.Suggestions))
	}
	if predResp.Suggestions[0].Template != "git push STR" {
		t.Errorf("template = %q, want %q", predResp.Suggestions[0].Template, "git push STR")
	}
}

func TestDaemonPredictPrefixNoMatch(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "record",
		"state": []string{"", "cmd"},
		"next":  "git push origin main",
	})
	var resp map[string]interface{}
	readJSON(t, conn, &resp)
	conn.Close()

	conn = dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":     "predict",
		"state":  []string{"", "cmd"},
		"prefix": "docker",
		"limit":  5,
	})
	var predResp struct {
		Suggestions []interface{} `json:"suggestions"`
	}
	readJSON(t, conn, &predResp)
	conn.Close()

	if len(predResp.Suggestions) != 0 {
		t.Errorf("expected 0 suggestions for non-matching prefix, got %d", len(predResp.Suggestions))
	}
}

func TestDaemonBadRequest(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op": "unknown_op_xyz",
	})
	var resp struct {
		Error string `json:"error"`
	}
	readJSON(t, conn, &resp)
	conn.Close()

	if resp.Error == "" {
		t.Error("expected error for unknown op, got empty")
	}
	if !strings.Contains(resp.Error, "unknown op") {
		t.Errorf("error = %q, want to contain 'unknown op'", resp.Error)
	}
}

func TestDaemonMalformedJSON(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}

	_, err = conn.Write([]byte("{{{not json\n"))
	if err != nil {
		t.Fatal(err)
	}

	var resp struct {
		Error string `json:"error"`
	}
	readJSON(t, conn, &resp)
	conn.Close()

	if resp.Error == "" {
		t.Error("expected error for malformed JSON, got empty")
	}
}

func TestDaemonConcurrentRecords(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("unix", socket)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			writeJSON(t, conn, map[string]interface{}{
				"op":    "record",
				"state": []string{"", "cmd"},
				"next":  "next",
			})
			var resp map[string]interface{}
			readJSON(t, conn, &resp)
		}()
	}
	wg.Wait()

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "predict",
		"state": []string{"", "cmd"},
		"limit": 5,
	})
	var predResp struct {
		Suggestions []struct {
			Count int `json:"count"`
		} `json:"suggestions"`
	}
	readJSON(t, conn, &predResp)
	conn.Close()

	if len(predResp.Suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d", len(predResp.Suggestions))
	}
	if predResp.Suggestions[0].Count != 50 {
		t.Errorf("count = %d, want 50", predResp.Suggestions[0].Count)
	}
}

func TestDaemonStaleLockRecovery(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "hunch.lock")
	pidPath := filepath.Join(dir, "hunch.pid")
	sockPath := filepath.Join(dir, "hunch.sock")
	dbPath := filepath.Join(dir, "hunch.db")

	os.WriteFile(lockPath, []byte("stale"), 0644)
	os.WriteFile(pidPath, []byte("999999999"), 0644)

	opts := LoadConfig()
	opts.Socket = sockPath
	opts.DBPath = dbPath

	ctx, cancel := context.WithCancel(context.Background())
	go Run(ctx, opts)
	waitForSocket(t, sockPath, 5*time.Second)

	conn := dial(t, sockPath)
	writeJSON(t, conn, map[string]interface{}{
		"op": "export",
	})
	var exportResp struct {
		Transitions []interface{} `json:"transitions"`
	}
	readJSON(t, conn, &exportResp)
	conn.Close()

	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Fatal("expected lock file to be recreated after stale lock recovery")
	}

	cancel()
}

func TestDaemonContextCancellation(t *testing.T) {
	opts := LoadConfig()
	opts.Socket = filepath.Join(t.TempDir(), "hunch.sock")
	opts.DBPath = filepath.Join(t.TempDir(), "hunch.db")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, opts)
	}()
	waitForSocket(t, opts.Socket, 5*time.Second)

	conn := dial(t, opts.Socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":    "record",
		"state": []string{"", "cmd"},
		"next":  "next",
	})
	var resp map[string]interface{}
	readJSON(t, conn, &resp)
	conn.Close()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not shut down within 3s after context cancellation")
	}

	if _, err := os.Stat(opts.Socket); !os.IsNotExist(err) {
		t.Error("socket file should be removed after shutdown")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(opts.DBPath), "hunch.pid")); !os.IsNotExist(err) {
		t.Error("PID file should be removed after shutdown")
	}
}

func TestDaemonExportOrderStable(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	for i := 0; i < 3; i++ {
		conn := dial(t, socket)
		writeJSON(t, conn, map[string]interface{}{
			"op":    "record",
			"state": []string{"a", "b"},
			"next":  strconv.Itoa(i),
		})
		var resp map[string]interface{}
		readJSON(t, conn, &resp)
		conn.Close()
	}

	var lastExport string
	for range 5 {
		conn := dial(t, socket)
		writeJSON(t, conn, map[string]interface{}{
			"op": "export",
		})
		var exportResp struct {
			Transitions []struct {
				Next string `json:"next"`
			} `json:"transitions"`
		}
		readJSON(t, conn, &exportResp)
		conn.Close()

		var order string
		for _, tx := range exportResp.Transitions {
			order += tx.Next
		}
		if lastExport != "" && order != lastExport {
			t.Fatalf("export order changed: was %q, now %q", lastExport, order)
		}
		lastExport = order
	}
}

func TestDaemonNormalizeOp(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op":   "normalize",
		"next": "git commit -m \"init\"",
	})
	var resp struct {
		Raw      string `json:"raw"`
		Template string `json:"template"`
	}
	readJSON(t, conn, &resp)
	conn.Close()

	if resp.Raw != "git commit -m \"init\"" {
		t.Errorf("raw = %q, want %q", resp.Raw, "git commit -m \"init\"")
	}
	if resp.Template != "git commit FLAG STR" {
		t.Errorf("template = %q, want %q", resp.Template, "git commit FLAG STR")
	}
}

func TestDaemonStatsOp(t *testing.T) {
	_, _, socket := startDaemon(t, LoadConfig())

	conn := dial(t, socket)
	writeJSON(t, conn, map[string]interface{}{
		"op": "stats",
	})
	var resp struct {
		Size     int     `json:"size"`
		HalfLife string  `json:"half_life"`
		Alpha    float64 `json:"alpha"`
		DBPath   string  `json:"db_path"`
	}
	readJSON(t, conn, &resp)
	conn.Close()

	if resp.Size != 0 {
		t.Errorf("size = %d, want 0", resp.Size)
	}
	if resp.Alpha != 0.5 {
		t.Errorf("alpha = %f, want 0.5", resp.Alpha)
	}
}
