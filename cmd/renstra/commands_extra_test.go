package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/config"
)

func TestMigrateCmdHermesDryRunAndRun(t *testing.T) {
	hermesDir := t.TempDir()
	renstraDir := t.TempDir()
	t.Setenv("RENSTRA_HOME", renstraDir)

	if err := os.MkdirAll(filepath.Join(hermesDir, "memories"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hermesDir, "memories", "MEMORY.md"), []byte("catatan lama"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := migrateCmd([]string{"hermes", "--from", hermesDir, "--dry-run"}); err != nil {
		t.Fatalf("migrate dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(renstraDir, "memory", "MEMORY.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run tidak boleh menulis file")
	}

	if err := migrateCmd([]string{"hermes", "--from", hermesDir}); err != nil {
		t.Fatalf("migrate hermes: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(renstraDir, "memory", "MEMORY.md"))
	if err != nil || string(got) != "catatan lama" {
		t.Fatalf("got=%q err=%v", string(got), err)
	}
}

func TestSkillAddFromLocalDirectory(t *testing.T) {
	renstraDir := t.TempDir()
	srcDir := t.TempDir()
	t.Setenv("RENSTRA_HOME", renstraDir)

	skillDir := filepath.Join(srcDir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: my-skill\ndescription: Skill uji\n---\nIsi panduan.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := skillCmd([]string{"add", skillDir}); err != nil {
		t.Fatalf("skill add dir: %v", err)
	}
	installed, err := os.ReadFile(filepath.Join(renstraDir, "skills", "my-skill", "SKILL.md"))
	if err != nil || string(installed) != content {
		t.Fatalf("skill tidak tersalin benar: err=%v got=%q", err, string(installed))
	}
}

func TestResolveAuxTarget(t *testing.T) {
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{
			"fast": {
				Kind:         "openai",
				BaseURL:      "https://api.openai.com/v1",
				DefaultModel: "gpt-4o-mini",
				Models: map[string]config.ModelConfig{
					"gpt-4o-mini": {PriceIn: 0.15, PriceOut: 0.60},
				},
			},
		},
		Agent: config.Agent{AuxModel: "fast:gpt-4o-mini"},
	}
	prov, model, pin, pout, err := resolveAuxTarget(cfg, nil, "main", config.ModelConfig{})
	if err != nil {
		t.Fatalf("resolveAuxTarget: %v", err)
	}
	if prov == nil || model != "gpt-4o-mini" || pin != 0.15 || pout != 0.60 {
		t.Fatalf("hasil salah: prov=%v model=%s in=%v out=%v", prov, model, pin, pout)
	}
}

func TestUpdateCmdHelpAndUsage(t *testing.T) {
	if err := updateCmd([]string{"check"}); err != nil {
		if !strings.Contains(err.Error(), "gagal memeriksa pembaruan") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestSlashApprovalCommandAndPersistence(t *testing.T) {
	st := newTestStore(t)
	r := &agent.Runner{Store: st, Model: "m"}
	h := &telegramHandler{store: st, runner: r}
	ctx := context.Background()

	reply, err := h.Handle(ctx, 88, "/approval")
	if err != nil {
		t.Fatalf("/approval: %v", err)
	}
	if !strings.Contains(reply, "prompt") {
		t.Fatalf("default harus prompt, got: %q", reply)
	}

	reply, err = h.Handle(ctx, 88, "/approval auto")
	if err != nil {
		t.Fatalf("/approval auto: %v", err)
	}
	if !strings.Contains(reply, "auto") {
		t.Fatalf("reply = %q", reply)
	}
	if got := h.runnerForChat(ctx, 88).GetApprovalMode(); got != "auto" {
		t.Fatalf("mode di runner = %q, want auto", got)
	}

	h2 := &telegramHandler{store: st, runner: &agent.Runner{Store: st, Model: "m"}}
	if got := h2.runnerForChat(ctx, 88).GetApprovalMode(); got != "auto" {
		t.Fatalf("mode tidak persisten setelah restart: %q, want auto", got)
	}
	if got := h2.runnerForChat(ctx, 89).GetApprovalMode(); got != "prompt" {
		t.Fatalf("chat lain harus tetap prompt: %q", got)
	}
}

func TestModelMenuPaginationAndColoredButtons(t *testing.T) {
	st := newTestStore(t)
	cfg := &config.Config{
		DefaultModel: "p1:m1",
		Providers: map[string]config.ProviderConfig{
			"p1": {
				Kind:         "openai",
				DefaultModel: "m1",
				Models: map[string]config.ModelConfig{
					"m1": {}, "m2": {}, "m3": {}, "m4": {}, "m5": {}, "m6": {}, "m7": {},
				},
			},
		},
	}
	r := &agent.Runner{Store: st, Model: "m1"}
	h := &telegramHandler{store: st, runner: r, cfg: cfg}
	ctx := context.Background()

	txt0, rows0 := h.buildModelMenu(ctx, 50, 0)
	if !strings.Contains(txt0, "Halaman 1/2") {
		t.Fatalf("expected Halaman 1/2, got: %s", txt0)
	}
	// 5 model rows + 1 pagination row + 1 action row = 7 rows
	if len(rows0) != 7 {
		t.Fatalf("expected 7 rows on page 0, got %d", len(rows0))
	}
	if rows0[0][0].Style != "success" || !strings.HasPrefix(rows0[0][0].Text, "✅ ") {
		t.Fatalf("active model button should be green success with ✅, got %+v", rows0[0][0])
	}
	if rows0[1][0].Style != "primary" {
		t.Fatalf("inactive model button should be primary, got %+v", rows0[1][0])
	}

	txt1, rows1 := h.buildModelMenu(ctx, 50, 1)
	if !strings.Contains(txt1, "Halaman 2/2") {
		t.Fatalf("expected Halaman 2/2, got: %s", txt1)
	}
	// 2 model rows + 1 pagination row + 1 action row = 4 rows
	if len(rows1) != 4 {
		t.Fatalf("expected 4 rows on page 1, got %d", len(rows1))
	}

	_, aRows := h.buildApprovalMenu(ctx, 50)
	if len(aRows) == 0 || len(aRows[0]) != 3 {
		t.Fatalf("expected 3 approval buttons, got %+v", aRows)
	}
	if aRows[0][2].Style != "danger" {
		t.Fatalf("deny button should have danger style, got %+v", aRows[0][2])
	}
}

func TestConfigMenuAndTextOverridesPersistence(t *testing.T) {
	st := newTestStore(t)
	r := &agent.Runner{Store: st, Model: "m", MaxIters: 40, Threshold: 0.7}
	h := &telegramHandler{store: st, runner: r}
	ctx := context.Background()

	if _, err := h.Handle(ctx, 77, "/config max_iterations 60"); err != nil {
		t.Fatalf("/config max_iterations: %v", err)
	}
	if _, err := h.Handle(ctx, 77, "/config auto_compact off"); err != nil {
		t.Fatalf("/config auto_compact: %v", err)
	}
	if _, err := h.Handle(ctx, 77, "/config warn_usd 3.5"); err != nil {
		t.Fatalf("/config warn_usd: %v", err)
	}

	cr := h.runnerForChat(ctx, 77)
	if cr.MaxIters != 60 || !cr.DisableAutoCompact || cr.Limits.WarnDailyUSD != 3.5 {
		t.Fatalf("settings not applied: maxIters=%d disableCompact=%v warn=%v", cr.MaxIters, cr.DisableAutoCompact, cr.Limits.WarnDailyUSD)
	}

	// Restart handler and verify persistence
	h2 := &telegramHandler{store: st, runner: &agent.Runner{Store: st, Model: "m", MaxIters: 40, Threshold: 0.7}}
	cr2 := h2.runnerForChat(ctx, 77)
	if cr2.MaxIters != 60 || !cr2.DisableAutoCompact || cr2.Limits.WarnDailyUSD != 3.5 {
		t.Fatalf("settings not persisted: maxIters=%d disableCompact=%v warn=%v", cr2.MaxIters, cr2.DisableAutoCompact, cr2.Limits.WarnDailyUSD)
	}

	// Reset returns to defaults
	if _, err := h2.Handle(ctx, 77, "/config reset"); err != nil {
		t.Fatalf("/config reset: %v", err)
	}
	if cr2.MaxIters != 40 || cr2.DisableAutoCompact {
		t.Fatalf("reset failed: maxIters=%d disableCompact=%v", cr2.MaxIters, cr2.DisableAutoCompact)
	}
}
