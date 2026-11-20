# AGENTS.md — Panduan Developer & AI Agent untuk RENSTRA

Dokumen ini adalah referensi utama bagi developer maupun coding agent yang bekerja pada repositori **RENSTRA** — agent AI self-hosted berbahasa Go (terinspirasi dari Hermes Agent) dengan fokus utama pada gateway Telegram, zero-CGO (`CGO_ENABLED=0`), dan single binary.

---

## 1. Visi & Prinsip Inti

- **Single Binary, Zero-CGO:** Dibangun dengan Go murni (`modernc.org/sqlite`, tanpa `mattn/go-sqlite3` atau `gcc`) agar mudah di-cross-compile ke `linux/amd64` dan `linux/arm64` dari Windows/macOS/Linux dan ringan di VPS kecil.
- **Telegram-First Gateway + CLI Fallback:** Pengalaman utama berjalan via long-polling Telegram (`renstra serve`), lengkap dengan live streaming edit, status tool, approval inline keyboard, interrupt turn, unduh lampiran, dan slash commands in-chat. Mode terminal interaktif tersedia via `renstra chat`.
- **Paritas Perilaku Hermes:** Memori persisten (`MEMORY.md` & `USER.md`) dengan snapshot beku, 4 lapis `SKILL.md` yang dapat ditulis sendiri oleh agent, kompaksi otomatis + `session_search`, failover multi-provider, cron scheduler, subagent `delegate`, dan integrasi ekstensi melalui MCP.
- **Satu Instance = Satu Profil:** Isolasi antar pengguna atau bot berbeda dicapai dengan memisahkan `RENSTRA_HOME`, bukan multi-tenant rumit dalam satu proses.

---

## 2. Peta Arsitektur & Paket

