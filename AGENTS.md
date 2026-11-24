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

## 7. Status Rilis & Pengembangan (v1.3.5)

Fitur & pembaruan v1.3.5:
1. **Hybrid BM25 + Trigram Semantic Search (`internal/store/search.go`):** Pencarian histori percakapan (`session_search`) menggunakan ranking BM25 + Character Trigram Jaccard + pencocokan prefix/morfologi serta opsi `all_chats: true`.
2. **Web Chat Playground (`/chat`), MCP Studio & Multi-Model per Provider (`internal/dashboard/api_chat.go`):** Uji chat/command langsung dari browser (`POST /api/chat`), kelola `mcp.yaml` dari Web Dashboard, serta tambah banyak Model ID sekaligus (dipisah koma) yang dikelompokkan per provider.
3. **Safe Backup & Restore (`.tar.gz`), Historical Topic Harvester & `react_message` (`internal/store/backup.go`):** `renstra backup` / `GET /api/backup` (`VACUUM INTO` SQLite) & `renstra restore`, auto-harvest topik forum lama dari histori SQLite, serta reaksi emoji pesan (`react_message`).

Fitur v1.3.2 sebelumnya:
1. **Tool Native `telegram_manage` & Persistensi Topik SQLite:** Kelola topik forum (`list_topics`, `create_topic`, `rename_topic`, `close_topic`, `reopen_topic`, `delete_topic`) dan pesan (`delete_message`, `edit_message`, `pin_message`, `unpin_message`), persistensi `tg_topics:<chatID>` di SQLite, `send_telegram` mengembalikan `message_id`, serta injeksi `msg_id`/`reply_msg_id` pada konteks pesan masuk.
2. **Dashboard UI Sync Fallback, `/favicon.ico` & 1-Line Logs:** Auto-fallback `main` -> `feat/core-agent` di `SyncUI`, `/favicon.ico` menyajikan `logo.svg`, serta log row 1-line responsif.

Fitur v1.3.1 sebelumnya:
1. **3-Layer Hybrid UI Loader & GitHub Hot-Sync (`internal/dashboard/uisync.go`):** Resolusi UI berlapis (`override` -> `cache` -> `embed`) dengan pengecekan kompatibilitas `manifest.json` (`min_api_version`/`max_api_version`), tombol `Sync UI` (`POST /api/ui/sync` & `POST /api/ui/reset`), dan `Cache-Control: no-cache`.
2. **Light Theme Default, Logs Beautifier, Skills Pagination & Desktop Drawer Fix:** Tema putih bersih sebagai default + toggle Dark, chip log terstruktur tanpa `error=""` kosong, paginasi Skills `12/halaman` + filter status, serta penyembunyian tombol hamburger/drawer di desktop.
3. **Akurasi Model, Context Window >32k & Vision Model Kustom:** Fallback `DefaultModelConfig` dinaikkan ke `128k` + `Vision: true` (`1M` untuk `gemini-3/4`, `200k` untuk `claude-*`), pewarisan `vision` saat tidak ditulis di `config.yaml` (`ModelConfig.UnmarshalYAML`), deduplikasi + `model_details` di tab Accounts & Models, serta sinkronisasi live global model dari dashboard.

Fitur v1.3.0 sebelumnya:
1. **Web Dashboard Control Plane & Live Log Hub (`internal/dashboard`):** Dashboard web single-binary (`embed`) dengan port & bearer token konfigurabel (`dashboard.*` / `--dashboard-port` / `renstra dashboard`), SPA routes (`/overview`, `/logs`, `/skills`, `/memory`, `/accounts`, `/cron`), full CRUD (Skills Studio, `SOUL.md`/`MEMORY.md`/`USER.md`, Multi-Akun & Model, Cron, Turns, Transkrip Sesi), Live SSE Logs + Download `.jsonl`, serta desain modular responsif Desktop/Tablet/Mobile.
2. **`/model <id> --global`:** Mengubah `default_model` sekaligus menyelaraskan seluruh sesi chat/grup/topik aktif di memori dan SQLite (`SetGlobalModel`).
3. **Antrean Pesan Coalesced Saat Thinking (Hermes-Style):** Pesan baru yang masuk saat bot sedang berpikir/menjalankan tool di Telegram otomatis diantrekan dan digabung (`enqueueOrAcquire`) tanpa membatalkan tugas berjalan; `/stop` tetap membatalkan secara instan.

