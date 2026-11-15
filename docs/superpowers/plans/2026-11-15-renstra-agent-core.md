# RENSTRA Agent — Plan 1: Core CLI Agent

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Binary Go yang bisa diajak ngobrol dari CLI, memanggil tool (`terminal`, file), menyimpan riwayat di SQLite, dan memakai provider OpenAI-compatible mana pun — tanpa Telegram.

**Architecture:** Satu binary berisi konfigurasi, store SQLite, provider interface + adapter OpenAI, registri tool, dan agent loop yang rekursif-aman (struct `Runner`, bukan singleton). CLI adalah salah satu adapter; Telegram menyusul di Plan 2 dengan memakai `Runner` yang sama.

**Tech Stack:** Go 1.24, `modernc.org/sqlite` (tanpa cgo), `gopkg.in/yaml.v3`, `net/http` stdlib.

**Spec:** `docs/superpowers/specs/2026-11-15-renstra-agent-design.md`

## Global Constraints

- Go 1.24; tanpa cgo; `CGO_ENABLED=0`.
- Driver SQLite `modernc.org/sqlite`; pragma: `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`, `temp_store=MEMORY`, `mmap_size=64MB`.
- Satu koneksi tulis: `db.SetMaxOpenConns(1)`.
- Semua format pesan internal memakai bentuk OpenAI: `role`, `content`, `tool_calls`, `tool_call_id`.
- Output tool dipotong 30 KB pada batas rune UTF-8; sisanya ke `$RENSTRA_HOME/workspace/.tool-output/<turn>-<n>.txt`.
- Agent bebas mengakses filesystem; `cwd` default `$RENSTRA_HOME/workspace/`; tidak ada jail.
- Secret hanya dari env (`api_key_env`); tidak pernah ditulis ke DB atau log.
- Path default: `~/.renstra` (Linux/macOS), `%APPDATA%\renstra` (Windows); override `RENSTRA_HOME`.
- Setiap task diakhiri test yang lulus dan satu commit.

## Review Focus

- Provider mengirim SSE terpotong di tengah JSON tool call → parser harus menahan buffer sampai event `[DONE]`, bukan menutup channel dengan argumen separuh.
- `terminal` mengeluarkan output > 30 KB berisi karakter multibyte → potongan tidak boleh menghasilkan byte UTF-8 invalid.
- Model mengembalikan nama tool yang tidak ada di registry → loop harus memberi hasil tool berisi error dan lanjut, bukan panic.
- Model mengembalikan `arguments` yang bukan JSON valid → hasil tool berisi error parse, satu kesempatan retry, lalu lanjut.
- Turn dibatalkan di tengah streaming (`Ctrl+C`) → teks parsial disimpan sebagai pesan `assistant` dan `Run` mengembalikan `context.Canceled`, bukan kehilangan output.

## File Structure

```
go.mod, go.sum
cmd/renstra/main.go                 # subcommand: chat, config, usage
internal/config/config.go           # Config, Load, Home(), paths
internal/config/config_test.go
internal/store/store.go             # Open, pragma, migrasi
internal/store/schema.sql
internal/store/store_test.go
internal/store/messages.go          # AppendMessage, Messages, SessionBreak
internal/store/messages_test.go
internal/provider/provider.go       # Message, ToolCall, ToolDef, Request, Event, Provider
internal/provider/openai/client.go  # SSE streaming, tool call assembly
internal/provider/openai/client_test.go
internal/tool/tool.go               # Tool interface, Registry, truncation
internal/tool/terminal.go
internal/tool/file.go               # read_file, write_file, patch
internal/tool/tool_test.go
internal/agent/runner.go            # Runner struct, Run loop
internal/agent/prompt.go            # system prompt
internal/agent/runner_test.go
internal/cli/repl.go                # REPL, streaming render, approval
internal/cli/repl_test.go
```

---

### Task 1: Module, config, dan path

