package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/provider"
	"renstra/internal/skill"
	"renstra/internal/store"
)

// newTestStore opens a throwaway SQLite store.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestSlashNewBreaksSession ensures /new writes a session break and clears the
// compaction summary, so the next turn starts from a clean context.
func TestSlashNewBreaksSession(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Seed two exchanges plus a summary. Compact needs the cut to land on a
	// user turn, so at least two full exchanges must exist.
	for _, m := range []provider.Message{
		{Role: provider.RoleUser, Content: "halo"},
		{Role: provider.RoleAssistant, Content: "hai"},
		{Role: provider.RoleUser, Content: "lanjut"},
		{Role: provider.RoleAssistant, Content: "siap"},
	} {
		if _, err := st.AppendMessage(ctx, 7, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Compact(ctx, 7, 1, "ringkasan lama"); err != nil {
		t.Fatal(err)
	}
	if st.Summary(ctx, 7) == "" {
		t.Fatal("setup: summary harus ada")
	}

	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st}}
	reply, err := h.Handle(ctx, 7, "/new")
	if err != nil {
		t.Fatalf("/new: %v", err)
	}
	if !strings.Contains(reply, "Sesi baru") {
		t.Fatalf("reply = %q", reply)
	}
	if got := st.Summary(ctx, 7); got != "" {
		t.Fatalf("summary harus terhapus, dapat %q", got)
	}
}

// TestSlashUsageEmptyDay ensures /usage does not fail on a day with no recorded
// usage (regression: UsageForDay used to surface sql.ErrNoRows).
func TestSlashUsageEmptyDay(t *testing.T) {
	st := newTestStore(t)
	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st}}

	reply, err := h.Handle(context.Background(), 8, "/usage")
	if err != nil {
		t.Fatalf("/usage tanpa data: %v", err)
	}
	if !strings.Contains(reply, "Token Masuk: 0") {
		t.Fatalf("reply = %q", reply)
	}
}

// TestSlashUsageAfterRecord ensures recorded usage is reported back.
func TestSlashUsageAfterRecord(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st}}

	reply, _ := h.Handle(ctx, 9, "/usage")
	today := extractDay(t, reply)
	if err := st.RecordUsage(ctx, today, 1000, 200, 0.05); err != nil {
		t.Fatal(err)
	}

	reply, err := h.Handle(ctx, 9, "/usage")
	if err != nil {
		t.Fatalf("/usage: %v", err)
	}
	if !strings.Contains(reply, "Token Masuk: 1000") || !strings.Contains(reply, "0.0500") {
		t.Fatalf("reply = %q", reply)
	}
}

// TestSlashModelSwitch ensures /model with an argument swaps the runner model.
func TestSlashModelSwitch(t *testing.T) {
	st := newTestStore(t)
	r := &agent.Runner{Store: st, Model: "lama:gpt-4o"}
	h := &telegramHandler{store: st, runner: r}

	reply, err := h.Handle(context.Background(), 10, "/model baru:claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.runnerForChat(context.Background(), 10).GetModel(); got != "baru:claude" {
		t.Fatalf("chat 10 model = %q, want baru:claude", got)
	}
	if r.GetModel() != "lama:gpt-4o" {
		t.Fatalf("runner default ikut bermutasi ke %q", r.GetModel())
	}
	if !strings.Contains(reply, "baru:claude") {
		t.Fatalf("reply = %q", reply)
	}

	reply, _ = h.Handle(context.Background(), 10, "/model")
	if !strings.Contains(reply, "baru:claude") {
		t.Fatalf("reply = %q", reply)
	}
}

// TestSlashCompactTooShort ensures /compact on a short history is a no-op
// message rather than an error.
func TestSlashCompactTooShort(t *testing.T) {
	st := newTestStore(t)
	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st}}

	reply, err := h.Handle(context.Background(), 11, "/compact")
	if err != nil {
		t.Fatalf("/compact: %v", err)
	}
	if !strings.Contains(reply, "belum cukup") {
		t.Fatalf("reply = %q", reply)
	}
}

// extractDay pulls the YYYY-MM-DD day out of the /usage reply header.
func extractDay(t *testing.T, reply string) string {
	t.Helper()
	open := strings.Index(reply, "(")
	close := strings.Index(reply, ")")
	if open < 0 || close < open {
		t.Fatalf("tidak menemukan tanggal di %q", reply)
	}
	return reply[open+1 : close]
}

