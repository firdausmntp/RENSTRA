package memory_test

import (
	"strings"
	"testing"

	"renstra/internal/memory"
)

func TestOpenAndLimits(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	memLimit, userLimit := st.Limits()
	if memLimit != 2200 || userLimit != 1375 {
		t.Fatalf("unexpected limits: %d, %d", memLimit, userLimit)
	}

	snap := st.Snapshot()
	if snap.Memory != "" || snap.User != "" {
		t.Fatalf("expected empty snapshot, got %q, %q", snap.Memory, snap.User)
	}
}

func TestWriteBoundaries(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Tepat pada batas maksimum
	memExact := strings.Repeat("A", 2200)
	if err := st.Write(memory.KindMemory, memExact); err != nil {
		t.Fatalf("write exact memory: %v", err)
	}

	userExact := strings.Repeat("B", 1375)
	if err := st.Write(memory.KindUser, userExact); err != nil {
		t.Fatalf("write exact user: %v", err)
	}

	snap := st.Snapshot()
	if snap.Memory != memExact {
		t.Fatalf("memory snapshot mismatch at boundary")
	}
	if snap.User != userExact {
		t.Fatalf("user snapshot mismatch at boundary")
	}
}

func TestWriteRuneCountBoundaries(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// 2200 rune multi-byte (misal huruf 'é')
	memMulti := strings.Repeat("é", 2200)
	if err := st.Write(memory.KindMemory, memMulti); err != nil {
		t.Fatalf("write 2200 runes multi-byte: %v", err)
	}
	if snap := st.Snapshot(); snap.Memory != memMulti {
		t.Fatalf("memory snapshot mismatch for multi-byte runes")
	}
}

func TestWriteOverflowError(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	initialMem := "initial memory"
	if err := st.Write(memory.KindMemory, initialMem); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	// 2201 karakter -> harus gagal dan pesan dalam bahasa Indonesia
	overflowMem := strings.Repeat("x", 2201)
	err = st.Write(memory.KindMemory, overflowMem)
	if err == nil {
		t.Fatalf("expected error on memory overflow, got nil")
	}
	// Periksa apakah pesan menyebutkan jumlah karakter dan batas
	msg := err.Error()
	if !strings.Contains(msg, "2201") || !strings.Contains(msg, "2200") {
		t.Fatalf("error message should state current and limit chars: %s", msg)
	}

	// Pastikan file TIDAK berubah
	if snap := st.Snapshot(); snap.Memory != initialMem {
		t.Fatalf("memory was mutated despite overflow: got %q, want %q", snap.Memory, initialMem)
	}

	// Uji user overflow: 1376 karakter
	overflowUser := strings.Repeat("y", 1376)
	err = st.Write(memory.KindUser, overflowUser)
	if err == nil {
		t.Fatalf("expected error on user overflow, got nil")
	}
	msgUser := err.Error()
	if !strings.Contains(msgUser, "1376") || !strings.Contains(msgUser, "1375") {
		t.Fatalf("error message should state current and limit chars: %s", msgUser)
	}
}

func TestSnapshotFrozen(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := st.Write(memory.KindMemory, "v1-mem"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := st.Write(memory.KindUser, "v1-user"); err != nil {
		t.Fatalf("write: %v", err)
	}

	frozen := st.Snapshot()

	// Update isi store
	if err := st.Write(memory.KindMemory, "v2-mem"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := st.Write(memory.KindUser, "v2-user"); err != nil {
		t.Fatalf("write: %v", err)
	}

	// frozen tidak boleh berubah
	if frozen.Memory != "v1-mem" || frozen.User != "v1-user" {
		t.Fatalf("frozen snapshot mutated: got %+v", frozen)
	}

	// Snapshot baru harus dapat versi baru
	latest := st.Snapshot()
	if latest.Memory != "v2-mem" || latest.User != "v2-user" {
		t.Fatalf("latest snapshot mismatch: got %+v", latest)
	}
}

func TestInvalidKind(t *testing.T) {
	dir := t.TempDir()
	st, err := memory.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Write("unknown", "hello"); err == nil {
		t.Fatalf("expected error on unknown kind")
	}
}