```text
cmd/renstra/
  main.go          # Entrypoint CLI (setup, serve, chat, model, skill, mcp, usage, config, version)
  commands.go      # serveCmd (Telegram + cron + MCP + slog), telegramHandler, skill/mcp/usage/model subcommands
  setup.go         # Wizard interaktif `renstra setup` untuk generate config.yaml

internal/agent/
  runner.go        # Core agent loop, budget/limits, turn tracking, loop detection, hooks, RunSubagent (depth), Vision, slog
  prompt.go        # BuildPrompt: rakit system prompt dari prefix, rules, tools, skill index, memory
  compress.go      # EstimateTokens, ShouldCompact, CompactHistory (tanpa memutus pasangan tool_calls)
  summarize.go     # Ringkas history lama memakai AuxModel dengan fallbackSummary deterministik
  fallback.go      # streamWithFallback: chain provider utama + fallbacks, prompted tools (`tools: false`), stream ke Out & StreamWriter

internal/telegram/
  bot.go           # Bot API client (long polling, dedup updates_seen, lockChat + interrupt, typing, SetMyCommands,
                   # DownloadFile + voice/audio, SendDocument/SendPhoto/SendFile, AskApproval + edit keputusan,
                   # Bot API 10.4 sendRichMessageDraft/sendRichMessage + stopped_message_generation, StreamWriter)
  format.go        # markdownToHTML konversi aman Markdown -> Telegram HTML
  message.go       # SplitMessage (batas 4096 / 32768 rune), command parser, callback parser, helpText

internal/provider/
  provider.go      # Interface Provider, Message (Attachments), ToolCall, ToolDef, Usage, Request, Event
  retry.go         # Retryable (429/5xx), Backoff eksponensial + jitter, hormati Retry-After
  openai/          # Adapter OpenAI Chat Completions, vision image_url & endpoint OpenAI-compatible (aktif)
  anthropic/       # Adapter Anthropic Messages API & vision base64 (aktif)
  google/          # Adapter Google Gemini streamGenerateContent SSE & vision inlineData (aktif)
  openrouter/      # Adapter OpenRouter (OpenAI-compatible, aktif)
  groq/            # Adapter Groq (OpenAI-compatible, aktif)
  ollama/          # Adapter Ollama lokal (OpenAI-compatible, aktif)
  mistral/         # Adapter Mistral (OpenAI-compatible, aktif)
  xai/             # Adapter xAI Grok (OpenAI-compatible, aktif)

internal/tool/
  tool.go          # Interface Tool, Registry (+ Clone), WithChatID/ChatIDFrom, Truncate (spill 30 KB ke .tool-output)
  terminal.go      # Eksekusi shell (cmd /C di Windows, sh -c di Unix), cap output 64 KB, allowlist env
  file.go          # read_file, write_file (atomik), patch (unik 1x replace)
  webfetch.go      # web_fetch HTTP GET + konversi HTML ke teks polos bertanda [untrusted]
  websearch.go     # web_search DuckDuckGo HTML (zero-API-key) bertanda [untrusted]
  session_search.go# session_search query histori percakapan lama di SQLite (termasuk yang terkompaksi)
  send_file.go     # send_file kirim berkas/foto balik ke chat Telegram
  agent_tools.go   # Tool `memory` (read|write) dan tool `skill` (list|load|save|create|update|delete)
  cron.go          # Tool `cron` (list|add|remove|toggle) dengan validasi Cron & context ChatID
  delegate.go      # Tool `delegate` untuk mendelegasikan sub-tugas ke subagent terisolasi (depth tracked)

internal/store/
  store.go         # SQLite opener (modernc.org/sqlite), WAL pragmas, SetMaxOpenConns(1), migrasi kv
  schema.sql       # Skema v1: users, chats, messages, turns, jobs, usage_daily, updates_seen, kv
  messages.go      # AppendMessage, Messages, Compact, SessionBreak, SearchMessages
  helpers.go       # updates_seen dedup, usage_daily, jobs CRUD, StartTurn/FinishTurn/RecoverInterruptedTurns, SetChatModel/GetChatModel

internal/skill/
  skill.go         # Registry 4 lapis SKILL.md, prioritas lapis atas, tracking konflik, Reload(), Create/Update/Delete

internal/memory/
  memory.go        # Store MEMORY.md (2200 rune) & USER.md (1375 rune), Snapshot(), atomic write

internal/migrate/
  hermes.go        # Impor data dari Hermes Agent (~/.hermes) ke $RENSTRA_HOME (`renstra migrate hermes`)

internal/update/
  update.go        # Self-updater in-place dari GitHub releases (`renstra update`), cache check, multi-OS binary replacement

internal/cron/
  cron.go          # Parser cron 5-field + @hourly/@daily/@weekly, NextAfter, Run ticker tanpa backfill

internal/mcp/
  client.go        # Client MCP JSON-RPC 2.0 via stdio (auto-reconnect) & HTTP/SSE, handshake, inject tool mcp__<server>__<tool>

internal/config/
  config.go        # Loader config.yaml, Duration, ApprovalConfig, Limits, ToolsConfig, Resolve alias/provider

internal/cli/
  repl.go          # REPL terminal `renstra chat` dengan Ctrl+C cancel turn
  approve.go       # Approver y/N di terminal
```

---

## 3. Aturan Ketergantungan (Dependency Graph)

1. **Larangan Siklus Import:** Paket di dalam `internal/*` **tidak boleh** mengimpor `internal/agent`.
   - Urutan ketergantungan searah:
     - `provider`, `config`, `memory`, `skill` -> hanya stdlib (+ `gopkg.in/yaml.v3` untuk `config`/`skill`).
     - `store` -> `provider`, `modernc.org/sqlite`.
     - `cron` -> `store`.
     - `tool` -> `provider`, `store`, `memory`, `skill`, `cron`.
     - `mcp` -> `tool`.
     - `telegram` -> stdlib saja.
     - `agent` -> `config`, `memory`, `provider`, `skill`, `store`, `tool`.
     - `cli` -> `agent`.
     - `cmd/renstra` -> merangkai seluruh paket di atas.
2. **Zero-CGO Wajib Terjaga:** Jangan menambahkan dependency yang membutuhkan CGO atau binding C.

---

## 4. Invarian Kritis (Wajib Dijaga Saat Mengubah Kode)