func TestSlashInsights(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	today := time.Now().Format("2006-01-02")
	if err := st.RecordUsage(ctx, today, 2000, 500, 0.1234); err != nil {
		t.Fatal(err)
	}

	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st}}
	reply, err := h.Handle(ctx, 20, "/insights")
	if err != nil {
		t.Fatalf("/insights: %v", err)
	}
	if !strings.Contains(reply, "Hari ini") || !strings.Contains(reply, "7 hari terakhir") || !strings.Contains(reply, "30 hari terakhir") || !strings.Contains(reply, "0.1234") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestSlashSkillsListAndReload(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	reg, err := skill.Load(skill.Layer{Dir: dir, Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Create("deploy-app", "cara deploy aplikasi", "build lalu push"); err != nil {
		t.Fatal(err)
	}

	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st, Skills: reg}}
	reply, err := h.Handle(context.Background(), 21, "/skills")
	if err != nil {
		t.Fatalf("/skills: %v", err)
	}
	if !strings.Contains(reply, "deploy-app") {
		t.Fatalf("reply = %q", reply)
	}

	reply, err = h.Handle(context.Background(), 21, "/skills reload")
	if err != nil {
		t.Fatalf("/skills reload: %v", err)
	}
	if !strings.Contains(reply, "dimuat ulang") || !strings.Contains(reply, "deploy-app") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestSlashMemoryShowsContentsAndLimits(t *testing.T) {
	st := newTestStore(t)
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Write(memory.KindMemory, "server prod ada di 10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := mem.Write(memory.KindUser, "nama: Andi"); err != nil {
		t.Fatal(err)
	}

	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st, Memory: mem}}
	reply, err := h.Handle(context.Background(), 22, "/memory")
	if err != nil {
		t.Fatalf("/memory: %v", err)
	}
	if !strings.Contains(reply, "10.0.0.1") || !strings.Contains(reply, "nama: Andi") || !strings.Contains(reply, "/2200") || !strings.Contains(reply, "/1375") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestSlashCronAddListToggleRemove(t *testing.T) {
	st := newTestStore(t)
	h := &telegramHandler{store: st, runner: &agent.Runner{Store: st}}
	ctx := context.Background()

	reply, err := h.Handle(ctx, 23, "/cron add 0 9 * * * | ringkas berita")
	if err != nil {
		t.Fatalf("/cron add: %v", err)
	}
	if !strings.Contains(reply, "ID 1") {
		t.Fatalf("reply = %q", reply)
	}

	reply, err = h.Handle(ctx, 23, "/cron list")
	if err != nil {
		t.Fatalf("/cron list: %v", err)
	}
	if !strings.Contains(reply, "ringkas berita") || !strings.Contains(reply, "[aktif]") {
		t.Fatalf("reply = %q", reply)
	}

	reply, err = h.Handle(ctx, 23, "/cron toggle 1")
	if err != nil {
		t.Fatalf("/cron toggle: %v", err)
	}
	if !strings.Contains(reply, "dinonaktifkan") {
		t.Fatalf("reply = %q", reply)
	}

	reply, err = h.Handle(ctx, 23, "/cron rm 1")
	if err != nil {
		t.Fatalf("/cron rm: %v", err)
	}
	if !strings.Contains(reply, "dihapus") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestSlashModelWithConfigResolvesProviderAndAlias(t *testing.T) {
	st := newTestStore(t)
	cfg := &config.Config{
		DefaultModel: "utama:gpt-4o-mini",
		Providers: map[string]config.ProviderConfig{
			"utama": {
				Kind:         "openai",
				BaseURL:      "https://api.openai.com/v1",
				DefaultModel: "gpt-4o-mini",
				Models: map[string]config.ModelConfig{
					"gpt-4o-mini": {Context: 128000, MaxOutput: 4096, PriceIn: 0.15, PriceOut: 0.60},
				},
			},
			"ant": {
				Kind:         "anthropic",
				BaseURL:      "https://api.anthropic.com",
				DefaultModel: "claude-sonnet-4-5",
				Models: map[string]config.ModelConfig{
					"claude-sonnet-4-5": {Context: 200000, MaxOutput: 8192, PriceIn: 3.0, PriceOut: 15.0},
				},
			},
		},
		Aliases: map[string]string{
			"sonnet": "ant:claude-sonnet-4-5",
		},
	}

	r := &agent.Runner{Store: st, Model: "gpt-4o-mini", ContextWindow: 128000}
	h := &telegramHandler{store: st, runner: r, cfg: cfg}

	listReply, err := h.Handle(context.Background(), 24, "/model")
	if err != nil {
		t.Fatalf("/model: %v", err)
	}
	if !strings.Contains(listReply, "gpt-4o-mini") || !strings.Contains(listReply, "sonnet -> ant:claude-sonnet-4-5") {
		t.Fatalf("listReply = %q", listReply)
	}

	switchReply, err := h.Handle(context.Background(), 24, "/model sonnet")
	if err != nil {
		t.Fatalf("/model sonnet: %v", err)
	}
	if !strings.Contains(switchReply, "claude-sonnet-4-5") {
		t.Fatalf("switchReply = %q", switchReply)
	}
	chat24 := h.runnerForChat(context.Background(), 24)
	if chat24.GetModel() != "claude-sonnet-4-5" || chat24.ContextWindow != 200000 || chat24.MaxTokens != 8192 || chat24.PriceOut != 15.0 {
		t.Fatalf("runner chat 24 caps tidak terupdate: model=%q ctx=%d maxTok=%d priceOut=%v", chat24.GetModel(), chat24.ContextWindow, chat24.MaxTokens, chat24.PriceOut)
	}
	if chat24.Provider == nil || chat24.Provider.Name() != "anthropic" {
		t.Fatalf("provider chat 24 tidak berganti ke anthropic: %v", chat24.Provider)
	}
	if r.GetModel() != "gpt-4o-mini" {
		t.Fatalf("runner default ikut bermutasi ke %q (harus terisolasi per chat)", r.GetModel())
	}
	chat99 := h.runnerForChat(context.Background(), 99)
	if chat99.GetModel() != "gpt-4o-mini" {
		t.Fatalf("chat lain ikut bermutasi: got %q", chat99.GetModel())
	}
	saved, err := st.GetChatModel(context.Background(), 24)
	if err != nil || saved != "sonnet" {
		t.Fatalf("model per chat tidak tersimpan di DB: got=%q err=%v", saved, err)
	}
}

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
	// Memastikan binary flags dan usage string terdaftar
	if err := updateCmd([]string{"check"}); err != nil {
		// Jika unauthenticated rate limit atau network offline di test environment,
		// error tetap di-wrap dengan rapi dan tidak panik.
		if !strings.Contains(err.Error(), "gagal memeriksa pembaruan") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}
