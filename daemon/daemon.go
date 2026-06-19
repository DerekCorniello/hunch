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
	"github.com/DerekCorniello/hunch/core/types"
	"github.com/DerekCorniello/hunch/ipc"
)

const (
	flushThreshold = 50
	flushInterval  = 5 * time.Second
)

type daemon struct {
	opts     Options
	g        atomic.Pointer[graph.Graph]
	pred     atomic.Pointer[predict.Predictor]
	st       *store
	log      *slog.Logger
	parents  []string // cached result of MergeParents

	rawMap   map[string]map[string]int // template → raw → count
	rawMu    sync.RWMutex

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
		parents: normalize.MergeParents(opts.ExtraParents),
		rawMap:  make(map[string]map[string]int),
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

	rawExamples, err := st.loadRawExamples()
	if err != nil {
		return fmt.Errorf("load raw examples: %w", err)
	}
	d.rawMap = rawExamples

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

	// Start IPC listener — clean stale socket first.
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

func (d *daemon) handleConn(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			d.log.Error("panic handling connection", "recover", r)
		}
		conn.Close()
	}()

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	req, err := parseRequest(conn)
	if err != nil {
		_ = writeError(conn, "bad request")
		return
	}

	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	switch req.Op {
	case "record":
		d.log.Debug("record", "state", req.State, "next", req.Next)
		d.handleRecord(conn, req)
	case "predict":
		d.log.Debug("predict", "state", req.State, "prefix", req.Prefix)
		d.handlePredict(conn, req)
	case "reset":
		d.handleReset(conn)
	case "export":
		d.handleExport(conn)
	case "stats":
		d.handleStats(conn)
	case "normalize":
		d.handleNormalize(conn, req)
	case "config":
		d.handleConfig(conn)
	case "import":
		d.handleImport(conn, req)
	case "record_raws":
		d.handleRecordRaws(conn, req)
	default:
		_ = writeError(conn, fmt.Sprintf("unknown op: %s", req.Op))
	}
}

func (d *daemon) handleRecord(conn net.Conn, req ipc.Request) {
	at, err := parseAt(req.At)
	if err != nil {
		_ = writeError(conn, fmt.Sprintf("bad at: %s", err))
		return
	}
	if at.IsZero() {
		at = time.Now()
	}

	// Normalize state and next before recording.
	normalizedState := make([]string, len(req.State))
	for i, raw := range req.State {
		normalizedState[i] = normalize.Normalize(raw, d.parents)
	}
	normalizedNext := normalize.Normalize(req.Next, d.parents)

	d.g.Load().Record(normalizedState, normalizedNext, at)

	d.rawMu.Lock()
	inner, ok := d.rawMap[normalizedNext]
	if !ok {
		inner = make(map[string]int)
		d.rawMap[normalizedNext] = inner
	}
	inner[req.Next]++
	d.rawMu.Unlock()

	d.flushMu.Lock()
	d.dirty.Add(1)
	d.flushMu.Unlock()

	if d.dirty.Load() >= flushThreshold {
		select {
		case d.flushCh <- struct{}{}:
		default:
		}
	}

	_ = writeOK(conn)
}

func (d *daemon) handleRecordRaws(conn net.Conn, req ipc.Request) {
	var examples []struct {
		Template string `json:"template"`
		Raw      string `json:"raw"`
		Count    int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(req.Next), &examples); err != nil {
		_ = writeError(conn, fmt.Sprintf("bad examples: %v", err))
		return
	}

	d.rawMu.Lock()
	for _, ex := range examples {
		inner, ok := d.rawMap[ex.Template]
		if !ok {
			inner = make(map[string]int)
			d.rawMap[ex.Template] = inner
		}
		inner[ex.Raw] += ex.Count
	}
	d.rawMu.Unlock()

	_ = writeOK(conn)
}

func (d *daemon) handlePredict(conn net.Conn, req ipc.Request) {
	at, err := parseAt(req.At)
	if err != nil {
		_ = writeError(conn, fmt.Sprintf("bad at: %s", err))
		return
	}
	if at.IsZero() {
		at = time.Now()
	}

	prev := make([]types.Command, len(req.State))
	for i, raw := range req.State {
		prev[i] = types.Command{Template: normalize.Normalize(raw, d.parents)}
	}

	st := types.State{Previous: prev}
	// Fetch all suggestions (limit=0 means no limit), then filter by
	// prefix server-side, then cap to the requested limit.
	suggestions := d.pred.Load().Predict(st, at, 0)

	if req.Prefix != "" {
		suggestions = filterByPrefix(suggestions, req.Prefix)
	}

	limit := req.Limit
	if limit < 0 {
		limit = 0
	}
	if limit > 0 && len(suggestions) > limit {
		suggestions = suggestions[:limit]
	}

	// Fill in raw commands from the raw mapping.
	d.rawMu.RLock()
	for i, s := range suggestions {
		bestRaw := ""
		bestCount := 0
		if inner, ok := d.rawMap[s.Template]; ok {
			for raw, count := range inner {
				if count > bestCount {
					bestCount = count
					bestRaw = raw
				}
			}
		}
		suggestions[i].Raw = bestRaw
	}
	d.rawMu.RUnlock()

	_ = writeSuggestions(conn, suggestions)
}

