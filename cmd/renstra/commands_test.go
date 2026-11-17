package main

import (
	"context"
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
	if r.Model != "baru:claude" {
		t.Fatalf("model = %q", r.Model)
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
	if r.GetModel() != "claude-sonnet-4-5" || r.ContextWindow != 200000 || r.MaxTokens != 8192 || r.PriceOut != 15.0 {
		t.Fatalf("runner caps tidak terupdate: model=%q ctx=%d maxTok=%d priceOut=%v", r.GetModel(), r.ContextWindow, r.MaxTokens, r.PriceOut)
	}
	if r.Provider == nil || r.Provider.Name() != "anthropic" {
		t.Fatalf("provider tidak berganti ke anthropic: %v", r.Provider)
	}
}