Fitur v1.2.8 sebelumnya:

Fitur & perbaikan v1.2.8:
1. **Anti-Lone-`>` Streaming & Fallback Finalize:** Menahan flush token awal yang hanya berisi simbol/prefix markdown (seperti `>` tunggal pada blockquote) hingga ada konten bermakna (`hasMeaningfulStreamText`), serta fallback `SendMessage` jika `EditMessage` saat `Finalize` gagal.
2. **Tool Native `send_telegram` Cross-Topic:** Mengirim pesan terformat Markdown -> Telegram HTML (`<blockquote>`, bold, code) ke `chat_id` & `topic_id` mana pun melalui [`Bot.SendToTopic`](internal/telegram/reply.go).
3. **Retry Attempt Respons AI Kosong:** `streamWithFallback` otomatis mencoba ulang hingga 2x retry pada model yang sama bila respons kosong atau error transient tanpa output sebelum berpindah ke fallback.
4. **Progress Tool Informatif & Diagnosis Timeout Akurat:** Menampilkan ringkasan argumen & durasi berjalan (`⏳ Menjalankan terminal: \`...\` (15s)`), memindahkan HTTP timeout OpenAI ke `ResponseHeaderTimeout`, melacak fase aktif di `Runner.Run`, serta membedakan Turn/Tool Timeout vs Provider Timeout di `FormatError` & `FormatCLIError`.

Fitur v1.2.7 sebelumnya:

Fitur v1.2.7 telah diimplementasikan:
1. **Dukungan Group, Supergroup & Forum Topics Relevan (`/group`):** Menghapus pembatasan `"DM saya langsung"`, mendukung otorisasi grup (`allowed_users`/`allowed_chats` & `From.ID`), filter `allowed_topics`, mode pemicu `group_mode` (`always`/`mention`), menu in-chat `/group` (termasuk `createForumTopic` & `editForumTopic`), injeksi konteks `[Grup | Topik | Pengirim]`, serta isolasi sesi & eksekusi turn paralel antar-topik (`SessionChatID` & `lockScope`).
2. **Core Telegram Bot API 10.5:** Implementasi `BotFullInfo` pada `GetMe`, `SetMySettings` (`setMySettings` untuk konfigurasi grup, privasi, topik, `has_topic_sidebar`, dan `callback_query_mode`), `WriteAccessAllowed.Inviter`, `CallbackQuery.InlineMessageID`, serta `RichBlockExpandableQuote` berbasis `blocks`.
3. **Default System `SOUL.md` (Zero Refusal & Action-First), Per-Topic `/persona`, Fleksibilitas `/memory` & `/undo`:** Berkas sistem `$RENSTRA_HOME/memory/SOUL.md` otomatis terisi `DefaultSoul` (`ZERO REFUSAL` & langsung eksekusi tool tanpa menolak/menunda) lengkap dengan `/soul`, `/persona` terisolasi per topik/grup, `/memory set|user|soul|here|del|limit` & `/remember user:|soul:|here:`, `/undo` checkpoint rollback, serta perbaikan approval grup (`cid != 0`).
4. **Manajemen Multi-Akun (`/account` & `renstra account`), `/model add|rm`, `/context`, Migrasi Penuh Hermes & Footer Update Versi:** Kelola banyak akun provider & API key `.env` secara live (`/account`), tambah/hapus model langsung (`/model add <name> <prov> [ctx]` & `/model rm`), auto-deteksi & auto-scaling context window (`128k`/`200k`/`1M` + `/context`), migrasi penuh `.env` + multi-provider dari Hermes (`renstra migrate hermes`), format CLI helper standar (`Usage:`, `Commands:`, `Flags:`, `Environment:`, `Examples:`), serta footer otomatis saat ada update versi baru di CLI dan pesan Telegram.

