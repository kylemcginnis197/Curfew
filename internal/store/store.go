// Package store persists anchor history to a local SQLite database (pure-Go
// driver, so the binary stays dependency-free).
package store

import (
	"database/sql"
	"time"

	"github.com/kyle/curfew/internal/model"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Store is a handle to the history database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the history database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite: serialize writes, avoid "database is locked"
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           INTEGER NOT NULL,
    provider     TEXT    NOT NULL,
    reset        TEXT,
    outcome      TEXT    NOT NULL,
    detail       TEXT,
    window_start INTEGER
);
CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts);
`

// Record inserts an event and returns it with its assigned ID.
func (s *Store) Record(e model.Event) (model.Event, error) {
	var ws int64
	if !e.WindowStart.IsZero() {
		ws = e.WindowStart.Unix()
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	res, err := s.db.Exec(
		`INSERT INTO events (ts, provider, reset, outcome, detail, window_start) VALUES (?,?,?,?,?,?)`,
		e.Time.Unix(), e.Provider, e.Reset, string(e.Outcome), e.Detail, ws,
	)
	if err != nil {
		return e, err
	}
	e.ID, _ = res.LastInsertId()
	return e, nil
}

// Recent returns the most recent events, newest first, capped at limit.
func (s *Store) Recent(limit int) ([]model.Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, ts, provider, reset, outcome, detail, window_start
		   FROM events ORDER BY ts DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Event
	for rows.Next() {
		var (
			e      model.Event
			ts, ws int64
			reset  sql.NullString
			detail sql.NullString
			out5   string
		)
		if err := rows.Scan(&e.ID, &ts, &e.Provider, &reset, &out5, &detail, &ws); err != nil {
			return nil, err
		}
		e.Time = time.Unix(ts, 0)
		e.Reset = reset.String
		e.Detail = detail.String
		e.Outcome = model.Outcome(out5)
		if ws > 0 {
			e.WindowStart = time.Unix(ws, 0)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }
