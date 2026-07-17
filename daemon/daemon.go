package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
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
	"github.com/DerekCorniello/hunch/ipc"
)

const (
	flushThreshold = 50
	flushInterval  = 5 * time.Second
	decayInterval  = 24 * time.Hour
)

// rawEntry tracks the accumulated count and most recent observation time
// for one (stateKey, template, raw) triple.
type rawEntry struct {
	count    int
	lastSeen time.Time
}

type daemon struct {
	opts     Options
	g        atomic.Pointer[graph.Graph]
	pred     atomic.Pointer[predict.Predictor]
	st       *store
	log      *slog.Logger
	parents  []string        // cached result of MergeParents
	redactor *redact.Matcher // drops sensitive commands before recording

	// rawMap: outerKey → raw → rawEntry
	// outerKey = rawOuterKey(stateTemplates, template)
	rawMap map[string]map[string]rawEntry
	rawMu  sync.RWMutex

	lock     Locker
	listener net.Listener

	flushMu sync.Mutex

	dirty   atomic.Int32
	flushCh chan struct{}

	sockPath string
	lockPath string
	pidPath  string
}

// rawOuterKey builds the map key for rawMap from a prior-command state slice
// and the next-command template. Empty strings in state are ignored so that
// `["", "git add PATH"]` and `["git add PATH"]` produce the same key.
func rawOuterKey(state []string, template string) string {
	var nonEmpty []string
	for _, s := range state {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	return strings.Join(nonEmpty, "\x00") + "\x00\x00" + template
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
		rawMap:   make(map[string]map[string]rawEntry),
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
	for _, rec := range rawRecords {
		outerKey := rawOuterKey(rec.State, rec.Template)
		inner, ok := d.rawMap[outerKey]
		if !ok {
			inner = make(map[string]rawEntry)
			d.rawMap[outerKey] = inner
		}
		inner[rec.Raw] = rawEntry{count: rec.Count, lastSeen: rec.LastSeen}
	}

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

	// Drop commands that look like they carry secrets: neither the
	// transition nor the raw example is recorded, so nothing sensitive is
	// ever persisted or suggested back. Reported as OK so the caller is
	// unaware which commands were skipped.
	if d.redactor.IsSensitive(req.Next) {
		d.log.Debug("skipping sensitive command")
		_ = writeOK(conn)
		return
	}

	// Normalize state and next before recording.
	normalizedState := make([]string, 0, len(req.State)+1)
	if req.CWD != "" {
		normalizedState = append(normalizedState, req.CWD)
	}
	for _, raw := range req.State {
		normalizedState = append(normalizedState, normalize.Normalize(raw, d.parents))
	}
	normalizedNext := normalize.Normalize(req.Next, d.parents)

	// Acceptance: the executed command confirms a suggestion when its
	// template matches the template hunch last showed for this state, even
	// if the user edited a value (normalization collapses STR/PATH/etc.).
	accepted := req.Suggested != "" && normalize.Normalize(req.Suggested, d.parents) == normalizedNext

	d.g.Load().RecordObs(graph.Observation{
		State:        normalizedState,
		Next:         normalizedNext,
		At:           at,
		CWD:          req.CWD,
		NextOutcome:  graph.Outcome(req.Outcome),
		PriorOutcome: graph.Outcome(req.PriorOutcome),
		Accepted:     accepted,
	})

	// Store raw example conditioned on the normalized state, so lookup
	// during prediction can prefer raws seen in the same workflow context.
	d.rawMu.Lock()
	outerKey := rawOuterKey(normalizedState, normalizedNext)
	inner, ok := d.rawMap[outerKey]
	if !ok {
		inner = make(map[string]rawEntry)
		d.rawMap[outerKey] = inner
	}
	prev := inner[req.Next]
	prev.count++
	prev.lastSeen = at
	inner[req.Next] = prev
	d.rawMu.Unlock()

	if d.dirty.Add(1) >= flushThreshold {
		select {
		case d.flushCh <- struct{}{}:
		default:
		}
	}

	_ = writeOK(conn)
}

func (d *daemon) handleRecordRaws(conn net.Conn, req ipc.Request) {
	now := time.Now()
	d.rawMu.Lock()
	for _, ex := range req.RawExamples {
		if ex.Template == "" || ex.Raw == "" {
			continue
		}
		var lastSeen time.Time
		if ex.LastSeen > 0 {
			lastSeen = time.Unix(ex.LastSeen, 0)
		} else {
			lastSeen = now
		}
		outerKey := rawOuterKey(ex.State, ex.Template)
		inner, ok := d.rawMap[outerKey]
		if !ok {
			inner = make(map[string]rawEntry)
			d.rawMap[outerKey] = inner
		}
		prev := inner[ex.Raw]
		prev.count += ex.Count
		if lastSeen.After(prev.lastSeen) {
			prev.lastSeen = lastSeen
		}
		inner[ex.Raw] = prev
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

	prior := types.Outcome(req.PriorOutcome)

	// Level 1: CWD-augmented state key — transitions learned in this
	// specific directory from live sessions.
	st := types.State{
		Previous:     prev,
		CWD:          req.CWD,
		PriorOutcome: prior,
	}
	suggestions := d.pred.Load().Predict(st, at, 0)

	// Level 2: walk up parent directories so that transitions learned
	// in ~/project still apply when the shell is in ~/project/src.
	if req.Prefix == "" && len(suggestions) == 0 && req.CWD != "" {
		for parent := filepath.Dir(req.CWD); parent != "/" && parent != req.CWD; parent = filepath.Dir(parent) {
			stParent := types.State{
				Previous:     prev,
				CWD:          parent,
				PriorOutcome: prior,
			}
			suggestions = d.pred.Load().Predict(stParent, at, 0)
			if len(suggestions) > 0 {
				break
			}
		}
	}

	if req.Prefix == "" && len(suggestions) == 0 {
		// Level 3: fall back to no-CWD state key — matches imported
		// shell history and any data recorded before CWD tracking.
		stNoCWD := types.State{
			Previous:     prev,
			CWD:          "",
			PriorOutcome: prior,
		}
		suggestions = d.pred.Load().Predict(stNoCWD, at, 0)
	}
	if req.Prefix == "" && len(suggestions) == 0 {
		// Level 4: progressively shorter history windows.
		for trim := 1; trim <= len(prev) && len(suggestions) == 0; trim++ {
			fallback := types.State{
				Previous:     prev[trim:],
				CWD:          "",
				PriorOutcome: prior,
			}
			suggestions = d.pred.Load().Predict(fallback, at, 0)
		}
	}

	if req.Prefix != "" {
		suggestions = filterByPrefix(suggestions, req.Prefix, d.parents)
	}

	// Suppress "cd" suggestions that target the current directory.
	suggestions = suppressCdToCurrent(suggestions, req.CWD)

	limit := req.Limit
	if limit < 0 {
		limit = 0
	}
	if limit > 0 && len(suggestions) > limit {
		suggestions = suggestions[:limit]
	}

	// Extract variable-value argument tokens from the recent raw prior
	// commands. These are used below to boost raw suggestions that reuse
	// the same file names, script names, etc. from the immediately preceding
	// context (option 2).
	argTokens := collectArgTokens(req.State, d.parents)

	// Build the clean (non-empty) state template list for conditioned lookup.
	stateTemplates := make([]string, 0, len(prev))
	for _, cmd := range prev {
		if cmd.Template != "" {
			stateTemplates = append(stateTemplates, cmd.Template)
		}
	}

	// Hydrate each suggestion with the best matching raw command.
	// Lookup is state-conditioned (option 1), scored by recency (option 3),
	// and boosted by argument token overlap with recent commands (option 2).
	d.rawMu.RLock()
	for i, s := range suggestions {
		if raw := d.findBestRaw(stateTemplates, s.Template, req.Prefix, argTokens, at); raw != "" {
			suggestions[i].Raw = raw
		}
	}
	d.rawMu.RUnlock()

	_ = writeSuggestions(conn, suggestions)
}

// collectArgTokens extracts unquoted variable-value tokens (STR, PATH, HASH,
// NUM, REPO) from the most recent raw prior commands. These represent file
// names, script names, etc. that the user may want to reuse in the next command.
// Tokens shorter than 3 characters are skipped to avoid spurious matches.
func collectArgTokens(rawCmds []string, parents []string) []string {
	if len(rawCmds) == 0 {
		return nil
	}
	start := len(rawCmds) - 2
	if start < 0 {
		start = 0
	}
	var tokens []string
	seen := make(map[string]struct{})
	for _, raw := range rawCmds[start:] {
		for _, tok := range normalize.ExtractArgTokens(raw, parents) {
			if len(tok) < 3 {
				continue
			}
			if _, ok := seen[tok]; !ok {
				seen[tok] = struct{}{}
				tokens = append(tokens, tok)
			}
		}
	}
	return tokens
}

// findBestRaw returns the highest-scored raw command for the given template,
// trying progressively shorter state windows until a match is found.
// Must be called with rawMu held for reading.
func (d *daemon) findBestRaw(stateTemplates []string, template, prefix string, argTokens []string, at time.Time) string {
	for trim := 0; trim <= len(stateTemplates); trim++ {
		outerKey := rawOuterKey(stateTemplates[trim:], template)
		inner, ok := d.rawMap[outerKey]
		if !ok || len(inner) == 0 {
			continue
		}
		if raw := d.selectBestRaw(inner, prefix, argTokens, at); raw != "" {
			return raw
		}
	}
	return ""
}

// selectBestRaw picks the highest-scored raw from a bucket. When prefix is
// non-empty, it prefers raws that literally start with it; if none match the
// prefix, it falls back to the overall best raw. Must be called with rawMu
// held for reading.
func (d *daemon) selectBestRaw(inner map[string]rawEntry, prefix string, argTokens []string, at time.Time) string {
	bestRaw, bestScore := "", -1.0
	bestPrefixRaw, bestPrefixScore := "", -1.0

	for raw, entry := range inner {
		score := d.scoreRaw(entry, raw, argTokens, at)
		if score > bestScore {
			bestScore = score
			bestRaw = raw
		}
		if prefix != "" && strings.HasPrefix(raw, prefix) && score > bestPrefixScore {
			bestPrefixScore = score
			bestPrefixRaw = raw
		}
	}

	if prefix != "" && bestPrefixRaw != "" {
		return bestPrefixRaw
	}
	return bestRaw
}

// scoreRaw computes a score for a raw command candidate. The score combines
// observation count with an exponential recency decay (matching the graph's
// half-life) and an additive boost when the raw contains a token that appeared
// as an argument in a recent prior command.
func (d *daemon) scoreRaw(entry rawEntry, raw string, argTokens []string, at time.Time) float64 {
	halfLife := d.opts.HalfLife()
	var recency float64
	if entry.lastSeen.IsZero() {
		recency = 0.1 // small floor for migrated entries without timestamps
	} else {
		elapsed := at.Sub(entry.lastSeen)
		recency = math.Exp(-math.Ln2 * float64(elapsed) / float64(halfLife))
	}
	score := float64(entry.count) * recency

	// Token overlap boost: each argument token from a recent prior command
	// that appears literally in this raw adds a fixed bonus large enough to
	// override moderate frequency differences, reflecting the high likelihood
	// the user wants to reuse the same file/script name.
	const tokenBoost = 100.0
	for _, tok := range argTokens {
		if strings.Contains(raw, tok) {
			score += tokenBoost
		}
	}
	return score
}

func (d *daemon) handleReset(conn net.Conn) {
	d.flushMu.Lock()
	newG := graph.New(2)
	d.g.Store(newG)
	d.pred.Store(predict.New(newG, d.opts.HalfLife(), d.opts.Alpha, d.opts.Beta, d.opts.Gamma, d.opts.Delta, d.opts.Epsilon))
	if err := d.st.clear(); err != nil {
		d.log.Error("clear store", "error", err)
	}
	d.rawMu.Lock()
	d.rawMap = make(map[string]map[string]rawEntry)
	d.rawMu.Unlock()
	d.dirty.Store(0)
	d.flushMu.Unlock()

	_ = writeOK(conn)
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

	rawSnapshot := d.snapshotRawMap()
	if len(rawSnapshot) > 0 {
		if err := d.st.saveRawExamples(rawSnapshot); err != nil {
			d.log.Error("flush raw examples failed", "error", err)
		}
	}

	return true
}

// snapshotRawMap returns a flat slice of rawRecords taken under the read
// lock, safe to iterate without further synchronization.
func (d *daemon) snapshotRawMap() []rawRecord {
	d.rawMu.RLock()
	defer d.rawMu.RUnlock()

	var records []rawRecord
	for outerKey, inner := range d.rawMap {
		// Decompose outerKey into stateKey and template. The separator
		// "\x00\x00" is safe because normalized templates only contain
		// alphanumeric tokens and spaces.
		parts := strings.SplitN(outerKey, "\x00\x00", 2)
		if len(parts) != 2 {
			continue
		}
		stateKey, template := parts[0], parts[1]
		var state []string
		if stateKey != "" {
			state = strings.Split(stateKey, "\x00")
		}
		for raw, entry := range inner {
			records = append(records, rawRecord{
				State:    state,
				Template: template,
				Raw:      raw,
				Count:    entry.count,
				LastSeen: entry.lastSeen,
			})
		}
	}
	return records
}

func (d *daemon) handleExport(conn net.Conn) {
	all := d.g.Load().All()
	_ = writeTransitions(conn, all)
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
	p, err := filepath.Abs(req.Next)
	if err != nil {
		_ = writeError(conn, fmt.Sprintf("bad path: %v", err))
		return
	}
	// Use Lstat (not Stat) so we can detect symlinks before following them.
	fi, err := os.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			_ = writeError(conn, "file not found")
		} else {
			_ = writeError(conn, fmt.Sprintf("stat: %v", err))
		}
		return
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		_ = writeError(conn, "symlinks not allowed")
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
	d.pred.Store(predict.New(d.g.Load(), d.opts.HalfLife(), d.opts.Alpha, d.opts.Beta, d.opts.Gamma, d.opts.Delta, d.opts.Epsilon))
	_ = writeOK(conn)
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

	if len(res.Orphaned) > 0 {
		// Build a set for O(1) lookup during the rawMap scan.
		orphanedSet := make(map[string]struct{}, len(res.Orphaned))
		for _, tmpl := range res.Orphaned {
			orphanedSet[tmpl] = struct{}{}
		}
		d.rawMu.Lock()
		for outerKey := range d.rawMap {
			parts := strings.SplitN(outerKey, "\x00\x00", 2)
			if len(parts) == 2 {
				if _, orphaned := orphanedSet[parts[1]]; orphaned {
					delete(d.rawMap, outerKey)
				}
			}
		}
		d.rawMu.Unlock()
	}

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
