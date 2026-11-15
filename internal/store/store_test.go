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

func TestOpenIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	s1, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()
	s2, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
}
