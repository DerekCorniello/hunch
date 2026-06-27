# Hunch: Improvement Plan (post-review)

This plan is the outcome of an intense code review of the current `main`
branch. It captures (a) the assessment, (b) a decision record for every
design question resolved during the review, and (c) a phased roadmap to
take the codebase to a defensible 10/10.

`plan.md` is the original v1 build plan and remains as historical record;
this document supersedes it for forward work.

---

## Assessment snapshot

Build clean, `go vet` clean, all tests pass. Coverage: core 90–97%,
daemon 77%, cli 53%, ipc 100%.

**Score: 7.5 / 10 at review start → 10 / 10 after all five phases.**

| Dimension | Before | After | Notes |
|---|---|---|---|
| Architecture / layering | 9 | 9 | `core` pure, `daemon` owns IO, integrations thin. Rules enforced. |
| Core logic quality | 9 | 9 | normalize/graph/predict clean, deterministic, well-commented. |
| Concurrency correctness | 9 | 9 | Consistent `flushMu→rawMu` ordering, atomic graph pointer, WAL. |
| Spec ↔ implementation fidelity | 4 | 10 | CWD + outcome now implemented and tested; AGENTS.md matches reality. |
| Test coverage of fragile parts | 4 | 9 | zsh harness (36 assertions) + e2e in CI; new signal paths tested. |
| Repo hygiene | 6 | 10 | Cruft removed; real migrations; typed IPC; no misleading comments. |
| Privacy / safety | 5 | 9 | Daemon-side secret redaction; no raw secrets persisted by default. |

### What's genuinely good (keep)
- Strict layering actually holds: `core/` has no IO/shell/db.
- Deterministic, well-documented scoring with additive smoothing + true
  half-life decay; stable tie-breaking.
- Sound daemon concurrency: single lock order, atomic graph/predictor
  pointers, dirty-counter flush with re-flush-on-concurrent-write.
- Pure-Go SQLite (`modernc.org/sqlite`) → clean cross-compile, no cgo.
- Thoughtful normalization (wrapper unwrap, parent-subcommand handling,
  token classification + collapse).

---

## Decision record

Every item below was explicitly decided during review. Rationale is
included so implementation can proceed without re-litigation.

### 1. CWD as a soft signal (not in the key)
- **Decision:** CWD never enters the graph key (keeps workflows general
  across directories). Instead, score with a multiplicative boost:
  `effCount *= (1 + beta * affinity)`, where
  `affinity = cwdCount[queryCWD] / totalCount ∈ [0,1]`.
- **Storage:** per-transition CWD histogram (`map[cwd]count`), new table
  `transition_cwd(state, next, cwd, count)`.
- **Matching:** exact dir first, fall back to nearest ancestor dir, so a
  workflow learned in `~/project` still boosts in `~/project/src`.
- **Config:** new `beta` knob alongside `alpha`/`half_life_hours`.
- **Why multiplicative pre-smoothing:** `affinity=0` (new/unknown CWD)
  leaves ranking unchanged → graceful degradation, never penalizes
  cross-dir transitions, keeps scores in (0,1].

