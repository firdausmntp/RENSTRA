package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer connection: removes SQLITE_BUSY and keeps write order stable.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA mmap_size=67108864",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	// kv must exist before the version can be read.
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	v, err := s.SchemaVersion()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if v > 0 {
		return nil
	}
	if _, err := s.db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("migrasi schema: %w", err)
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO kv(key, value) VALUES('schema_version', '1')`)
	return err
}

func (s *Store) SchemaVersion() (int, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = 'schema_version'`).Scan(&raw)
	if err != nil {
		return 0, err
	}
	var v int
	if _, err := fmt.Sscanf(raw, "%d", &v); err != nil {
		return 0, err
	}
	return v, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }
