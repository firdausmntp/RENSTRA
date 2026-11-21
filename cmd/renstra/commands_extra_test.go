package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/provider"
	"renstra/internal/telegram"
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

func TestRememberMemoryMenuAndExport(t *testing.T) {
	st := newTestStore(t)
	ws := t.TempDir()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &agent.Runner{Store: st, Memory: mem, Model: "m", Cwd: ws}
	h := &telegramHandler{store: st, runner: r}
	ctx := context.Background()

	out, err := h.Handle(ctx, 99, "/remember server staging port 8080")
	if err != nil || !strings.Contains(out, "MEMORY.md") {
		t.Fatalf("/remember memory: out=%q err=%v", out, err)
	}
	out, err = h.Handle(ctx, 99, "/remember user: pakai bahasa santai")
	if err != nil || !strings.Contains(out, "USER.md") {
		t.Fatalf("/remember user: out=%q err=%v", out, err)
	}
	snap := mem.Snapshot()
	if !strings.Contains(snap.Memory, "server staging port 8080") || !strings.Contains(snap.User, "pakai bahasa santai") {
		t.Fatalf("snapshot mismatch: mem=%q user=%q", snap.Memory, snap.User)
	}

	// Build /memory menu & verify clear callback
	text, rows := h.buildMemoryMenu()
	if !strings.Contains(text, "MEMORY") || len(rows) != 2 {
		t.Fatalf("memory menu invalid: %q rows=%d", text, len(rows))
	}
	h.handleMemoryCallback(ctx, 99, 1, []string{"mem", "clr", "memory"})
	if got := strings.TrimSpace(mem.Snapshot().Memory); got != "" {
		t.Fatalf("memory belum terhapus via callback: %q", got)
	}

	// Seed conversation messages and test /export
	if _, err := st.AppendMessage(ctx, 99, provider.Message{Role: provider.RoleUser, Content: "halo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(ctx, 99, provider.Message{Role: provider.RoleAssistant, Content: "hai juga"}); err != nil {
		t.Fatal(err)
	}
	expOut, err := h.Handle(ctx, 99, "/export")
	if err != nil || !strings.Contains(expOut, ".md") {
		t.Fatalf("/export failed: out=%q err=%v", expOut, err)
	}
	matches, _ := filepath.Glob(filepath.Join(ws, "tmp", "chat-99-*.md"))
	if len(matches) != 1 {
		t.Fatalf("file export .md tidak terbuat: %v", matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil || !strings.Contains(string(raw), "hai juga") {
		t.Fatalf("isi export tidak sesuai: %q err=%v", string(raw), err)
	}
}

func TestModelSetAndBackToMenuButtons(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RENSTRA_HOME", dir)
	cfgYAML := "default_model: oai:gpt-4o-mini\nproviders:\n  oai:\n    kind: openai\n    default_model: gpt-4o-mini\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := modelCmd([]string{"set", "gpt-4o"}); err != nil {
		t.Fatalf("modelCmd set failed: %v", err)
	}
	cfg, err := config.Load()
	if err != nil || cfg.DefaultModel != "oai:gpt-4o" {
		t.Fatalf("DefaultModel = %q err=%v", cfg.DefaultModel, err)
	}

	st := newTestStore(t)
	mem, _ := memory.Open(t.TempDir())
	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st, Memory: mem, Model: "gpt-4o"}, cfg: cfg}
	ctx := context.Background()

	hasMenuBtn := func(rows [][]telegram.InlineButton) bool {
		for _, r := range rows {
			for _, b := range r {
				if b.CallbackData == "cmd:menu" {
					return true
				}
			}
		}
		return false
	}

	_, r1 := h.buildModelMenu(ctx, 1, 0)
	_, r2 := h.buildApprovalMenu(ctx, 1)
	_, r3 := h.buildCronMenu(ctx, 1, 0)
	_, r4 := h.buildConfigMenu(ctx, 1, 0)
	_, r5 := h.buildMemoryMenu()
	_, r6 := h.buildStatusMenu(ctx, 1)
	_, r7 := h.buildFallbackMenu(ctx, 1)
	for name, rows := range map[string][][]telegram.InlineButton{
		"model": r1, "approval": r2, "cron": r3, "config": r4, "memory": r5, "status": r6, "fallback": r7,
	} {
		if !hasMenuBtn(rows) {
			t.Fatalf("menu %s tidak memiliki tombol Back to Menu (cmd:menu)", name)
		}
	}

	// Auto-discovery Smart Fallback discovers secondary configured providers
	on := true
	cfgMulti := &config.Config{
		DefaultModel:  "p1:m1",
		SmartFallback: &on,
		Providers: map[string]config.ProviderConfig{
			"p1": {Kind: "openai", BaseURL: "https://example.invalid/v1", APIKey: "k1", DefaultModel: "m1"},
			"p2": {Kind: "groq", BaseURL: "https://example.invalid/v1", APIKey: "k2", DefaultModel: "llama-3.3-70b"},
		},
	}
	targets := buildFallbacks(cfgMulti)
	if len(targets) != 1 || targets[0].Model != "llama-3.3-70b" {
		t.Fatalf("auto-discovery fallbacks = %+v, want llama-3.3-70b", targets)
	}
}

func TestRestartCmdAndPIDLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RENSTRA_HOME", home)

	// No running service/server -> clean informative output, no error
	var out strings.Builder
	if err := restartCmd(nil, &out); err != nil {
		t.Fatalf("restartCmd without running server failed: %v", err)
	}
	if !strings.Contains(out.String(), "Tidak ada service") {
		t.Fatalf("unexpected restart output: %q", out.String())
	}

	// Stale PID file is cleaned up automatically
	if err := os.WriteFile(pidFilePath(home), []byte("99999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, alive := readLivePID(home); alive {
		t.Fatal("stale PID should not be reported alive")
	}
	if _, err := os.Stat(pidFilePath(home)); !os.IsNotExist(err) {
		t.Fatalf("stale PID file should be removed, err=%v", err)
	}

	// writePIDFile cleanup removes own PID, ignores overwritten PID
	cleanup, err := writePIDFile(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidFilePath(home), []byte("123456\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(pidFilePath(home)); err != nil {
		t.Fatalf("cleanup should not remove PID file overwritten by newer process: %v", err)
	}
}

func TestErrorLogRecorderMasksSecretsAndHandlesLogsCmd(t *testing.T) {
	home := t.TempDir()
	rec := newErrorLogRecorder(home, "v1.2.6")
	snap := rec.recordAndWriteSnapshot(42, "openai:gpt-4o", os.ErrPermission)
	if snap == "" {
		t.Fatal("expected diagnostic snapshot path")
	}
	snap2 := rec.recordAndWriteSnapshot(42, "openai:gpt-4o", os.NewSyscallError("auth sk-supersecretkey12345678", os.ErrInvalid))
	raw, err := os.ReadFile(snap2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-supersecretkey12345678") {
		t.Fatalf("secret leaked in diagnostic snapshot:\n%s", string(raw))
	}
	if !strings.Contains(string(raw), "sk-s****") {
		t.Fatalf("expected redacted key prefix in snapshot:\n%s", string(raw))
	}

	st := newTestStore(t)
	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st, Model: "m"}, errLog: rec}
	out, err := h.Handle(context.Background(), 42, "/logs clear")
	if err != nil || !strings.Contains(out, "dibersihkan") {
		t.Fatalf("/logs clear: out=%q err=%v", out, err)
	}
}