1. **Pasangan `assistant(tool_calls)` & `tool(tool_call_id)` Tidak Boleh Pincang:**
   - Provider OpenAI dan Anthropic menolak request bila pesan `assistant` memiliki `tool_calls` tanpa pesan `tool` ber-ID cocok setelahnya.
   - Saat konteks dibatalkan di tengah eksekusi banyak tool, `Runner.fillCancelledTools` **wajib** menulis hasil sintetik untuk sisa tool call yang belum dijalankan.
   - Saat kompaksi (`CompactHistory` dan `Store.Compact`), titik potong **wajib** mundur hingga mendarat pada pesan `RoleUser` agar grup `tool_calls` tidak terbelah.
2. **Satu Koneksi Tulis SQLite (`SetMaxOpenConns(1)`):**
   - `modernc.org/sqlite` menggunakan `SetMaxOpenConns(1)` dengan `WAL`, `busy_timeout(5000)`, dan `foreign_keys(1)`. Jangan ubah pool tanpa memisahkan koneksi read-only eksplisit.
3. **Save Context Terpisah Saat Shutdown / Cancel:**
   - Operasi penulisan status saat turn dibatalkan (`savePartial`, `fillCancelledTools`, `appendWithFallback`, `FinishTurn`, `recordUsageWithPrices`) **wajib** memakai context baru dengan `saveTimeout`, bukan `ctx` turn yang mungkin sudah `context.Canceled` atau `DeadlineExceeded`.
4. **Penandaan Konten Tidak Tepercaya (`[untrusted]`):**
   - Hasil dari `web_fetch`, `web_search`, dan tool MCP (`mcp__*`) **wajib** dibungkus `[untrusted]...[/untrusted]` untuk memitigasi prompt injection.
5. **Isolasi Env Subprocess `terminal`:**
   - `tool.Terminal` hanya meneruskan variabel di `defaultEnvKeys` (+ konfigurasi eksplisit). Jangan mewariskan seluruh `os.Environ()` karena API key provider (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, dll.) dan token tidak boleh bocor ke perintah shell.
6. **Cron Unattended Safety:**
   - Pemanggilan `runner.Run` dari `cron.Run` **wajib** dibungkus `agent.WithUnattended(cctx)` agar tool `Danger() == true` yang tidak pre-approved oleh `ApprovalConfig` langsung ditolak tanpa menunggu timeout inline keyboard 120 detik.
7. **Thread-Safety pada `agent.Runner`:**
   - Gunakan `Runner.SetModel`, `Runner.GetModel`, dan `Runner.primaryTarget()` (dilindungi `r.mu`) saat membaca atau mengganti model/provider secara runtime (mis. `/model` di Telegram).
   - Pertahankan `tool.ChatIDFrom(ctx)` di `Runner.Run` saat `RunSubagent` berjalan dengan `subID` sintetis, agar approval dan tool `cron`/`session_search` tetap mengetahui `chatID` asli.
8. **Dedup Update Telegram & Urutan Eksekusi Per Chat:**
   - `Bot.handleUpdate` mencatat `update_id` ke `seen` dan `OnSeen` (disimpan ke SQLite `updates_seen`) sebelum memproses pesan.
   - `Bot.lockChat` membatalkan turn berjalan (`cancels[chatID]`) dan menserialisasikan giliran per chat; `/stop`, `/help`, `/start` diproses di luar `lockChat` agar tetap responsif saat chat sibuk.

---

## 5. Panduan Ekstensi Cepat (How-To)

### A. Menambah Tool Baru
1. Buat struct di `internal/tool/` yang mengimplementasikan interface `tool.Tool`:
   - `Name() string`
   - `Description() string`
   - `Schema() json.RawMessage`
   - `Danger() bool` (set `true` bila butuh approval user)
   - `Execute(ctx context.Context, args json.RawMessage) (string, error)`
2. Tambahkan flag toggle `*bool` di `ToolsConfig` serta `switch` di `IsEnabled()` pada `internal/config/config.go`, lalu tampilkan default-nya di `cmd/renstra/setup.go`.
3. Daftarkan instance tool di `serveCmd()` (`cmd/renstra/commands.go`) dan `chatCmd()` (`cmd/renstra/main.go`).
4. Tambahkan unit test di `internal/tool/`.

