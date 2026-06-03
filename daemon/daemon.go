package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/predict"
	"github.com/DerekCorniello/hunch/core/types"
)

const (
	flushThreshold = 50
	flushInterval  = 5 * time.Second
)

type daemon struct {
	opts Options
	g    atomic.Pointer[graph.Graph]
	pred atomic.Pointer[predict.Predictor]
	st   *store
	log  *slog.Logger

	lock     Locker
	listener net.Listener

	flushMu sync.Mutex

	dirty   atomic.Int32
	flushCh chan struct{}

	sockPath string
	lockPath string
	pidPath  string
}

// Run starts the daemon and blocks until ctx is cancelled or a fatal
// error occurs. On shutdown the graph is flushed to SQLite before returning.
func Run(ctx context.Context, opts Options) error {
	d := &daemon{
		opts:    opts,
		log:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(opts.LogLevel)})),
		flushCh: make(chan struct{}, 1),
	}
	d.g.Store(graph.New(2))

	// Derive paths from options.
	d.sockPath = opts.Socket
	dataDir := filepath.Dir(opts.DBPath)
	d.lockPath = filepath.Join(dataDir, "hunch.lock")
	d.pidPath = filepath.Join(dataDir, "hunch.pid")

	if err := d.start(ctx); err != nil {
		return err
	}
	defer d.stop()

	// Wait for cancellation.
	<-ctx.Done()
	return nil
}