Fitur v1.2.6 sebelumnya:
1. **Perbaikan SSE Streaming & Empty Response Guard:** Melewati `data:` keepalive kosong, buffering payload SSE multi-baris di provider OpenAI/Anthropic/Google, fallback JSON non-SSE pada HTTP 200, dan validasi respons kosong di `Runner`.
2. **Dukungan Sub-Message, Reply, Quote & Thread Telegram:** Context injection untuk `reply_to_message` & `quote` (termasuk lampiran media) serta pengiriman balik `message_thread_id` dan `reply_parameters` across text, rich messages, media, keyboard, dan approval.
3. **Fitur Baru (`/remember`, `/export`, Menu `/memory`, `/fallback`, Markdown Heading, CLI & Navigasi):** Penyimpanan cepat ke `MEMORY.md`/`USER.md`, export transkrip sesi ke `.md`, menu `/memory` & `/fallback` (Smart Fallback dengan circuit-breaker, context/vision routing, dan auto-discovery), heading `#` menjadi `<b>`, `renstra model set`, aktivasi VT ANSI Windows, serta tombol `⬅️ Menu` di seluruh submenu Telegram.

Fitur v1.2.5 sebelumnya:
1. **CLI Lebih Berwarna & Animated Loading Spinner:** `cliStreamWriter` menampilkan spinner animasi (`⠋⠙⠹...`) dan pesan status (`Menghubungi model...`, `Menjalankan <tool>...`), indikator konteks berwarna (hijau/kuning/merah), serta pause/resume otomatis saat konfirmasi approval CLI.
2. **Approval Mode & Auto-Approve di Telegram & CLI (`/approval`):** Mendukung pergantian mode approval per chat (`prompt`, `auto`, `deny`) yang persisten di SQLite (`kv`), lengkap dengan tombol cepat `⚡ Auto-Approve Chat Ini` pada keyboard inline Telegram.
3. **Desain Ulang Pesan Approval Telegram:** Pesan konfirmasi tool kini dirender dengan format HTML rapi (ekstraksi perintah `terminal` ke blok `<pre><code class="language-bash">`, pretty-print JSON untuk tool lain, badge status `✅`/`❌`/`⚡`/`⌛`/`🛑`).

Fitur v1.2.0 sebelumnya:
1. **Pesan Awal Telegram `/start` & `/help`:** Diformat rapi dan elegan sesuai pembaruan Telegram Bot API 10.4.
2. **Modernisasi Chat UI CLI (`renstra chat`):** Tampilan welcome banner informatif, prompt visual interaktif, status bar live streaming.
3. **In-Place Self-Updater (`renstra update`):** Pembaruan otomatis binary langsung dari GitHub Releases (`firdausmntp/RENSTRA`).
4. **Notifikasi Rilis Baru:** Deteksi non-blocking versi rilis baru dengan cache TTL 12 jam.
5. **One-Line Bash Installer:** Script [`install.sh`](install.sh) untuk instalasi cepat di VPS.
6. **Error Formatter Rapi & Elegan:** Parsing HTTP status code dan rendering `<blockquote>` Telegram / ANSI box CLI.
7. **Peningkatan Setup & Auto-Load `.env`:** Wizard `renstra setup` menyimpan API Key ke `$RENSTRA_HOME/.env` (mode `0600`).
Lihat daftar perbaikan lengkap di [TODO.md](TODO.md).

---

## 8. Aturan Commit, Tag & Riwayat Rilis (Wajib)

Untuk menjaga keterlacakan perubahan di repositori, setiap commit dan tag rilis wajib mengikuti standar berikut:

1. **Detail di Setiap Commit (`git commit`):**
   - Gunakan format Conventional Commits pada baris subjek (`feat:`, `fix:`, `refactor:`, `docs:`, `release:`).
   - Wajib sertakan **commit body yang detail** berisi daftar perubahan per komponen/paket, alasan teknis, serta status pengujian (`go test`, `go vet`). Hindari commit satu baris tanpa konteks.
2. **Annotated Tag dengan Riwayat Lengkap (`git tag -a`):**
   - Dilarang membuat lightweight tag untuk rilis. Selalu gunakan *annotated tag* (`git tag -a vX.Y.Z`) yang memuat ringkasan rilis, poin fitur baru, perbaikan bug, dan catatan penting.
3. **Sinkronisasi Catatan Rilis:**
   - Setiap kali menyiapkan tag rilis baru, perbarui daftar perubahan di [`TODO.md`](TODO.md) dan bagian status di `AGENTS.md` / [`README.md`](README.md) sebelum commit dan tag dibuat.