**Files:**
- Create: `go.mod`, `cmd/renstra/main.go`, `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `config.Config` dengan field `Home string`, `DefaultModel string`, `Providers map[string]ProviderConfig`, `Aliases map[string]string`, `Limits Limits`
  - `config.ProviderConfig{Kind, APIStyle, BaseURL, Path, APIKeyEnv string, Headers map[string]string, Proxy string, Timeout time.Duration, Models map[string]ModelConfig, DefaultModel string}`
  - `config.ModelConfig{Context, MaxOutput int, Tools, Vision bool, PriceIn, PriceOut float64}`
  - `config.Load() (*Config, error)`, `config.Home() string`, `config.Resolve(ref string, cfg *Config) (ProviderConfig, model string, error)`

- [ ] **Step 1: Tulis test yang gagal**

```go
func TestHomeUsesEnv(t *testing.T) {
    dir := t.TempDir()
    t.Setenv("RENSTRA_HOME", dir)
    if got := config.Home(); got != dir {
        t.Fatalf("Home() = %q, want %q", got, dir)
    }
}

func TestResolveAlias(t *testing.T) {
    cfg := &config.Config{
        Providers: map[string]config.ProviderConfig{"anthropic": {Kind: "anthropic", DefaultModel: "claude-sonnet-4-5"}},
        Aliases:   map[string]string{"sonnet": "anthropic:claude-sonnet-4-5"},
    }
    p, model, err := config.Resolve("sonnet", cfg)
    if err != nil { t.Fatal(err) }
    if p.Kind != "anthropic" || model != "claude-sonnet-4-5" {
        t.Fatalf("got %q/%q", p.Kind, model)
    }
}

