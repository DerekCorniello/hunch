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

// open opens the SQLite database at path, creates tables, and runs
// migrations. Returns an error if the database cannot be opened.
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

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS transitions (
			state     TEXT NOT NULL,
			next      TEXT NOT NULL,
			count     INTEGER NOT NULL,
			last_seen INTEGER NOT NULL,
			PRIMARY KEY (state, next)
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create transitions table: %w", err)
	}
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_state ON transitions(state)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create index: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS raw_examples (
			template TEXT NOT NULL,
			raw      TEXT NOT NULL,
			count    INTEGER NOT NULL,
			PRIMARY KEY (template, raw)
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create raw_examples table: %w", err)
	}

	return &store{db: db}, nil
}

// load reads every transition from the database.
func (s *store) load() ([]graph.Transition, error) {
	rows, err := s.db.Query(`SELECT state, next, count, last_seen FROM transitions`)
	if err != nil {
		return nil, fmt.Errorf("query transitions: %w", err)
	}
	defer rows.Close()

	var result []graph.Transition
	for rows.Next() {
		var stateJSON string
		var t graph.Transition
		var lastSeenUnix int64

		if err := rows.Scan(&stateJSON, &t.Next, &t.Count, &lastSeenUnix); err != nil {
			return nil, fmt.Errorf("scan transition: %w", err)
		}
		if err := json.Unmarshal([]byte(stateJSON), &t.State); err != nil {
			return nil, fmt.Errorf("unmarshal state: %w", err)
		}
		t.LastSeen = time.Unix(lastSeenUnix, 0)
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transitions: %w", err)
	}

	return result, nil
}

// save upserts a batch of transitions in a single transaction.
func (s *store) save(transitions []graph.Transition) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO transitions (state, next, count, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(state, next) DO UPDATE SET
			count = excluded.count,
			last_seen = excluded.last_seen
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, t := range transitions {
		stateJSON, err := json.Marshal(t.State)
		if err != nil {
			return fmt.Errorf("marshal state: %w", err)
		}
		if _, err := stmt.Exec(string(stateJSON), t.Next, t.Count, t.LastSeen.Unix()); err != nil {
			return fmt.Errorf("exec upsert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// clear deletes all transitions and raw examples from the database.
func (s *store) clear() error {
	_, err := s.db.Exec(`DELETE FROM transitions`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM raw_examples`)
	return err
}

// loadRawExamples reads every template→raw mapping from the database.
func (s *store) loadRawExamples() (map[string]map[string]int, error) {
	rows, err := s.db.Query(`SELECT template, raw, count FROM raw_examples`)
	if err != nil {
		return nil, fmt.Errorf("query raw_examples: %w", err)
	}
	defer rows.Close()

	m := make(map[string]map[string]int)
	for rows.Next() {
		var tmpl, raw string
		var count int
		if err := rows.Scan(&tmpl, &raw, &count); err != nil {
			return nil, fmt.Errorf("scan raw_example: %w", err)
		}
		inner, ok := m[tmpl]
		if !ok {
			inner = make(map[string]int)
			m[tmpl] = inner
		}
		inner[raw] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate raw_examples: %w", err)
	}
	return m, nil
}

// saveRawExamples upserts a batch of template→raw mappings.
func (s *store) saveRawExamples(examples map[string]map[string]int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO raw_examples (template, raw, count)
		VALUES (?, ?, ?)
		ON CONFLICT(template, raw) DO UPDATE SET
			count = excluded.count
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for tmpl, inner := range examples {
		for raw, count := range inner {
			if _, err := stmt.Exec(tmpl, raw, count); err != nil {
				return fmt.Errorf("exec upsert: %w", err)
			}
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