func (d *daemon) handleReset(conn net.Conn) {
	d.flushMu.Lock()
	newG := graph.New(2)
	d.g.Store(newG)
	d.pred.Store(predict.New(newG, d.opts.HalfLife(), d.opts.Alpha))
	if err := d.st.clear(); err != nil {
		d.log.Error("clear store", "error", err)
	}
	d.rawMu.Lock()
	d.rawMap = make(map[string]map[string]int)
	d.rawMu.Unlock()
	d.dirty.Store(0)
	d.flushMu.Unlock()

	writeOK(conn)
}

// flushDB persists the current graph and raw examples to the database.
// flushMu must be held by the caller. Returns true on success.
func (d *daemon) flushDB() bool {
	all := d.g.Load().All()
	if len(all) == 0 {
		return true
	}
	if err := d.st.save(all); err != nil {
		d.log.Error("flush failed", "error", err)
		return false
	}

	d.rawMu.RLock()
	rawMap := d.rawMap
	d.rawMu.RUnlock()
	if len(rawMap) > 0 {
		if err := d.st.saveRawExamples(rawMap); err != nil {
			d.log.Error("flush raw examples failed", "error", err)
		}
	}

	return true
}

func (d *daemon) handleExport(conn net.Conn) {
	all := d.g.Load().All()
	writeTransitions(conn, all)
}

func (d *daemon) handleStats(conn net.Conn) {
	size := d.g.Load().Size()
	resp := ipc.StatsResponse{
		Size:     size,
		HalfLife: d.opts.HalfLife().String(),
		Alpha:    d.opts.Alpha,
		DBPath:   d.opts.DBPath,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		_ = writeError(conn, "marshal stats")
		return
	}
	fmt.Fprint(conn, string(data)+"\n")
}

func (d *daemon) handleNormalize(conn net.Conn, req ipc.Request) {
	raw := req.Next
	if raw == "" && len(req.State) > 0 {
		raw = req.State[len(req.State)-1]
	}
	if raw == "" {
		_ = writeError(conn, "no input to normalize")
		return
	}
	template := normalize.Normalize(raw, d.parents)
	resp := ipc.NormalizeResponse{
		Raw:      raw,
		Template: template,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		_ = writeError(conn, "marshal normalize")
		return
	}
	fmt.Fprint(conn, string(data)+"\n")
}

func (d *daemon) handleConfig(conn net.Conn) {
	resp := ipc.ConfigResponse{
		AcceptKeys:   d.opts.AcceptKeys,
		ExtraParents: d.opts.ExtraParents,
		HalfLife:     d.opts.HalfLife().String(),
		Alpha:        d.opts.Alpha,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		_ = writeError(conn, "marshal config")
		return
	}
	fmt.Fprint(conn, string(data)+"\n")
}

func (d *daemon) handleImport(conn net.Conn, req ipc.Request) {
	if req.Next == "" {
		_ = writeError(conn, "import path required")
		return
	}
	// Only allow regular files (no device files, FIFOs, etc.). No path
	// traversal either — seed files should be under the data directory.
	p, err := filepath.Abs(req.Next)
	if err != nil {
		_ = writeError(conn, fmt.Sprintf("bad path: %v", err))
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			_ = writeError(conn, "file not found")
		} else {
			_ = writeError(conn, fmt.Sprintf("stat: %v", err))
		}
		return
	}
	if !fi.Mode().IsRegular() {
		_ = writeError(conn, "not a regular file")
		return
	}
	if err := d.importSeed(p); err != nil {
		_ = writeError(conn, fmt.Sprintf("import failed: %v", err))
		return
	}
	d.pred.Store(predict.New(d.g.Load(), d.opts.HalfLife(), d.opts.Alpha))
	writeOK(conn)
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

	// Persist imported transitions immediately so nothing is lost on crash.
	d.flush()

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
