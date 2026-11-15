package store_test

import (
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/store"
)

func TestOpenAppliesPragmasAndSchema(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var mode string
	if err := s.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}

	v, err := s.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("schema version = %d, want 1", v)
	}
}

func TestDowngradeIsRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE kv SET value = '99' WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := store.Open(p); err == nil || !strings.Contains(err.Error(), "versi 99") {
		t.Fatalf("want downgrade error, got %v", err)
	}
}