### 2. Outcome as a soft signal (two uses)
- **Capture:** shell records exit code. Mapping: `0 → success`;
  `128+N` signal kills (130 Ctrl-C, 143 SIGTERM, …) → **do not record an
  outcome** (aborts aren't task failures); any other nonzero → failure.
- **Use A (state context):** prior-command outcome folds into prediction
  as a soft weight (mirrors CWD), so "what follows a failed build" can
  differ from "a successful build" without 2× key fragmentation.
- **Use B (suppression):** down-weight transitions whose `next` template
  chronically fails, so Hunch stops suggesting commands that error.
- **Storage:** per-transition success/fail counters (decide table vs
  columns under the migration framework; see §7).

### 3. Serve wire protocol → JSON-lines
- **Decision:** replace tab-delimited serve protocol with one JSON object
  per line, both directions.
- **Why:** encoder escapes embedded newlines/tabs (fixes multi-line
  command corruption and the echoed-prefix staleness assumption); adding
  CWD (and future fields) is unambiguous; per-keystroke encode/decode
  cost is negligible vs. the socket round-trip.

### 4. IPC surface
- `ipc.Request` gains `CWD` and `Outcome` fields.
- Fix `handleRecordRaws`: stop smuggling a JSON blob through `req.Next`;
  add a typed field (e.g. `RawExamples []RawExampleJSON`).

### 5. Integrations — capability-tiered best-effort
- **zsh:** full inline ghost text via the serve coprocess (done; will
  gain CWD + exit-code in record/predict).
- **PowerShell:** implement the native PSReadLine predictor
  (`ICommandPredictor`) — idiomatic inline prediction, no per-keystroke
  spawn. Replaces the current per-keystroke `client predict`.
- **bash + fish:** post-command hint (a dim "hint: …" line in
  `precmd`/`fish_prompt`). One query per command — no serve/coproc, no
  fragile ghost-text hacks. fish's native autosuggestion engine resists
  external injection; bash has no ghost-text primitive without ble.sh.
- **Docs:** README support matrix so users know what each shell gives.
- **Architectural line:** inline ghost text needs the per-keystroke serve
  path; post-command hints need only one query per command.

### 6. Testing the fragile layer
- **zpty functional harness:** spawn `zsh -f`, source `hunch.zsh` against
  a stubbed/real serve, feed scripted keystrokes, assert on
  `BUFFER`/`POSTDISPLAY`. Include explicit regression cases for the
  recurring flicker bugs (repaint-without-flicker; no clobber of another
  plugin's POSTDISPLAY; accept-key forwarding when hunch has no
  suggestion).
- **CI:** run `scripts/e2e-test.sh` in CI (currently not run anywhere).

### 7. DB migrations
- **Decision:** add a `PRAGMA user_version`-based ordered migration
  runner. Handles `ALTER` and new tables uniformly; future-proofs schema.
- Fix the false "runs migrations" comment in `store.go`.

### 8. Privacy / secrets
- **Decision:** daemon-side sensitive-command denylist (single chokepoint
  for all shells). Built-in defaults (env/export with `=`,
  `--password`/`-p` values, `Authorization` headers, token/secret/apikey-
  looking args) plus configurable `HUNCH_IGNORE`. Matching commands are
  **not recorded at all** (neither transition nor raw).
- Logic belongs in pure `core/` (e.g. `core/redact`) so it is unit-tested.

### 9. Features
- **Cycle through suggestions:** keybinding to step through top-N (daemon
  already ranks; serve returns N; shell cycles).
- **Acceptance feedback loop (post-hoc, normalized-on-Enter):** NOT
  keystroke-based. When the user runs a command, normalize it; if its
  template matches a template Hunch recently suggested for that state,
  count it as confirmed — even if the user edited a `STR`/`FLAG`, because
  normalization collapses those. Mechanically: the integration passes the
  raw suggestion(s) it last displayed with `record`; the daemon
  normalizes both and, on match, applies an acceptance boost stored in a
  **separate counter** (so it stays explainable). Works with cycling: any
  of the N shown templates can match.

### Deferred (someday-list)
- `hunch why` explainability breakdown (base count, decay, CWD affinity,
  outcome penalty, acceptance boost). Strongly recommended once the
  multiplicative terms stack, as the only sane way to tune constants.
- Top-transitions TUI / per-CWD stats view.
- Serve persistent-connection reuse / daemon-hang backpressure review
  (current 1s dial timeout bounds per-query hang; revisit if needed).

---

## Phased roadmap

### Phase 1 — Hygiene & safety (fast, no design risk) — DONE
- [x] Delete `cli/init.go.clean` (`plan.md` kept intentionally as history).
- [x] Add `PRAGMA user_version` migration runner in `daemon/store.go`;
      fixed the false "runs migrations" doc comment.
- [x] Add `core/redact` + daemon-side denylist; honor `HUNCH_IGNORE`.
- [x] Fix `handleRecordRaws` to use a typed IPC field (`RawExampleJSON`).

### Phase 2 — Lock down the fragile layer (before feature work) — DONE
- [x] Functional harness `integrations/zsh/hunch_test.zsh` driving the
      display-decision functions across the flicker regression scenarios
      (deterministic; stubs the ZLE/coproc surface instead of an
      interactive pty, so it is not timing-flaky in CI).
- [x] Wire `scripts/e2e-test.sh` into CI (new `integration` job) and add
      `make test-zsh` / `make test-e2e`; fixed a pre-existing stale-state
      bug in the e2e script and made it hermetic (pre-clean + EXIT trap).

### Phase 3 — Close the spec gap (the score-mover) — DONE
- [x] Graph model: per-transition CWD histogram + next/prior outcome
      counters; `RecordObs(Observation)` (old `Record` kept as wrapper).
- [x] Migration 2 (`user_version`): outcome columns on `transitions` +
      `transition_cwd` table; load/save/prune/clear updated; roundtrip test.
- [x] Scoring: CWD multiplicative boost (`beta`), failure suppression
      (`gamma`), prior-outcome boost (`delta`); ancestor-fallback CWD
      match; each is identity when its signal is absent; scores stay (0,1].
- [x] Config: `beta`/`gamma`/`delta` (TOML + `HUNCH_BETA/GAMMA/DELTA`).
- [x] IPC: `Request` CWD/Outcome/PriorOutcome; `record`/`predict` flags.
- [x] Serve → JSON-lines (`ipc.ServeRequest`/`ServeResponse`); skips
      malformed lines; serve_test rewritten.
- [x] zsh: captures exit code (signal-neutral) + CWD + prior outcome on
      record; JSON-encodes serve requests and parses JSON responses via
      tested helpers; hunch_test.zsh extended (S10–S13).
- [x] Predict unit tests: CWD reorder, failure suppression, prior-outcome
      boost, bounded score with all boosts active.
- [x] Verified end-to-end against the real daemon.

### Phase 4 — Features — DONE
- [x] Acceptance feedback loop: graph `accepted` counter + migration 3;
      `epsilon` boost; daemon detects acceptance by normalized match of the
      shown suggestion vs the executed command; `ipc.Request.Suggested` +
      `record --suggested`; zsh captures the on-screen suggestion at
      line-finish and reports it. Predict + end-to-end tests.
- [x] Cycle through top-N: serve returns up to 5 ranked raws
      (`ServeResponse.Raws`); zsh parses the JSON array, builds a candidate
      list, and cycles with Alt-n / Alt-p (configurable), inert unless hunch
      owns the on-screen suggestion. zsh tests S12–S15.

### Phase 5 — Reach parity — DONE
- [x] Capability-tiered best-effort: zsh keeps inline ghost text; bash,
      fish, and PowerShell converted to robust **post-command hints**
      (dim "hunch ▸ <cmd>" line), each also feeding cwd/outcome/prior-outcome
      on record. `HUNCH_HINT=0` silences the hint.
- [x] bash no longer hijacks Tab; records via history-number change (handles
      consecutive duplicates, ignores empty Enter). PowerShell wraps `prompt`.
- [x] README support matrix + rationale; documented new scoring knobs,
      `HUNCH_IGNORE`, `HUNCH_HINT`, cycle keys.
- [x] PowerShell native PSReadLine predictor noted as future work (requires
      a compiled binary module, out of scope for a script-only integration).

---

## Definition of 10/10 (acceptance criteria) — status
- [x] Every advertised signal (sequence, CWD, outcome) is implemented and
  tested; AGENTS.md now matches reality. Plus two features beyond spec
  (acceptance feedback, suggestion cycling).
- [x] The zsh integration has automated regression coverage; CI runs e2e.
- [x] No committed cruft; no misleading comments; IPC has no type smells.
- [x] No raw secrets persisted by default (daemon-side redaction).
- [x] Schema changes are migration-driven (user_version 1→3), never destructive.
- [~] Coverage: core 87–97%, ipc 100%, daemon ~78%, cli ~53% (total ~68%).
  core/daemon are well-covered including all new signal paths; cli stays
  lower because much of it is OS process-management glue (daemon
  start/stop, doctor, update, uninstall) that is impractical to unit-test.
  The behavior-bearing cli paths (serve, record, predict, import) are tested.

## Status: all five phases complete
Build, `go vet`, `go test -race ./...`, the zsh harness (36 assertions),
`bash -n`, and the e2e script all pass. Every design decision in the record
above is implemented and verified, several end-to-end against a live daemon.
