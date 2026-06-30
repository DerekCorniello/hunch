package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	_ "modernc.org/sqlite"
)

// store wraps the SQLite database.
type store struct {
	db *sql.DB
}

// rawRecord is one row from the raw_examples table.
type rawRecord struct {
	State    []string
	Template string
	Raw      string
	Count    int
	LastSeen time.Time
}

// migrations is the ordered list of schema migrations. Each entry is
// applied exactly once, in order, and SQLite's user_version PRAGMA records
// how many have run. Append new migrations to the end — never edit or
// reorder existing ones, or databases in the field will diverge.
//
// Migration 1 uses IF NOT EXISTS so it adopts databases created before the
// migration runner existed (those report user_version 0 but already have
// the tables).
var migrations = []string{
	// 1: initial schema.
	`
	CREATE TABLE IF NOT EXISTS transitions (
		state     TEXT NOT NULL,
		next      TEXT NOT NULL,
		count     INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		PRIMARY KEY (state, next)
	);
	CREATE INDEX IF NOT EXISTS idx_state ON transitions(state);
	CREATE TABLE IF NOT EXISTS raw_examples (
		template TEXT NOT NULL,
		raw      TEXT NOT NULL,
		count    INTEGER NOT NULL,
		PRIMARY KEY (template, raw)
	);
	`,
	// 2: per-transition outcome counters and a CWD histogram, for the
	// location-affinity and outcome-weighting signals.
	`
	ALTER TABLE transitions ADD COLUMN next_success  INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE transitions ADD COLUMN next_failure  INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE transitions ADD COLUMN prior_success INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE transitions ADD COLUMN prior_failure INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE IF NOT EXISTS transition_cwd (
		state TEXT NOT NULL,
		next  TEXT NOT NULL,
		cwd   TEXT NOT NULL,
		count INTEGER NOT NULL,
		PRIMARY KEY (state, next, cwd)
	);
	`,
	// 3: per-transition acceptance counter for the confirmed-suggestion boost.
	`ALTER TABLE transitions ADD COLUMN accepted INTEGER NOT NULL DEFAULT 0;`,
	// 4: add state-conditioning and recency to raw_examples. Recreate the
	// table with a new primary key (state, template, raw) so raw suggestions
	// can be looked up conditioned on the prior-command context that produced
	// them, and scored by recency. Existing rows migrate with state=[] and
	// last_seen=now so they retain full weight immediately.
	`
	CREATE TABLE raw_examples_new (
		state     TEXT    NOT NULL DEFAULT '[]',
		template  TEXT    NOT NULL,
		raw       TEXT    NOT NULL,
		count     INTEGER NOT NULL,
		last_seen INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (state, template, raw)
	);
	CREATE INDEX IF NOT EXISTS idx_raw_examples_template
		ON raw_examples_new(template);
	INSERT INTO raw_examples_new (state, template, raw, count, last_seen)
		SELECT '[]', template, raw, count, strftime('%s', 'now')
		FROM raw_examples;
	DROP TABLE raw_examples;
	ALTER TABLE raw_examples_new RENAME TO raw_examples;
	`,
}

// open opens the SQLite database at path, applies any pending schema
// migrations, and returns the store. Returns an error if the database
// cannot be opened or a migration fails.
func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("set synchronous: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &store{db: db}, nil
}

// migrate applies every migration whose index exceeds the database's current
// user_version, advancing user_version as it goes. Each migration runs in its
// own transaction so a failure leaves the schema at the last good version.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", i+1, err)
		}
		// PRAGMA user_version does not accept bind parameters; i+1 is a
		// trusted loop index, not user input, so interpolation is safe.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set schema version %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}
	}
	return nil
}

// load reads every transition from the database, including its outcome
// counters and CWD histogram.
func (s *store) load() ([]graph.Transition, error) {
	rows, err := s.db.Query(`
		SELECT state, next, count, last_seen,
		       next_success, next_failure, prior_success, prior_failure, accepted
		FROM transitions
	`)
	if err != nil {
		return nil, fmt.Errorf("query transitions: %w", err)
	}
	defer rows.Close()

	var result []graph.Transition
	// index maps "stateJSON\x00next" to the transition's slot in result, so
	// the CWD histogram rows can be attached in a second pass.
	index := make(map[string]int)
	for rows.Next() {
		var stateJSON string
		var t graph.Transition
		var lastSeenUnix int64

		if err := rows.Scan(&stateJSON, &t.Next, &t.Count, &lastSeenUnix,
			&t.NextSuccess, &t.NextFailure, &t.PriorSuccess, &t.PriorFailure, &t.Accepted); err != nil {
			return nil, fmt.Errorf("scan transition: %w", err)
		}
		if err := json.Unmarshal([]byte(stateJSON), &t.State); err != nil {
			return nil, fmt.Errorf("unmarshal state: %w", err)
		}
		t.LastSeen = time.Unix(lastSeenUnix, 0)
		index[stateJSON+"\x00"+t.Next] = len(result)
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transitions: %w", err)
	}

	if err := s.attachCWDs(result, index); err != nil {
		return nil, err
	}
	return result, nil
}