func TestResolveUnknownProviderFails(t *testing.T) {
    _, _, err := config.Resolve("nope:x", &config.Config{})
    if err == nil { t.Fatal("want error") }
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/config/ -run 'TestHome|TestResolve' -v`
Expected: FAIL — package/identifiers belum ada.

- [ ] **Step 3: Implementasi**

`go mod init renstra` lalu `go get gopkg.in/yaml.v3 modernc.org/sqlite`.
`config.Home()`: `RENSTRA_HOME` bila diset, jika tidak `filepath.Join(os.UserConfigDir(), "renstra")` (Linux `~/.config/renstra`, Windows `%APPDATA%\renstra`).
`config.Load()`: baca `Home()/config.yaml`; file tidak ada → config kosong dengan default `Limits.WarnDailyUSD = 1.0`, `Agent.MaxIterations = 40`.
`config.Resolve(ref, cfg)`: `alias` dulu, lalu `provider[:model]`; kecocokan persis nama provider menang atas pemisahan `:`; error menyebut nama yang dicoba.

- [ ] **Step 4: Jalankan, pastikan lulus**

Run: `go test ./internal/config/ -v`
Expected: PASS (3 test).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd internal/config
git commit -m "feat(config): path resolution, config load, model alias"
```

---

### Task 2: Store — buka database, pragma, migrasi

**Files:**
- Create: `internal/store/store.go`, `internal/store/schema.sql`, `internal/store/store_test.go`

**Interfaces:**
- Produces: `store.Open(path string) (*Store, error)`, `(*Store).Close() error`, `(*Store).SchemaVersion() (int, error)`, `(*Store).DB() *sql.DB`

- [ ] **Step 1: Tulis test yang gagal**

```go
func TestOpenAppliesPragmasAndSchema(t *testing.T) {
    s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
    if err != nil { t.Fatal(err) }
    defer s.Close()

    var mode string
    if err := s.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil { t.Fatal(err) }
    if strings.ToLower(mode) != "wal" { t.Fatalf("journal_mode = %q, want wal", mode) }

    v, err := s.SchemaVersion()
    if err != nil { t.Fatal(err) }
    if v != 1 { t.Fatalf("schema version = %d, want 1", v) }
}

func TestOpenIsIdempotent(t *testing.T) {
    p := filepath.Join(t.TempDir(), "state.db")
    s1, err := store.Open(p); if err != nil { t.Fatal(err) }
    s1.Close()
    s2, err := store.Open(p); if err != nil { t.Fatal(err) }
    defer s2.Close()
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/store/ -run TestOpen -v`
Expected: FAIL — `store.Open` belum ada.

- [ ] **Step 3: Implementasi**

DSN: `file:<path>?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)`; setelah `sql.Open`: `SetMaxOpenConns(1)`, eksekusi `journal_mode=WAL`, `synchronous=NORMAL`, `temp_store=MEMORY`, `mmap_size=67108864`.
`schema.sql` membuat tabel dari spec §7.9 (`users`, `chats`, `messages`, `turns`, `jobs`, `usage_daily`, `updates_seen`, `kv`).
Migrasi: baca `kv['schema_version']`; kosong → jalankan `schema.sql`, tulis `1`.

- [ ] **Step 4: Jalankan, pastikan lulus**

Run: `go test ./internal/store/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): sqlite open with wal pragmas and schema migration"
```

---

### Task 3: Store — pesan dan penanda sesi

**Files:**
- Create: `internal/store/messages.go`, `internal/store/messages_test.go`
- Modify: `internal/store/schema.sql` (kolom `session_break` sudah termasuk di Task 2)

**Interfaces:**
- Consumes: `store.Open` (Task 2); `provider.Message` (didefinisikan di Task 4 — paket `store` mengimpor `provider`, bukan sebaliknya, jadi urutan tidak memblokir)
- Produces:
  - `(*Store).AppendMessage(ctx context.Context, chatID int64, m provider.Message) (int64, error)`
  - `(*Store).Messages(ctx context.Context, chatID int64) ([]provider.Message, error)`
  - `(*Store).SessionBreak(ctx context.Context, chatID int64) error`

- [ ] **Step 1: Tulis test yang gagal**

```go
func TestAppendAndReadMessagesInOrder(t *testing.T) {
    s := newTestStore(t)
    ctx := context.Background()
    for _, c := range []string{"halo", "dunia"} {
        if _, err := s.AppendMessage(ctx, 1, store.Message{Role: "user", Content: c}); err != nil { t.Fatal(err) }
    }
    got, err := s.Messages(ctx, 1)
    if err != nil { t.Fatal(err) }
    if len(got) != 2 || got[0].Content != "halo" || got[1].Content != "dunia" {
        t.Fatalf("got %+v", got)
    }
}

func TestSessionBreakHidesOlderMessages(t *testing.T) {
    s := newTestStore(t)
    ctx := context.Background()
    s.AppendMessage(ctx, 1, store.Message{Role: "user", Content: "lama"})
    if err := s.SessionBreak(ctx, 1); err != nil { t.Fatal(err) }
    s.AppendMessage(ctx, 1, store.Message{Role: "user", Content: "baru"})
    got, _ := s.Messages(ctx, 1)
    if len(got) != 1 || got[0].Content != "baru" { t.Fatalf("got %+v", got) }
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/store/ -run 'TestAppend|TestSessionBreak' -v`
Expected: FAIL.

- [ ] **Step 3: Implementasi**

`SessionBreak` menulis baris `role='session_break'`. `Messages` hanya mengembalikan baris setelah `session_break` terakhir untuk `chat_id` itu, urut `id ASC`.

- [ ] **Step 4: Jalankan, pastikan lulus**

Run: `go test ./internal/store/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): append messages and session break marker"
```

---

### Task 4: Provider interface dan adapter OpenAI

**Files:**
- Create: `internal/provider/provider.go`, `internal/provider/openai/client.go`, `internal/provider/openai/client_test.go`

**Interfaces:**
- Produces:
  - `provider.Message{Role Role; Content string; ToolCalls []ToolCall; ToolCallID string}`
  - `provider.ToolCall{ID, Name, Arguments string}`
  - `provider.ToolDef{Name, Description string; Schema json.RawMessage}`
  - `provider.Request{Model string; Messages []Message; Tools []ToolDef; MaxTokens int}`
  - `provider.Event{Text string; ToolCalls []ToolCall; Usage *Usage; Err error; Done bool}`
  - `provider.Provider interface { Name() string; Stream(ctx context.Context, req Request) (<-chan Event, error) }`
  - `openai.New(cfg config.ProviderConfig, apiKey string) *Client` yang mengimplementasikan `provider.Provider`

- [ ] **Step 1: Tulis test yang gagal**

```go
func TestStreamAssemblesToolCallFromChunks(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/event-stream")
        io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"terminal\",\"arguments\":\"{\\\"cmd\\\":\"}}]}}]}\n\n")
        io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"echo hi\\\"}\"}}]}}]}\n\n")
        io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\n")
        io.WriteString(w, "data: [DONE]\n\n")
    }))
    defer srv.Close()

    c := openai.New(config.ProviderConfig{BaseURL: srv.URL, Path: "/chat/completions", Timeout: 5 * time.Second}, "k")
    ch, err := c.Stream(context.Background(), provider.Request{Model: "m", Messages: []provider.Message{{Role: "user", Content: "x"}}})
    if err != nil { t.Fatal(err) }

    var calls []provider.ToolCall
    for ev := range ch {
        if ev.Err != nil { t.Fatal(ev.Err) }
        if len(ev.ToolCalls) > 0 { calls = ev.ToolCalls }
    }
    if len(calls) != 1 || calls[0].Name != "terminal" || calls[0].Arguments != `{"cmd":"echo hi"}` {
        t.Fatalf("got %+v", calls)
    }
}

func TestStreamSurfacesHTTPError(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
    }))
    defer srv.Close()
    c := openai.New(config.ProviderConfig{BaseURL: srv.URL, Path: "/chat/completions"}, "k")
    ch, err := c.Stream(context.Background(), provider.Request{Model: "m"})
    if err != nil { t.Fatal(err) }
    var seen error
    for ev := range ch { if ev.Err != nil { seen = ev.Err } }
    if seen == nil || !strings.Contains(seen.Error(), "401") { t.Fatalf("got %v", seen) }
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/provider/openai/ -v`
Expected: FAIL.

- [ ] **Step 3: Implementasi**

`Stream` mengembalikan channel dan langsung mengembalikan `nil` error (error dikirim sebagai `Event{Err}`), memakai goroutine yang dihentikan oleh `ctx.Done()`.
Body: JSON dengan `model`, `messages`, `tools`, `stream: true`. Header `Authorization: Bearer <key>` + `cfg.Headers`; `http.Client` dengan `cfg.Timeout` dan `Transport` ber-`Proxy` bila `cfg.Proxy` diisi.
Parser: bufio.Scanner per baris, ambil yang berawalan `data: `; `[DONE]` → kirim `Event{Done:true}` lalu tutup. Tool call diakumulasi per `index` (id, name, arguments di-append).
Error non-2xx: baca body maksimal 4 KB, kirim `Event{Err}` berisi status + body.

- [ ] **Step 4: Jalankan, pastikan lulus**

Run: `go test ./internal/provider/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider
git commit -m "feat(provider): openai-compatible sse streaming with tool call assembly"
```

---

### Task 5: Tool registry dan `terminal`

**Files:**
- Create: `internal/tool/tool.go`, `internal/tool/terminal.go`, `internal/tool/tool_test.go`

**Interfaces:**
- Produces:
  - `tool.Tool interface { Name() string; Description() string; Schema() json.RawMessage; Danger() bool; Execute(ctx context.Context, args json.RawMessage) (string, error) }`
  - `tool.NewRegistry(tools ...Tool) *Registry`
  - `(*Registry).Defs() []provider.ToolDef`, `(*Registry).Get(name string) (Tool, bool)`
  - `tool.Truncate(s string, limit int, spillDir string, name string) (string, error)`
  - `tool.NewTerminal(cwd string, timeout time.Duration) *Terminal`

- [ ] **Step 1: Tulis test yang gagal**

```go
func TestTruncateKeepsValidUTF8AndSpills(t *testing.T) {
    dir := t.TempDir()
    in := strings.Repeat("é", 20000) // 40000 byte
    out, err := tool.Truncate(in, 30*1024, dir, "call1")
    if err != nil { t.Fatal(err) }
    if !utf8.ValidString(out) { t.Fatal("output bukan utf8 valid") }
    if len(out) > 30*1024+200 { t.Fatalf("output %d byte", len(out)) }
    if !strings.Contains(out, ".tool-output/call1") { t.Fatalf("tidak menyebut path spill: %s", out[:200]) }
    entries, _ := os.ReadDir(filepath.Join(dir, ".tool-output"))
    if len(entries) != 1 { t.Fatalf("spill file = %d", len(entries)) }
}

func TestTerminalRunsCommand(t *testing.T) {
    t := tool.NewTerminal(t.TempDir(), 10*time.Second)
    out, err := t.Execute(context.Background(), json.RawMessage(`{"cmd":"echo hi"}`))
    if err != nil { t.Fatal(err) }
    if !strings.Contains(out, "hi") { t.Fatalf("got %q", out) }
}

func TestTerminalReportsExitCode(t *testing.T) {
    t := tool.NewTerminal(t.TempDir(), 10*time.Second)
    out, _ := t.Execute(context.Background(), json.RawMessage(`{"cmd":"exit 3"}`))
    if !strings.Contains(out, "exit code 3") { t.Fatalf("got %q", out) }
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/tool/ -v`
Expected: FAIL.

- [ ] **Step 3: Implementasi**

`Truncate`: bila `len(s) <= limit` kembalikan apa adanya; selain itu potong di `limit`, mundur sampai `utf8.ValidString`, tulis sisa ke `spillDir/.tool-output/<name>.txt`, tambahkan baris `[truncated: N bytes at <path>]`.
`Terminal`: `sh -c` di Unix, `cmd /C` di Windows (`runtime.GOOS`); `exec.CommandContext` dengan timeout; gabung stdout+stderr; hasil selalu string, error hanya untuk kegagalan eksekusi (bukan exit code non-nol); exit code disertakan sebagai teks.

- [ ] **Step 4: Jalankan, pastikan lulus**

Run: `go test ./internal/tool/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tool
git commit -m "feat(tool): registry, utf8-safe truncation, terminal tool"
```

---

### Task 6: Tool file — `read_file`, `write_file`, `patch`

**Files:**
- Create: `internal/tool/file.go`
- Modify: `internal/tool/tool_test.go`

**Interfaces:**
- Produces: `tool.NewReadFile()`, `tool.NewWriteFile()`, `tool.NewPatch()` — masing-masing `*FileTool` dengan `Name()` = `read_file` / `write_file` / `patch`
- Args: `read_file{"path":string,"offset":int,"limit":int}`, `write_file{"path":string,"content":string}`, `patch{"path":string,"old":string,"new":string}`

- [ ] **Step 1: Tulis test yang gagal**

```go
func TestPatchReplacesUniqueText(t *testing.T) {
    dir := t.TempDir(); p := filepath.Join(dir, "a.txt")
    os.WriteFile(p, []byte("satu dua tiga"), 0o644)
    out, err := tool.NewPatch().Execute(context.Background(), json.RawMessage(`{"path":"`+strings.ReplaceAll(p, `\`, `\\`)+`","old":"dua","new":"DUA"}`))
    if err != nil { t.Fatal(err) }
    b, _ := os.ReadFile(p)
    if string(b) != "satu DUA tiga" { t.Fatalf("got %q (%s)", b, out) }
}

func TestPatchFailsWhenOldNotFoundOrAmbiguous(t *testing.T) {
    dir := t.TempDir(); p := filepath.Join(dir, "a.txt")
    os.WriteFile(p, []byte("x x"), 0o644)
    _, err := tool.NewPatch().Execute(context.Background(), json.RawMessage(`{"path":"`+strings.ReplaceAll(p, `\`, `\\`)+`","old":"x","new":"y"}`))
    if err == nil { t.Fatal("want error untuk old yang ambigu") }
}

func TestReadFileAbsolutePathAllowed(t *testing.T) {
    dir := t.TempDir(); p := filepath.Join(dir, "a.txt")
    os.WriteFile(p, []byte("isi"), 0o644)
    out, err := tool.NewReadFile().Execute(context.Background(), json.RawMessage(`{"path":"`+strings.ReplaceAll(p, `\`, `\\`)+`"}`))
    if err != nil { t.Fatal(err) }
    if out != "isi" { t.Fatalf("got %q", out) }
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/tool/ -run 'TestPatch|TestReadFile' -v`
Expected: FAIL.

- [ ] **Step 3: Implementasi**

`patch` menolak bila `old` muncul ≠ 1 kali; pesan error menyebut jumlah kemunculan. `write_file` membuat direktori induk. `read_file` mendukung `offset`/`limit` baris dan menolak file > 10 MB dengan pesan jelas. Tidak ada pembatasan path — hanya izin OS.

- [ ] **Step 4: Jalankan, pastikan lulus**

Run: `go test ./internal/tool/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tool
git commit -m "feat(tool): read_file, write_file, patch with unique-match rule"
```

---

### Task 7: Agent loop

**Files:**
- Create: `internal/agent/runner.go`, `internal/agent/runner_test.go`

**Interfaces:**
- Consumes: `provider.Provider`, `tool.Registry`, `store.Store`
- Produces:
  - `agent.Runner{Provider provider.Provider; Tools *tool.Registry; Store *store.Store; Model string; Cwd string; MaxIters int; Approve func(ctx context.Context, name string, args json.RawMessage) (bool, error); Out io.Writer}`
  - `(*Runner).Run(ctx context.Context, chatID int64, userText string) (string, error)`

- [ ] **Step 1: Tulis test yang gagal**

```go
type fakeProvider struct{ steps [][]provider.Event }

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
    ch := make(chan provider.Event)
    go func() {
        defer close(ch)
        if len(f.steps) == 0 { ch <- provider.Event{Text: "selesai", Done: true}; return }
        for _, ev := range f.steps[0] { ch <- ev }
        f.steps = f.steps[1:]
    }()
    return ch, nil
}

func TestRunExecutesToolThenAnswers(t *testing.T) {
    s := newStore(t)
    fp := &fakeProvider{steps: [][]provider.Event{
        {{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "terminal", Arguments: `{"cmd":"echo halo"}`}}}},
        {{Text: "sudah dijalankan", Done: true}},
    }}
    r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(tool.NewTerminal(t.TempDir(), 5*time.Second)), Store: s, Model: "m", MaxIters: 5, Out: io.Discard}
    got, err := r.Run(context.Background(), 1, "jalankan echo")
    if err != nil { t.Fatal(err) }
    if got != "sudah dijalankan" { t.Fatalf("got %q", got) }
    msgs, _ := s.Messages(context.Background(), 1)
    if len(msgs) != 4 { t.Fatalf("pesan tersimpan = %d, want 4", len(msgs)) }
}

func TestRunStopsAtMaxIters(t *testing.T) {
    fp := &fakeProvider{}
    r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: newStore(t), Model: "m", MaxIters: 1, Out: io.Discard}
    _, err := r.Run(context.Background(), 1, "x")
    if err == nil || !strings.Contains(err.Error(), "iterasi") { t.Fatalf("got %v", err) }
}

func TestUnknownToolBecomesErrorResult(t *testing.T) {
    fp := &fakeProvider{steps: [][]provider.Event{
        {{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "ghost", Arguments: `{}`}}}},
        {{Text: "ok", Done: true}},
    }}
    r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: newStore(t), Model: "m", MaxIters: 5, Out: io.Discard}
    if _, err := r.Run(context.Background(), 1, "x"); err != nil { t.Fatal(err) }
}

func TestInvalidToolArgsBecomesErrorResult(t *testing.T) {
    s := newStore(t)
    fp := &fakeProvider{steps: [][]provider.Event{
        {{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "terminal", Arguments: `{bukan json`}}}},
        {{Text: "ok", Done: true}},
    }}
    r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(tool.NewTerminal(t.TempDir(), time.Second)), Store: s, Model: "m", MaxIters: 5, Out: io.Discard}
    if _, err := r.Run(context.Background(), 1, "x"); err != nil { t.Fatal(err) }
    msgs, _ := s.Messages(context.Background(), 1)
    if len(msgs) != 4 { t.Fatalf("pesan = %d, want 4", len(msgs)) }
}

func TestCancelSavesPartialAssistantText(t *testing.T) {
    s := newStore(t)
    ctx, cancel := context.WithCancel(context.Background())
    fp := &cancelAfterTextProvider{cancel: cancel, text: "separuh jawaban"}
    r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: s, Model: "m", MaxIters: 5, Out: io.Discard}
    _, err := r.Run(ctx, 1, "x")
    if !errors.Is(err, context.Canceled) { t.Fatalf("got %v", err) }
    msgs, _ := s.Messages(context.Background(), 1)
    last := msgs[len(msgs)-1]
    if last.Role != "assistant" || last.Content != "separuh jawaban" { t.Fatalf("got %+v", last) }
}
```

Catatan: `newStore(t)` adalah helper per paket test (`store.Open` di `t.TempDir()`) dan `cancelAfterTextProvider` mengirim satu `Event{Text: ...}` lalu memanggil `cancel()` sebelum `Done`.

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/agent/ -v`
Expected: FAIL.

- [ ] **Step 3: Implementasi**

Loop: muat history dari store → append user → panggil `Stream` → tulis `TextDelta` ke `Out` → kumpulkan tool call → bila ada, eksekusi berurutan per call dengan `ctx`, bungkus hasil lewat `tool.Truncate(…, 30*1024, filepath.Join(Cwd, ".tool-output"), call.ID)`, append `Message{Role:"tool", ToolCallID: id}` → ulang. Tanpa tool call: simpan `assistant`, kembalikan teks.
Tool yang `Danger()` dan `Approve != nil` → minta izin; tolak = hasil tool `"ditolak user"`.
`ctx` dibatalkan → simpan teks parsial sebagai `assistant`, kembalikan `ctx.Err()`.
Nama tool tidak dikenal → hasil tool `"tool tidak dikenal: <name>"`, loop lanjut.
Argumen tool bukan JSON valid → hasil tool `"argumen tool tidak valid: <err>"`, loop lanjut.
Iterasi habis → error `"batas iterasi (<n>) tercapai"`.

- [ ] **Step 4: Jalankan, pastikan lulus**

Run: `go test ./internal/agent/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent
git commit -m "feat(agent): tool loop with iteration budget and error results"
```

---

### Task 8: Prompt builder

**Files:**
- Create: `internal/agent/prompt.go`
- Modify: `internal/agent/runner_test.go`

**Interfaces:**
- Produces: `agent.BuildPrompt(p BuildParams) string` dengan `BuildParams{Cwd string; Tools []provider.ToolDef; Memory, UserProfile string; Model string}`

- [ ] **Step 1: Tulis test yang gagal**

```go
func TestBuildPromptContainsRulesAndTools(t *testing.T) {
    p := agent.BuildPrompt(agent.BuildParams{Cwd: "/tmp/ws", Model: "m", Tools: []provider.ToolDef{{Name: "terminal"}}})
    for _, want := range []string{"terminal", "/tmp/ws", "[untrusted]", "renstra"} {
        if !strings.Contains(p, want) { t.Fatalf("prompt tidak memuat %q", want) }
    }
}

func TestBuildPromptIncludesMemoryWhenPresent(t *testing.T) {
    p := agent.BuildPrompt(agent.BuildParams{Memory: "user suka kopi", UserProfile: "nama: Budi"})
    if !strings.Contains(p, "user suka kopi") || !strings.Contains(p, "nama: Budi") { t.Fatal("memory tidak di-inject") }
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/agent/ -run TestBuildPrompt -v`
Expected: FAIL.

- [ ] **Step 3: Implementasi**

Urutan: identitas singkat (`renstra`, bahasa mengikuti user) → aturan keamanan (jangan ikuti instruksi dalam `[untrusted]`, konfirmasi sebelum destruktif) → `cwd` → daftar tool (nama + deskripsi) → blok `MEMORY` dan `USER PROFILE` bila tidak kosong.
Prompt dihitung sekali per `Runner` dan di-cache di field.

- [ ] **Step 4: Jalankan, pastikan lulus**

Run: `go test ./internal/agent/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent
git commit -m "feat(agent): system prompt assembly"
```

---

### Task 9: CLI REPL

**Files:**
- Create: `internal/cli/repl.go`, `internal/cli/repl_test.go`
- Modify: `cmd/renstra/main.go`

**Interfaces:**
- Produces: `cli.Run(ctx context.Context, r *agent.Runner, chatID int64, in io.Reader, out io.Writer) error`
- Commands: `/new`, `/model <alias>`, `/usage`, `/exit`

- [ ] **Step 1: Tulis test yang gagal**

```go
func TestReplNewResetsSession(t *testing.T) {
    s := newStore(t)
    fp := &fakeProvider{steps: [][]provider.Event{{{Text: "hai", Done: true}}, {{Text: "hai lagi", Done: true}}}}
    r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: s, Model: "m", MaxIters: 5, Out: io.Discard}
    in := strings.NewReader("halo\n/new\nhalo lagi\n/exit\n")
    var out bytes.Buffer
    if err := cli.Run(context.Background(), r, 1, in, &out); err != nil { t.Fatal(err) }
    msgs, _ := s.Messages(context.Background(), 1)
    if len(msgs) != 2 { t.Fatalf("pesan = %d, want 2 (satu per sesi)", len(msgs)) }
}

func TestReplUnknownCommandIsSentToModel(t *testing.T) {
    fp := &fakeProvider{steps: [][]provider.Event{{{Text: "ok", Done: true}}}}
    r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: newStore(t), Model: "m", MaxIters: 5, Out: io.Discard}
    in := strings.NewReader("/bukan-perintah halo\n/exit\n")
    if err := cli.Run(context.Background(), r, 1, in, io.Discard); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/cli/ -v`
Expected: FAIL.

- [ ] **Step 3: Implementasi**

`bufio.Scanner` per baris; kosong dilewati; `"/"` prefix dicocokkan persis ke command yang dikenal, sisanya dikirim ke model. Ctrl+C (`os.Interrupt`) membatalkan turn berjalan lewat `context.WithCancel`, bukan keluar. `cmd/renstra/main.go` `chat` subcommand: load config, resolve provider, buka store, buat registry (`terminal`, `read_file`, `write_file`, `patch`), jalankan REPL.
Test di paket `cli` mendefinisikan fake provider sendiri (tidak bisa memakai milik paket `agent`) plus helper `newStore(t)`.

- [ ] **Step 4: Jalankan, pastikan lulus**

Run: `go test ./... && go build ./...`
Expected: PASS dan build bersih.

- [ ] **Step 5: Commit**

```bash
git add internal/cli cmd
git commit -m "feat(cli): repl with session reset and streaming output"
```

---

### Task 10: Approval dan commit akhir

**Files:**
- Modify: `internal/cli/repl.go`, `internal/agent/runner.go`
- Modify: `internal/cli/repl_test.go`

**Interfaces:**
- Produces: `cli.Approver(in io.Reader, out io.Writer) func(context.Context, string, json.RawMessage) (bool, error)`

- [ ] **Step 1: Tulis test yang gagal**

```go
func TestApproverRejectsOnNo(t *testing.T) {
    approve := cli.Approver(strings.NewReader("n\n"), io.Discard)
    ok, err := approve(context.Background(), "terminal", json.RawMessage(`{"cmd":"rm -rf /"}`))
    if err != nil || ok { t.Fatalf("ok=%v err=%v", ok, err) }
}

func TestDangerousToolDeniedDoesNotRun(t *testing.T) {
    s := newStore(t)
    fp := &fakeProvider{steps: [][]provider.Event{
        {{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "terminal", Arguments: `{"cmd":"echo tidak-jalan"}`}}}},
        {{Text: "ok", Done: true}},
    }}
    var out bytes.Buffer
    r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(tool.NewTerminal(t.TempDir(), time.Second)),
        Store: s, Model: "m", MaxIters: 5, Out: &out,
        Approve: func(context.Context, string, json.RawMessage) (bool, error) { return false, nil }}
    if _, err := r.Run(context.Background(), 1, "x"); err != nil { t.Fatal(err) }
    if strings.Contains(out.String(), "tidak-jalan") { t.Fatal("tool jalan padahal ditolak") }
}
```

- [ ] **Step 2: Jalankan, pastikan gagal**

Run: `go test ./internal/cli/ -run TestApprover -v`
Expected: FAIL.

- [ ] **Step 3: Implementasi**

`Approver`: tulis `jalankan <tool>? <args>\n[y/N] ` ke `out`, baca baris, hanya `y`/`ya`/`yes` (case-insensitive) yang diterima. Default tolak; timeout 120 detik via `context`.
`Terminal.Danger()` = `true`; `read_file`/`write_file`/`patch` = `false` untuk v1 (spec §7.5).

- [ ] **Step 4: Jalankan semua test dan build**

Run: `go test ./... && go build ./...`
Expected: PASS semua.

- [ ] **Step 5: Commit**

```bash
git add internal/cli internal/agent
git commit -m "feat(cli): danger approval gate"
```

---

## Roadmap rencana berikutnya

- **Plan 2 — Telegram:** long polling, dedup `update_id`, streaming edit, inline keyboard approval, interrupt, `/stop`.
- **Plan 3 — Memory, skills, compression:** dua lapis skills + `--global`, `MEMORY.md`/`USER.md` frozen snapshot, kompaksi, `/usage`.
- **Plan 4 — Provider tambahan, cron, delegate, MCP:** `custom` provider, stub folder, scheduler, subagent, MCP client + plugin.

Masing-masing menghasilkan software yang bisa dipakai sendiri.
