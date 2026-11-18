package migrate_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/migrate"
	"renstra/internal/skill"
	"renstra/internal/store"
)

func TestFromHermesFullMigrationAndIdempotency(t *testing.T) {
	hermesDir := t.TempDir()
	renstraDir := t.TempDir()

	// 1. Memories & SOUL.md
	if err := os.MkdirAll(filepath.Join(hermesDir, "memories"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hermesDir, "memories", "MEMORY.md"), []byte("server prod di 10.1.1.5"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hermesDir, "memories", "USER.md"), []byte("nama user: Firdaus"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hermesDir, "SOUL.md"), []byte("kamu adalah asisten teknis"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Skills (nested category folder + top-level md)
	deployDir := filepath.Join(hermesDir, "skills", "devops", "deploy-app")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "SKILL.md"), []byte("---\nname: deploy-app\ndescription: cara deploy\n---\n\njalanin docker compose up -d\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. .env & config.yaml (with mcp_servers & telegram)
	if err := os.WriteFile(filepath.Join(hermesDir, ".env"), []byte("OPENROUTER_API_KEY=sk-or-test\nTELEGRAM_BOT_TOKEN=12345:XYZ\nTELEGRAM_ALLOWED_USERS=777,888\nGH_TOK=ghp_abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hermesYAML := `
model:
  provider: openrouter
  default: anthropic/claude-3.5-sonnet
agent:
  max_turns: 30
mcp_servers:
  github:
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "${GH_TOK}"
`
	if err := os.WriteFile(filepath.Join(hermesDir, "config.yaml"), []byte(hermesYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. Hermes state.db (1 telegram session + 1 cli session)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(hermesDir, "state.db")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			source_platform TEXT,
			user_id TEXT,
			started_at TEXT
		);
		CREATE TABLE messages (
			message_id TEXT PRIMARY KEY,
			session_id TEXT,
			role TEXT,
			content TEXT,
			tool_calls TEXT,
			timestamp TEXT
		);
		INSERT INTO sessions VALUES ('s-tg', 'telegram', '777', '2026-11-01T10:00:00Z');
		INSERT INTO sessions VALUES ('s-cli', 'cli', 'local', '2026-11-02T10:00:00Z');
		INSERT INTO messages VALUES ('m1', 's-tg', 'user', 'halo dari hermes', '', '2026-11-01T10:00:01Z');
		INSERT INTO messages VALUES ('m2', 's-tg', 'assistant', 'siap bos', '', '2026-11-01T10:00:02Z');
		INSERT INTO messages VALUES ('m3', 's-cli', 'user', 'catatan cli lama', '', '2026-11-02T10:00:01Z');
	`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Dry run first must not create anything
	dry, err := migrate.FromHermes(context.Background(), migrate.Options{
		FromDir: hermesDir,
		ToDir:   renstraDir,
		DryRun:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.ConfigWritten || !dry.MCPWritten || !dry.MemoryCopied || !dry.UserCopied || dry.SkillsCopied != 1 || dry.SessionsCopied != 2 || dry.MessagesCopied != 3 {
		t.Fatalf("unexpected dry run report: %+v", dry)
	}
	if _, err := os.Stat(filepath.Join(renstraDir, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("dry-run must not write config.yaml")
	}

	// Real migration
	rep, err := migrate.FromHermes(context.Background(), migrate.Options{
		FromDir: hermesDir,
		ToDir:   renstraDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SessionsCopied != 2 || rep.MessagesCopied != 3 || rep.SkillsCopied != 1 || rep.MCPServers != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}

	// Verify config loads cleanly via RENSTRA config loader
	t.Setenv("RENSTRA_HOME", renstraDir)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.Token != "12345:XYZ" || len(cfg.Telegram.AllowedUsers) != 2 {
		t.Fatalf("telegram config = %+v", cfg.Telegram)
	}
	if cfg.Agent.SystemPromptPrefix != "kamu adalah asisten teknis" || cfg.Agent.MaxIterations != 30 {
		t.Fatalf("agent config = %+v", cfg.Agent)
	}
	if _, model, err := config.Resolve(cfg.DefaultModel, cfg); err != nil || model != "anthropic/claude-3.5-sonnet" {
		t.Fatalf("resolve default_model %q: model=%q err=%v", cfg.DefaultModel, model, err)
	}

	// Verify memory & skills
	mem, err := memory.Open(filepath.Join(renstraDir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	snap := mem.Snapshot()
	if snap.Memory != "server prod di 10.1.1.5" || snap.User != "nama user: Firdaus" {
		t.Fatalf("snap = %+v", snap)
	}

	skReg, err := skill.Load(skill.Layer{Dir: filepath.Join(renstraDir, "skills"), Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	if sk, ok := skReg.Get("deploy-app"); !ok || !strings.Contains(sk.Body, "docker compose") {
		t.Fatalf("skill deploy-app missing: ok=%v sk=%+v", ok, sk)
	}

	// Verify mcp.yaml expanded env
	mcpRaw, err := os.ReadFile(filepath.Join(renstraDir, "mcp.yaml"))
	if err != nil || !strings.Contains(string(mcpRaw), "GITHUB_PERSONAL_ACCESS_TOKEN=ghp_abc") {
		t.Fatalf("mcp.yaml = %q err=%v", string(mcpRaw), err)
	}

	// Verify SQLite messages imported and searchable
	st, err := store.Open(filepath.Join(renstraDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tgHits, err := st.SearchMessages(context.Background(), 777, "hermes", 10)
	if err != nil || len(tgHits) != 1 || tgHits[0].Content != "halo dari hermes" {
		t.Fatalf("tg search = %+v err=%v", tgHits, err)
	}
	cliHits, err := st.SearchMessages(context.Background(), 1, "cli lama", 10)
	if err != nil || len(cliHits) != 1 {
		t.Fatalf("cli search = %+v err=%v", cliHits, err)
	}

	// Second run without --force skips already migrated items
	rep2, err := migrate.FromHermes(context.Background(), migrate.Options{
		FromDir: hermesDir,
		ToDir:   renstraDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.SessionsCopied != 0 || rep2.SessionsSkipped != 2 || rep2.SkillsCopied != 0 || rep2.SkillsSkipped != 1 {
		t.Fatalf("expected idempotent skip on rerun, got %+v", rep2)
	}
}