### B. Mengaktifkan Provider Stub (`openrouter`, `groq`, `ollama`, `mistral`, `xai`, `google`)
1. Untuk provider OpenAI-compatible (`openrouter`, `groq`, `ollama`, `mistral`, `xai`): arahkan `New(cfg, apiKey)` di package masing-masing ke `openai.New(cfg, apiKey)` dengan mengisi default `BaseURL` dan `Path` (`/chat/completions`) bila kosong.
2. Untuk `google` (Gemini): implementasikan client streaming yang memetakan `provider.Request`/`provider.Event` ke API Gemini.
3. Daftarkan `case "<kind>":` di `buildProvider()` pada `cmd/renstra/commands.go` dan perbarui output `modelListCmd()`.

### C. Menambah Slash Command Telegram Baru
1. Tambahkan case perintah di `switch cmd` dalam `telegramHandler.Handle` di `cmd/renstra/commands.go`.
2. Jika perintah boleh memutus antrean atau membatalkan proses saat chat sibuk, tangani di `Bot.handleMessage` (`internal/telegram/bot.go`) sebelum `lockChat`.
3. Perbarui `helpText` di `internal/telegram/message.go`.
4. Tambahkan unit test di `cmd/renstra/commands_test.go`.

---

## 6. Perintah Build, Test & Verifikasi

Jalankan perintah berikut dari root repositori sebelum menyelesaikan perubahan apa pun:

```powershell
# Jalankan seluruh unit test tanpa cache
go test -count=1 ./...

# Analisis statis
go vet ./...

# Build binary lokal
go build -o renstra.exe ./cmd/renstra

# Cross-compile untuk VPS Linux amd64 / arm64 (zero-CGO)
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="amd64"; go build -o renstra-linux-amd64 ./cmd/renstra
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="arm64"; go build -o renstra-linux-arm64 ./cmd/renstra
```

> **Catatan Windows:** Jangan gunakan flag `-race` di environment yang tidak memiliki compiler `gcc` 64-bit terkonfigurasi (`CGO_ENABLED=0` adalah target utama proyek ini).

---

## 7. Status Rilis & Pengembangan (v1.2.0)

Fitur v1.2.0 telah diimplementasikan:
1. **Pesan Awal Telegram `/start` & `/help`:** Diformat rapi dan elegan sesuai pembaruan Telegram Bot API 10.4.
2. **Modernisasi Chat UI CLI (`renstra chat`):** Tampilan welcome banner informatif (versi, provider asli, model, context window, skills count), prompt visual interaktif (`╭─ User` dan `╭─ RENSTRA (<model>)`).
3. **In-Place Self-Updater (`renstra update`):** Pembaruan otomatis binary langsung dari GitHub Releases (`firdausmntp/RENSTRA`) dengan progress bar dan replacement binary yang aman di Linux, macOS, dan Windows.
4. **Notifikasi Rilis Baru:** Deteksi non-blocking versi rilis baru dengan cache TTL 12 jam.
5. **One-Line Bash Installer:** Script [`install.sh`](install.sh) via `curl -fsSL https://raw.githubusercontent.com/firdausmntp/RENSTRA/main/install.sh | bash` untuk instalasi cepat di VPS (Ubuntu, Debian, Armbian, CentOS, macOS).
6. **Error Formatter Rapi & Elegan:** Parsing HTTP status code (401, 403, 404, 429, 500), URL provider, dan JSON body menjadi pesan Telegram (dengan `<blockquote>` Bot API 10.4) dan ANSI boxed panel di CLI beserta tips solusi langkah demi langkah.
7. **Peningkatan Setup & Auto-Load `.env`:** Wizard `renstra setup` memungkinkan input API Key langsung ke `$RENSTRA_HOME/.env` (mode `0600`), dan `config.Load()` memuat file `.env` secara otomatis.
Lihat daftar perbaikan lengkap di [TODO.md](TODO.md).
