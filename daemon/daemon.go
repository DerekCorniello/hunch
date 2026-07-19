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
	"github.com/DerekCorniello/hunch/core/normalize"
	"github.com/DerekCorniello/hunch/core/predict"
	"github.com/DerekCorniello/hunch/core/redact"
	"github.com/DerekCorniello/hunch/core/types"
)

const (
	flushThreshold = 50
	flushInterval  = 5 * time.Second
	decayInterval  = 24 * time.Hour
)

type daemon struct {
	opts     Options
	g        atomic.Pointer[graph.Graph]
	pred     atomic.Pointer[predict.Predictor]
	st       *store
	log      *slog.Logger
	parents  []string        // cached result of MergeParents
	redactor *redact.Matcher // drops sensitive commands before recording

	raws *rawStore

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
		opts:     opts,
		log:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(opts.LogLevel)})),
		flushCh:  make(chan struct{}, 1),
		parents:  normalize.MergeParents(opts.ExtraParents),
		redactor: redact.New(opts.Ignore),
		raws:     newRawStore(opts.HalfLife()),
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
	// Ensure data and socket directories exist.
	if err := os.MkdirAll(filepath.Dir(d.opts.DBPath), 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(d.sockPath), 0755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
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
	if err := os.WriteFile(d.pidPath, []byte(strconv.Itoa(pid)), 0600); err != nil {
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

	rawRecords, err := st.loadRawExamples()
	if err != nil {
		return fmt.Errorf("load raw examples: %w", err)
	}
	d.raws.load(rawRecords)

	// Seed import on first run.
	if d.opts.SeedPath != "" && len(transitions) == 0 {
		if err := d.importSeed(d.opts.SeedPath); err != nil {
			d.log.Warn("seed import failed", "path", d.opts.SeedPath, "error", err)
		}
	}

	// Build predictor.
	d.pred.Store(predict.New(d.g.Load(), d.opts.HalfLife(), d.opts.Alpha, d.opts.Beta, d.opts.Gamma, d.opts.Delta, d.opts.Epsilon))

	// Start flush loop.
	go d.flushLoop(ctx)

	// Prune stale transitions on startup, then once a day.
	d.decay()
	go d.decayLoop(ctx)

	// Start IPC listener - clean stale socket first.
	os.Remove(d.sockPath)
	listener, err := net.Listen("unix", d.sockPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if err := os.Chmod(d.sockPath, 0700); err != nil {
		listener.Close()
		return fmt.Errorf("set socket permissions: %w", err)
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
	if err := os.Remove(d.pidPath); err != nil && !os.IsNotExist(err) {
		d.log.Warn("remove pid file", "error", err)
	}

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
	if err := os.Remove(d.lockPath); err != nil {
		d.log.Warn("remove stale lock file", "error", err)
	}
	if err := os.Remove(d.pidPath); err != nil && !os.IsNotExist(err) {
		d.log.Warn("remove stale pid file", "error", err)
	}
	return nil
}

func (d *daemon) acceptLoop(ctx context.Context) {
	backoff := 10 * time.Millisecond
	const maxBackoff = 5 * time.Second

	for {
		conn, err := d.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			d.log.Error("accept error", "error", err)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = 10 * time.Millisecond
		go d.handleConn(conn)
	}
}

// flushDB persists the current graph and raw examples to the database.
// flushMu must be held by the caller. Returns true on success.
func (d *daemon) flushDB() bool {
	if all := d.g.Load().All(); len(all) > 0 {
		if err := d.st.save(all); err != nil {
			d.log.Error("flush failed", "error", err)
			return false
		}
	}

	rawSnapshot := d.raws.snapshot()
	if len(rawSnapshot) > 0 {
		if err := d.st.saveRawExamples(rawSnapshot); err != nil {
			d.log.Error("flush raw examples failed", "error", err)
		}
	}

	return true
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

	ok := d.flushDB()

	if ok {
		// Only clear dirty if the save succeeded. Records that arrived
		// during the save (incrementing dirty) are in the graph but not
		// yet persisted. If dirty was > 0 at swap time, schedule a re-flush.
		if d.dirty.Swap(0) > 0 {
			select {
			case d.flushCh <- struct{}{}:
			default:
			}
		}
	}
	d.log.Debug("flushed")
}

// decay prunes stale transitions from the graph and the database. It holds
// flushMu so it never interleaves with a flush, since both write the DB.
func (d *daemon) decay() {
	d.flushMu.Lock()
	defer d.flushMu.Unlock()

	res := d.g.Load().Decay(time.Now(), d.opts.HalfLife())
	if len(res.Pruned) == 0 && len(res.Orphaned) == 0 {
		return
	}

	d.raws.dropOrphaned(res.Orphaned)

	if err := d.st.prune(res.Pruned, res.Orphaned); err != nil {
		d.log.Error("decay prune failed", "error", err)
		return
	}
	d.log.Debug("decayed", "pruned", len(res.Pruned), "orphaned", len(res.Orphaned))
}

// decayLoop runs decay once per day until ctx is cancelled.
func (d *daemon) decayLoop(ctx context.Context) {
	ticker := time.NewTicker(decayInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.decay()
		}
	}
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

	if err := seed.Validate(); err != nil {
		return fmt.Errorf("invalid seed: %w", err)
	}

	if err := d.g.Load().Merge(seed.Transitions); err != nil {
		return fmt.Errorf("merge seed: %w", err)
	}

	// Persist imported transitions immediately so nothing is lost on crash.
	d.flush()

	d.log.Info("seed imported", "source", seed.Source, "transitions", len(seed.Transitions))
	return nil
}

func filterByPrefix(suggestions []types.Suggestion, prefix string, parents []string) []types.Suggestion {
	normPrefix := normalize.Normalize(prefix, parents)
	var filtered []types.Suggestion
	for _, s := range suggestions {
		if strings.HasPrefix(s.Template, normPrefix) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// suppressCdToCurrent removes "cd PATH" suggestions whose target matches
// the current working directory, so hunch never suggests navigating to a
// directory the shell is already in.
func suppressCdToCurrent(suggestions []types.Suggestion, cwd string) []types.Suggestion {
	if cwd == "" || len(suggestions) == 0 {
		return suggestions
	}
	filtered := make([]types.Suggestion, 0, len(suggestions))
	for _, s := range suggestions {
		if s.Template == "cd PATH" && s.Raw != "" {
			target := strings.TrimSpace(strings.TrimPrefix(s.Raw, "cd "))
			if target == "." || strings.HasSuffix(cwd, "/"+target) || cwd == target {
				continue
			}
		}
		filtered = append(filtered, s)
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
