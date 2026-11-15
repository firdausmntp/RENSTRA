// Package memory menyimpan catatan agent (MEMORY.md) dan profil user (USER.md)
// dalam satu direktori, dengan batas karakter yang ditegakkan saat menulis.
package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"
)

const (
	MemoryLimit = 2200
	UserLimit   = 1375
)

// Kind memilih berkas mana yang ditulis atau dibaca.
type Kind string

const (
	KindMemory Kind = "memory"
	KindUser   Kind = "user"
)

// Snapshot adalah salinan beku isi memory saat Snapshot dipanggil.
type Snapshot struct {
	Memory string
	User   string
}

type Store struct {
	mu    sync.RWMutex
	paths map[Kind]string
}

// Open menyiapkan direktori dan berkas memory bila belum ada.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("membuat direktori memory: %w", err)
	}
	s := &Store{paths: map[Kind]string{
		KindMemory: filepath.Join(dir, "MEMORY.md"),
		KindUser:   filepath.Join(dir, "USER.md"),
	}}
	for kind, path := range s.paths {
		switch _, err := os.Stat(path); {
		case errors.Is(err, os.ErrNotExist):
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				return nil, fmt.Errorf("membuat %s: %w", path, err)
			}
		case err != nil:
			return nil, fmt.Errorf("memeriksa %s: %w", path, err)
		default:
			_ = kind
		}
	}
	return s, nil
}

// Limits mengembalikan batas karakter untuk memory dan profil user.
func (s *Store) Limits() (memLimit, userLimit int) {
	return MemoryLimit, UserLimit
}

// Snapshot membaca kedua berkas. Galat baca diabaikan dan menghasilkan string
// kosong karena tanda tangan ini tidak mengembalikan error.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mem, _ := os.ReadFile(s.paths[KindMemory])
	user, _ := os.ReadFile(s.paths[KindUser])
	return Snapshot{Memory: string(mem), User: string(user)}
}

// Write menimpa berkas untuk kind. Bila isi melebihi batas, tidak ada yang
// ditulis dan galat menyebutkan jumlah karakter saat ini dan batasnya.
func (s *Store) Write(kind Kind, content string) error {
	path, limit, err := s.target(kind)
	if err != nil {
		return err
	}
	if n := utf8.RuneCountInString(content); n > limit {
		return fmt.Errorf("isi %s terlalu panjang: %d karakter, batas %d karakter", path, n, limit)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAtomic(path, content)
}

func (s *Store) target(kind Kind) (path string, limit int, err error) {
	switch kind {
	case KindMemory:
		return s.paths[KindMemory], MemoryLimit, nil
	case KindUser:
		return s.paths[KindUser], UserLimit, nil
	default:
		return "", 0, fmt.Errorf("jenis memory tidak dikenal: %q", kind)
	}
}

// writeAtomic menulis lewat berkas sementara agar catatan lama tidak hilang
// bila proses berhenti di tengah penulisan.
func writeAtomic(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
