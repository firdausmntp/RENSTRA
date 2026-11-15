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
	// Pragma query parameters apply to every connection opened on this pool.
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=temp_store(MEMORY)" +
		"&_pragma=mmap_size(67108864)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	v, err := s.SchemaVersion()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if v > 1 {
		return fmt.Errorf("database memakai schema versi %d, binary ini hanya mendukung versi 1 (perlu update renstra)", v)
	}
	if v == 1 {
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