func (d *daemon) start(ctx context.Context) error {
	// Ensure data directory exists.
	if err := os.MkdirAll(filepath.Dir(d.opts.DBPath), 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// Acquire lock with stale recovery.
	lock, err := OpenLock(d.lockPath)
	if err != nil {
		return fmt.Errorf("open lock: %w", err)
	}
	d.lock = lock

	if err := d.lock.Lock(); err != nil {
		if err != ErrLocked {
			return fmt.Errorf("acquire lock: %w", err)
		}

		if err := d.recoverStaleLock(); err != nil {
			return err // another instance is alive
		}

		if err := d.lock.Lock(); err != nil {
			return fmt.Errorf("acquire lock after recovery: %w", err)
		}
	}

	// Write PID file.
	pid := os.Getpid()
	if err := os.WriteFile(d.pidPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}

	// Open store and load graph.
	st, err := openStore(d.opts.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	d.st = st

	transitions, err := st.load()
	if err != nil {
		return fmt.Errorf("load graph: %w", err)
	}
	if err := d.g.Load().Merge(transitions); err != nil {
		return fmt.Errorf("merge loaded transitions: %w", err)
	}

	// Seed import on first run.
	if d.opts.SeedPath != "" && len(transitions) == 0 {
		if err := d.importSeed(d.opts.SeedPath); err != nil {
			d.log.Warn("seed import failed", "path", d.opts.SeedPath, "error", err)
		}
	}

	// Build predictor.
	d.pred.Store(predict.New(d.g.Load(), d.opts.HalfLife(), d.opts.Alpha))

	// Start flush loop.
	go d.flushLoop(ctx)

	// Start IPC listener.
	listener, err := net.Listen("unix", d.sockPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	d.listener = listener

	d.log.Info("daemon started", "socket", d.sockPath)
	go d.acceptLoop(ctx)
	return nil
}

func (d *daemon) stop() {
	d.log.Info("daemon shutting down")

	if d.listener != nil {
		d.listener.Close()
	}

	d.flush()

	if d.st != nil {
		d.st.close()
	}

	os.Remove(d.sockPath)
	os.Remove(d.pidPath)

	if d.lock != nil {
		d.lock.Close()
	}
}

func (d *daemon) recoverStaleLock() error {
	pidData, err := os.ReadFile(d.pidPath)
	if err != nil {
		return fmt.Errorf("stale lock but no pid file: %w", ErrLocked)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		return fmt.Errorf("stale lock but invalid pid: %w", ErrLocked)
	}

	alive, err := processExists(pid)
	if err != nil {
		return fmt.Errorf("check pid %d: %w", pid, err)
	}
	if alive {
		return fmt.Errorf("another instance is running (pid %d)", pid)
	}

	d.log.Warn("removing stale lock", "pid", pid)
	os.Remove(d.lockPath)
	os.Remove(d.pidPath)
	return nil
}

func (d *daemon) acceptLoop(ctx context.Context) {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			d.log.Error("accept error", "error", err)
			continue
		}
		go d.handleConn(conn)
	}
}

func (d *daemon) handleConn(conn net.Conn) {
	defer conn.Close()

	req, err := parseRequest(conn)
	if err != nil {
		writeError(conn, "bad request")
		return
	}

	switch req.Op {
	case "record":
		d.handleRecord(conn, req)
	case "predict":
		d.handlePredict(conn, req)
	case "reset":
		d.handleReset(conn)
	case "export":
		d.handleExport(conn)
	default:
		writeError(conn, fmt.Sprintf("unknown op: %s", req.Op))
	}
}

func (d *daemon) handleRecord(conn net.Conn, req request) {
	at, err := parseAt(req.At)
	if err != nil {
		writeError(conn, fmt.Sprintf("bad at: %s", err))
		return
	}
	if at.IsZero() {
		at = time.Now()
	}

	d.g.Load().Record(req.State, req.Next, at)
	d.dirty.Add(1)

	if d.dirty.Load() >= flushThreshold {
		select {
		case d.flushCh <- struct{}{}:
		default:
		}
	}

	writeOK(conn)
}

func (d *daemon) handlePredict(conn net.Conn, req request) {
	at, err := parseAt(req.At)
	if err != nil {
		writeError(conn, fmt.Sprintf("bad at: %s", err))
		return
	}
	if at.IsZero() {
		at = time.Now()
	}

	prev := make([]types.Command, len(req.State))
	for i, tmpl := range req.State {
		prev[i] = types.Command{Template: tmpl}
	}

	st := types.State{Previous: prev}
	// Fetch all suggestions (limit=0 means no limit), then filter by
	// prefix server-side, then cap to the requested limit.
	suggestions := d.pred.Load().Predict(st, at, 0)

	if req.Prefix != "" {
		suggestions = filterByPrefix(suggestions, req.Prefix)
	}

	if req.Limit > 0 && len(suggestions) > req.Limit {
		suggestions = suggestions[:req.Limit]
	}

	writeSuggestions(conn, suggestions)
}

func (d *daemon) handleReset(conn net.Conn) {
	d.flushMu.Lock()
	newG := graph.New(2)
	d.g.Store(newG)
	d.pred.Store(predict.New(newG, d.opts.HalfLife(), d.opts.Alpha))
	if err := d.st.clear(); err != nil {
		d.log.Error("clear store", "error", err)
	}
	d.flushMu.Unlock()

	writeOK(conn)
}

func (d *daemon) handleExport(conn net.Conn) {
	all := d.g.Load().All()
	writeTransitions(conn, all)
}

// flushLoop periodically flushes the in-memory graph to SQLite.
func (d *daemon) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.flushCh:
			d.flush()
		case <-ticker.C:
			if d.dirty.Load() > 0 {
				d.flush()
			}
		}
	}
}

func (d *daemon) flush() {
	d.flushMu.Lock()
	defer d.flushMu.Unlock()

	all := d.g.Load().All()
	if len(all) == 0 {
		return
	}

	if err := d.st.save(all); err != nil {
		d.log.Error("flush failed", "error", err)
		return
	}
	d.log.Debug("flushed", "count", len(all))
}

func (d *daemon) importSeed(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read seed: %w", err)
	}

	var seed graph.Seed
	if err := json.Unmarshal(data, &seed); err != nil {
		return fmt.Errorf("unmarshal seed: %w", err)
	}

	if err := d.g.Load().Merge(seed.Transitions); err != nil {
		return fmt.Errorf("merge seed: %w", err)
	}

	d.log.Info("seed imported", "source", seed.Source, "transitions", len(seed.Transitions))
	return nil
}

func filterByPrefix(suggestions []types.Suggestion, prefix string) []types.Suggestion {
	var filtered []types.Suggestion
	for _, s := range suggestions {
		if strings.HasPrefix(s.Template, prefix) {
			filtered = append(filtered, s)
		}
	}
	if filtered == nil {
		return []types.Suggestion{}
	}
	return filtered
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}