// attachCWDs reads the transition_cwd histogram and attaches each row to its
// transition in result, located via index (keyed by "stateJSON\x00next").
func (s *store) attachCWDs(result []graph.Transition, index map[string]int) error {
	rows, err := s.db.Query(`SELECT state, next, cwd, count FROM transition_cwd`)
	if err != nil {
		return fmt.Errorf("query transition_cwd: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var stateJSON, next, cwd string
		var count int
		if err := rows.Scan(&stateJSON, &next, &cwd, &count); err != nil {
			return fmt.Errorf("scan transition_cwd: %w", err)
		}
		i, ok := index[stateJSON+"\x00"+next]
		if !ok {
			continue // orphaned histogram row; ignored (pruned on next save)
		}
		if result[i].CWDs == nil {
			result[i].CWDs = make(map[string]int)
		}
		result[i].CWDs[cwd] = count
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate transition_cwd: %w", err)
	}
	return nil
}

// save upserts a batch of transitions in a single transaction.
func (s *store) save(transitions []graph.Transition) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO transitions (state, next, count, last_seen,
			next_success, next_failure, prior_success, prior_failure, accepted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(state, next) DO UPDATE SET
			count = excluded.count,
			last_seen = excluded.last_seen,
			next_success = excluded.next_success,
			next_failure = excluded.next_failure,
			prior_success = excluded.prior_success,
			prior_failure = excluded.prior_failure,
			accepted = excluded.accepted
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	cwdStmt, err := tx.Prepare(`
		INSERT INTO transition_cwd (state, next, cwd, count)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(state, next, cwd) DO UPDATE SET count = excluded.count
	`)
	if err != nil {
		return fmt.Errorf("prepare cwd upsert: %w", err)
	}
	defer cwdStmt.Close()

	for _, t := range transitions {
		stateJSON, err := json.Marshal(t.State)
		if err != nil {
			return fmt.Errorf("marshal state: %w", err)
		}
		if _, err := stmt.Exec(string(stateJSON), t.Next, t.Count, t.LastSeen.Unix(),
			t.NextSuccess, t.NextFailure, t.PriorSuccess, t.PriorFailure, t.Accepted); err != nil {
			return fmt.Errorf("exec upsert: %w", err)
		}
		for cwd, count := range t.CWDs {
			if _, err := cwdStmt.Exec(string(stateJSON), t.Next, cwd, count); err != nil {
				return fmt.Errorf("exec cwd upsert: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// prune deletes decayed transitions and orphaned raw-example templates in a
// single transaction. Both slices may be empty, in which case it is a no-op.
func (s *store) prune(transitions []graph.Transition, orphanedTemplates []string) error {
	if len(transitions) == 0 && len(orphanedTemplates) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if len(transitions) > 0 {
		stmt, err := tx.Prepare(`DELETE FROM transitions WHERE state = ? AND next = ?`)
		if err != nil {
			return fmt.Errorf("prepare delete transition: %w", err)
		}
		defer stmt.Close()
		cwdStmt, err := tx.Prepare(`DELETE FROM transition_cwd WHERE state = ? AND next = ?`)
		if err != nil {
			return fmt.Errorf("prepare delete transition_cwd: %w", err)
		}
		defer cwdStmt.Close()
		for _, t := range transitions {
			stateJSON, err := json.Marshal(t.State)
			if err != nil {
				return fmt.Errorf("marshal state: %w", err)
			}
			if _, err := stmt.Exec(string(stateJSON), t.Next); err != nil {
				return fmt.Errorf("exec delete transition: %w", err)
			}
			if _, err := cwdStmt.Exec(string(stateJSON), t.Next); err != nil {
				return fmt.Errorf("exec delete transition_cwd: %w", err)
			}
		}
	}

	if len(orphanedTemplates) > 0 {
		stmt, err := tx.Prepare(`DELETE FROM raw_examples WHERE template = ?`)
		if err != nil {
			return fmt.Errorf("prepare delete raw_examples: %w", err)
		}
		defer stmt.Close()
		for _, tmpl := range orphanedTemplates {
			if _, err := stmt.Exec(tmpl); err != nil {
				return fmt.Errorf("exec delete raw_examples: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// clear deletes all transitions, CWD histograms, and raw examples.
func (s *store) clear() error {
	if _, err := s.db.Exec(`DELETE FROM transitions`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM transition_cwd`); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM raw_examples`)
	return err
}

// loadRawExamples reads every raw example from the database.
func (s *store) loadRawExamples() ([]rawRecord, error) {
	rows, err := s.db.Query(`SELECT state, template, raw, count, last_seen FROM raw_examples`)
	if err != nil {
		return nil, fmt.Errorf("query raw_examples: %w", err)
	}
	defer rows.Close()

	var records []rawRecord
	for rows.Next() {
		var stateJSON, template, raw string
		var count int
		var lastSeenUnix int64
		if err := rows.Scan(&stateJSON, &template, &raw, &count, &lastSeenUnix); err != nil {
			return nil, fmt.Errorf("scan raw_example: %w", err)
		}
		var state []string
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return nil, fmt.Errorf("unmarshal raw_example state: %w", err)
		}
		records = append(records, rawRecord{
			State:    state,
			Template: template,
			Raw:      raw,
			Count:    count,
			LastSeen: time.Unix(lastSeenUnix, 0),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate raw_examples: %w", err)
	}
	return records, nil
}

// saveRawExamples upserts a batch of raw example records.
func (s *store) saveRawExamples(records []rawRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO raw_examples (state, template, raw, count, last_seen)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(state, template, raw) DO UPDATE SET
			count = excluded.count,
			last_seen = excluded.last_seen
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, rec := range records {
		stateJSON, err := json.Marshal(rec.State)
		if err != nil {
			return fmt.Errorf("marshal state: %w", err)
		}
		var lastSeenUnix int64
		if !rec.LastSeen.IsZero() {
			lastSeenUnix = rec.LastSeen.Unix()
		}
		if _, err := stmt.Exec(string(stateJSON), rec.Template, rec.Raw, rec.Count, lastSeenUnix); err != nil {
			return fmt.Errorf("exec upsert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// close closes the database connection.
func (s *store) close() error {
	return s.db.Close()
}
