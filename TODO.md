# TODO — Rencana Perbaikan & Peningkatan RENSTRA

Daftar perbaikan, audit keamanan, dan pengembangan fitur antar versi.

---

## Rilis v1.3.0 (Web Dashboard Control Plane, Live SSE Logs, Skills Studio & Memory/Account Manager)

- [x] **Web Dashboard Single-Binary (`embed`) dengan Port & Token Konfigurabel**
  - **Lokasi:** [`internal/dashboard/server.go`](internal/dashboard/server.go), [`internal/dashboard/api_manage.go`](internal/dashboard/api_manage.go), [`internal/dashboard/ui/`](internal/dashboard/ui/index.html), [`internal/config/config.go`](internal/config/config.go), [`cmd/renstra/dashboard.go`](cmd/renstra/dashboard.go)
  - **Fitur:** Dashboard UI gelap modern tersemat langsung di binary Go (tanpa dependensi eksternal), port dapat diatur lewat `config.yaml` (`dashboard.addr` / `dashboard.port`), flag `--dashboard-port` / `--dashboard-addr` / `--no-dashboard`, atau env `RENSTRA_DASHBOARD_*`, lengkap dengan autentikasi Bearer token opsional (`dashboard.token`) dan perintah Telegram `/dashboard`.
- [x] **Live Log Hub (`slog.Handler` + SSE Real-Time + Persistent File Log)**
  - **Lokasi:** [`internal/dashboard/loghub.go`](internal/dashboard/loghub.go), [`cmd/renstra/serve.go`](cmd/renstra/serve.go), [`internal/store/dashboard.go`](internal/store/dashboard.go)
  - **Fitur:** Menangkap seluruh event terstruktur (`INFO`, `WARN`, `ERROR`, runtime error Telegram, tool approval, smart fallback, retry) ke ring buffer in-memory, file `$RENSTRA_HOME/logs/renstra.log`, dan live stream SSE `/api/logs/stream` dengan sensor secret otomatis serta tabel trace Turns terbaru.
- [x] **Kelola Skills, Memory/SOUL, Multi-Akun, Model & Cron dari Browser (+ SPA Routes & Full CRUD)**
  - **Lokasi:** [`internal/dashboard/api_manage.go`](internal/dashboard/api_manage.go), [`internal/dashboard/api_ops.go`](internal/dashboard/api_ops.go), [`internal/dashboard/ui/js/`](internal/dashboard/ui/js/app.js)
  - **Fitur:** SPA routes per tab (`/overview`, `/logs`, `/skills`, `/memory`, `/accounts`, `/cron`), Skills Studio (`SKILL.md` CRUD + reload), editor `SOUL.md`/`MEMORY.md`/`USER.md` (+ clear & quota limit), manajer multi-akun & model CRUD, reset circuit breaker, Cron CRUD + inspeksi transkrip & reset sesi, download log `.jsonl`, warning keamanan jika bind publik tanpa token, serta UI responsif Tablet & Mobile drawer.
- [x] **`/model <id> --global` & Antrean Pesan Coalesced Saat Thinking Ala Hermes**
  - **Lokasi:** [`cmd/renstra/telegram_group.go`](cmd/renstra/telegram_group.go), [`cmd/renstra/main.go`](cmd/renstra/main.go), [`internal/telegram/group.go`](internal/telegram/group.go), [`internal/telegram/bot.go`](internal/telegram/bot.go), [`internal/store/dashboard.go`](internal/store/dashboard.go)
  - **Fitur:** `/model <id> --global` mengubah model aktif di seluruh sesi, grup, dan topik serentak; pesan baru yang masuk saat bot sedang *thinking*/menjalankan tool tidak lagi membatalkan tugas berjalan, melainkan diantrekan dan digabung otomatis setelah tugas aktif selesai (tetap bisa dibatalkan instan dengan `/stop`).

---

## Rilis v1.2.8 (Anti-Lone-`>` Streaming, `send_telegram` Cross-Topic, Empty-Response Retry & Informative Tool/Turn Timeout)

- [x] **Anti-Lone-`>` Streaming Draft & Fallback Finalize**
  - **Lokasi:** [`internal/telegram/stream.go`](internal/telegram/stream.go), [`internal/telegram/send.go`](internal/telegram/send.go), [`internal/telegram/format.go`](internal/telegram/format.go)
  - **Perbaikan:** Menahan flush token awal yang hanya berisi simbol/prefix markdown (seperti `>` tunggal pada awal blockquote) hingga ada konten kata bermakna (`hasMeaningfulStreamText`), mencegah terkirimnya pesan kosong/`>` terpisah sebelum balasan penuh, serta fallback `SendMessage` jika `EditMessage` saat `Finalize` gagal.
- [x] **Tool Native `send_telegram` untuk Pesan Antar-Topik / Antar-Chat Terformat**
  - **Lokasi:** [`internal/tool/send_telegram.go`](internal/tool/send_telegram.go), [`internal/telegram/reply.go`](internal/telegram/reply.go), [`internal/config/config.go`](internal/config/config.go), [`cmd/renstra/serve.go`](cmd/renstra/serve.go)
  - **Fitur:** Menambahkan tool `send_telegram(chat_id, topic_id, text)` agar agent dapat mengirim pesan ke topik forum atau chat lain secara langsung dengan konversi Markdown -> Telegram HTML (`<blockquote>`, bold, code) otomatis.
- [x] **Retry Attempt Otomatis Jika Response API AI Kosong**
  - **Lokasi:** [`internal/agent/fallback.go`](internal/agent/fallback.go)
  - **Perbaikan:** Menambahkan hingga 2x retry otomatis (`emptyResponseMaxRetries`) pada provider/model yang sama dengan jeda singkat sebelum berpindah ke fallback atau mengembalikan error.
- [x] **Streaming Tool Progress Informatif & Diagnosis Timeout Akurat**
  - **Lokasi:** [`internal/telegram/stream.go`](internal/telegram/stream.go), [`internal/cli/stream.go`](internal/cli/stream.go), [`internal/agent/runner.go`](internal/agent/runner.go), [`internal/agent/execute.go`](internal/agent/execute.go), [`internal/telegram/error.go`](internal/telegram/error.go), [`internal/cli/error.go`](internal/cli/error.go), [`internal/provider/openai/client.go`](internal/provider/openai/client.go)
  - **Perbaikan:** Menampilkan ringkasan argumen & durasi berjalan pada status tool (`⏳ Menjalankan terminal: \`...\` (15s)`), memindahkan timeout HTTP OpenAI ke `ResponseHeaderTimeout` agar stream panjang tidak terputus, serta melacak fase aktif di `Runner.Run` sehingga error `context deadline exceeded` menampilkan detail perintah/tool/iterasi yang timeout dan dibedakan dari provider timeout di `FormatError`.

---

## Rilis v1.2.7 (Telegram Group & Forum Topics, Core Telegram Bot API 10.5, Connection Pooling & Isolasi Topik)

- [x] **Dukungan Penuh Group, Supergroup & Forum Topics (Tanpa Tolakan `"DM saya langsung"`)**
  - **Lokasi:** [`internal/telegram/bot.go`](internal/telegram/bot.go), [`internal/telegram/group.go`](internal/telegram/group.go), [`internal/telegram/reply.go`](internal/telegram/reply.go), [`cmd/renstra/telegram_handler.go`](cmd/renstra/telegram_handler.go)
  - **Fitur:** Mendukung chat `group` dan `supergroup` (otorisasi via ID grup di `allowed_users`/`allowed_chats` maupun ID pengirim `From.ID`), filter topik relevan (`allowed_topics`), mode pemicu grup (`group_mode: always|mention` untuk `@bot_username` / reply ke bot / slash command), pelacakan nama topik otomatis (`forum_topic_created` / `forum_topic_edited`), injeksi konteks `[Grup | Topik | Pengirim]`, serta isolasi histori SQLite & lock turn paralel per topik (`SessionChatID` & `lockScope`).
- [x] **Pembaruan Core Telegram Bot API 10.5 (`getMe` `BotFullInfo`, `setMySettings`, `WriteAccessAllowed.Inviter`, Expandable Quote `blocks`)**
  - **Lokasi:** [`internal/telegram/bot105.go`](internal/telegram/bot105.go), [`internal/telegram/types.go`](internal/telegram/types.go), [`internal/telegram/rich.go`](internal/telegram/rich.go), [`internal/config/config.go`](internal/config/config.go), [`cmd/renstra/serve.go`](cmd/renstra/serve.go)
  - **Fitur:** Struktur `BotFullInfo` (`active_usernames`, `inline_query_placeholder`, `needs_inline_query_location`, `has_topic_sidebar`), method `SetMySettings` (`can_join_groups`, `can_read_all_group_messages`, `has_topics_enabled`, `has_topic_sidebar`, `callback_query_mode`), pelacakan `WriteAccessAllowed.Inviter`, dan blok `RichBlockExpandableQuote` berbasis `blocks`.
- [x] **Optimasi Performa & Penanganan Turn Grup/Topik**
  - **Lokasi:** [`internal/telegram/bot.go`](internal/telegram/bot.go), [`internal/provider/openai/client.go`](internal/provider/openai/client.go), [`internal/provider/anthropic/client.go`](internal/provider/anthropic/client.go), [`internal/provider/google/client.go`](internal/provider/google/client.go), [`internal/agent/runner.go`](internal/agent/runner.go)
  - **Peningkatan:** Tuning HTTP connection pool (`MaxIdleConns: 32`, `MaxIdleConnsPerHost: 16`, `IdleConnTimeout: 90s`) di Telegram client dan adapter provider untuk menurunkan latensi TLS, serta pencatatan turn SQLite untuk ID grup negatif (`chatID != 0`).

- [x] **Default System `SOUL.md` (`ZERO REFUSAL` & Action-First), Perbaikan Approval Grup & Auto-Deteksi Context Window**
  - **Lokasi:** [`internal/memory/memory.go`](internal/memory/memory.go), [`internal/agent/prompt.go`](internal/agent/prompt.go), [`internal/tool/agent_tools.go`](internal/tool/agent_tools.go), [`cmd/renstra/serve.go`](cmd/renstra/serve.go), [`internal/config/resolve.go`](internal/config/resolve.go), [`cmd/renstra/telegram_config.go`](cmd/renstra/telegram_config.go)
  - **Fitur & Perbaikan:** Menambahkan berkas sistem bawaan `$RENSTRA_HOME/memory/SOUL.md` (`DefaultSoul` dengan prinsip **Zero Refusal & Action-First**) yang selalu di-inject di awal system prompt (serta perintah `/soul [reset|<teks>]` & `target=soul` di tool `memory`), isolasi `/persona [reset|<teks>]`, `/model`, dan `/config` per topik forum & per grup (`SessionChatID`), memperbaiki bug `cid <= 0` di `serve.go` yang menolak eksekusi tool pada chat grup (`cid < 0`), menghapus instruksi ragu-ragu di prompt, perintah `/model add <nama> <provider> <context>` & `/model rm` (di Telegram, CLI REPL & `renstra model add`), menu kontrol grup & topik in-chat `/group`, perintah `/context <ukuran>` + auto-scale `ContextWindow` dari usage provider, auto-deteksi ukuran context window model (`128k` / `200k` / `1M`), manajemen memori fleksibel (`/memory set|user|soul|here|del|limit` & `/remember user:|soul:|here:`), `/undo` checkpoint rollback, serta manajemen multi-akun (`/account` & `renstra account`) + migrasi penuh `.env` & multi-provider dari Hermes (`renstra migrate hermes`).

---

## Rilis v1.2.6 (Fix Streaming JSON Chunk, Reply/Sub-Message Telegram, `/memory`, `/remember`, `/export`)

- [x] **Perbaikan Error Streaming `chunk tidak valid: unexpected end of JSON input`**
  - **Lokasi:** [`internal/provider/openai/client.go`](internal/provider/openai/client.go), [`internal/provider/openai/stream.go`](internal/provider/openai/stream.go), [`internal/provider/anthropic/stream.go`](internal/provider/anthropic/stream.go), [`internal/provider/google/stream.go`](internal/provider/google/stream.go)
  - **Perbaikan:** Melewati baris `data:` kosong (*keepalive* SSE), menyangga (*buffer*) payload SSE JSON multi-baris sampai valid (`json.Valid` / baris kosong), serta fallback pembacaan respons JSON non-SSE pada HTTP 200.
- [x] **Proteksi Respons Kosong dari Model**
  - **Lokasi:** [`internal/agent/runner.go`](internal/agent/runner.go)
  - **Perbaikan:** Menolak respons model yang tidak menghasilkan teks maupun `tool_calls` agar tidak menyimpan pesan asisten kosong ke SQLite.
- [x] **Dukungan Penuh Sub-Message / Reply / Quote / Forum Thread Telegram**
  - **Lokasi:** [`internal/telegram/types.go`](internal/telegram/types.go), [`internal/telegram/reply.go`](internal/telegram/reply.go), [`internal/telegram/bot.go`](internal/telegram/bot.go), [`internal/telegram/send.go`](internal/telegram/send.go), [`internal/telegram/keyboard.go`](internal/telegram/keyboard.go), [`internal/telegram/approval.go`](internal/telegram/approval.go)
  - **Fitur:** Menginjeksi isi `reply_to_message` & `quote` (termasuk unduh media dari pesan yang di-reply) ke konteks agent, serta mengirim balik balasan, dokumen, foto, dan kartu approval dengan `message_thread_id` & `reply_parameters`.
- [x] **Manajemen Memori Interaktif (`/memory`, `/remember`), `/export` & Heading HTML**
  - **Lokasi:** [`cmd/renstra/telegram_extras.go`](cmd/renstra/telegram_extras.go), [`cmd/renstra/telegram_handler.go`](cmd/renstra/telegram_handler.go), [`cmd/renstra/telegram_callbacks.go`](cmd/renstra/telegram_callbacks.go), [`internal/cli/export.go`](internal/cli/export.go), [`internal/telegram/format.go`](internal/telegram/format.go)
  - **Fitur:** Menu `/memory` interaktif dengan tombol clear/refresh, perintah cepat `/remember` (+ shortcut reply pesan di Telegram), `/export` transkrip percakapan ke `.md`, dan rendering heading Markdown `#` menjadi `<b>` di Telegram.

- [x] **Smart Fallback & Circuit Breaker Multi-Provider (`/fallback`)**
  - **Lokasi:** [`internal/agent/smart_fallback.go`](internal/agent/smart_fallback.go), [`internal/agent/fallback.go`](internal/agent/fallback.go), [`cmd/renstra/providers.go`](cmd/renstra/providers.go), [`cmd/renstra/telegram_fallback.go`](cmd/renstra/telegram_fallback.go), [`internal/cli/export.go`](internal/cli/export.go)
  - **Fitur:** Klasifikasi error (transient 429/5xx, auth/quota, context overflow, empty response), cooldown circuit-breaker per target dengan half-open recovery, routing sadar kapabilitas (vision & context window), auto-discovery multi-provider (`smart_fallback: true` / `fallbacks: [auto]`), serta menu interaktif `/fallback` di Telegram & CLI.
- [x] **Set Model CLI & Resolusi Fleksibel, Warna CLI Windows VT & Tombol Back-to-Menu**
  - **Lokasi:** [`internal/config/resolve.go`](internal/config/resolve.go), [`internal/config/save.go`](internal/config/save.go), [`cmd/renstra/commands_model.go`](cmd/renstra/commands_model.go), [`internal/cli/tty_windows.go`](internal/cli/tty_windows.go), [`cmd/renstra/telegram_callbacks.go`](cmd/renstra/telegram_callbacks.go)
  - **Peningkatan:** Perintah `renstra model set <ref>` dan resolusi nama model tanpa prefix provider, aktivasi Virtual Terminal ANSI di Windows (`SetConsoleMode`) + pewarnaan subcommand CLI, perintah `renstra restart [--start]` untuk me-restart service/server aktif (`systemd` / PID), auto-kirim file log diagnostik saat error + `/logs`, perbaikan parser Markdown Telegram (`_italic_`, `__bold__`, `~~strike~~`, blockquote berformat, bullet `*`, anti nested `<b><b>`), serta tombol `⬅️ Menu` (`cmd:menu`) di semua submenu Telegram.

---

## Rilis v1.2.5 (CLI Spinner & Warna, Telegram Approval Mode & Rich Approval Card)

- [x] **CLI Lebih Berwarna & Animated Loading Spinner**
  - **Lokasi:** [`internal/cli/stream.go`](internal/cli/stream.go), [`internal/cli/repl.go`](internal/cli/repl.go), [`internal/cli/approve.go`](internal/cli/approve.go)
  - **Peningkatan:** Menambahkan animasi Braille spinner (`⠋⠙⠹...`) dan pesan status live (`Menghubungi model...`, `Menjalankan <tool>...`), indikator pemakaian konteks berwarna (hijau/kuning/merah), kotak konfirmasi CLI berwarna dengan ekstraksi `cmd`, serta mekanisme `Pause`/`Resume` spinner saat konfirmasi user.
- [x] **Perintah `/approval [prompt|auto|deny]` & Auto-Approve Persisten per Chat**
  - **Lokasi:** [`internal/store/helpers.go`](internal/store/helpers.go), [`internal/agent/runner.go`](internal/agent/runner.go), [`cmd/renstra/commands.go`](cmd/renstra/commands.go), [`internal/cli/repl.go`](internal/cli/repl.go)
  - **Fitur:** Menambahkan perintah `/approval` di Telegram dan CLI untuk mengganti mode persetujuan secara langsung (`prompt`, `auto`, `deny`) dan menyimpannya per chat di SQLite (`kv`).
- [x] **Perbaikan Tampilan Pesan Approval Telegram & Tombol Cepat Auto-Approve**
  - **Lokasi:** [`internal/telegram/bot.go`](internal/telegram/bot.go), [`internal/telegram/message.go`](internal/telegram/message.go)
  - **Peningkatan:** Mengganti teks JSON mentah menjadi kartu konfirmasi HTML terstruktur (`🔐 Konfirmasi Eksekusi`, blok `<pre><code class="language-bash">` untuk `terminal`, pretty-print JSON untuk tool lain), status hasil terformat (`✅`/`❌`/`⚡`/`⌛`/`🛑`), serta tombol inline `⚡ Auto-Approve Chat Ini`.
- [x] **Standar Detail Commit & Tag History di `AGENTS.md`**
  - **Lokasi:** [`AGENTS.md`](AGENTS.md)
  - **Peningkatan:** Menetapkan aturan wajib commit body detail dan annotated tag dengan riwayat perubahan lengkap.

---

## Rilis v1.2.0 (UX Chat UI, Self-Update & One-Line Installer)

- [x] **Desain Ulang Pesan Awal Telegram `/start` & `/help` (Telegram Bot API 10.4)**
  - **Lokasi:** [`internal/telegram/message.go`](internal/telegram/message.go)
  - **Peningkatan:** Pesan sambutan dirancang ulang dengan visual branding elegan, kategori perintah terstruktur (Sesi & Kontrol, Kecerdasan & Memori, Otomasi & Analitik), dan petunjuk penggunaan yang ramah pengguna.
- [x] **Modernisasi Chat UI CLI (`renstra chat`)**
  - **Lokasi:** [`internal/cli/repl.go`](internal/cli/repl.go), [`internal/cli/stream.go`](internal/cli/stream.go), [`internal/cli/tty.go`](internal/cli/tty.go), [`cmd/renstra/main.go`](cmd/renstra/main.go)
  - **Peningkatan:** Welcome banner visual (versi, model, provider, context window, jumlah skill, workspace), prompt `╭─ User · HH:MM:SS`, live streaming dengan status bar (⏱ elapsed, ↑in ↓out tok, tok/s, ctx%, biaya USD), serta footer statistik turn dari `agent.TurnStats`. Slash command paritas penuh: `/model`, `/cron`, `/insights`, `/status`, `/skills <nama>`, `/version`. Deteksi TTY via `go-isatty`: output piped/file otomatis polos tanpa ANSI.
- [x] **Perintah In-Place Self-Updater (`renstra update`)**
  - **Lokasi:** [`internal/update/update.go`](internal/update/update.go), [`cmd/renstra/commands.go`](cmd/renstra/commands.go)
  - **Fitur:** Mengecek rilis terbaru dari GitHub (`firdausmntp/RENSTRA`), mengunduh asset binary yang cocok dengan OS & arsitektur (`runtime.GOOS`/`runtime.GOARCH`) dengan indikator progress download, lalu mengganti binary yang sedang berjalan secara aman (mendukung Unix atomic rename dan Windows fallback rename). Dukungan mode `renstra update check`.
- [x] **Notifikasi Versi Baru Otomatis (Non-Blocking)**
  - **Lokasi:** [`internal/update/update.go`](internal/update/update.go), [`cmd/renstra/main.go`](cmd/renstra/main.go), [`cmd/renstra/commands.go`](cmd/renstra/commands.go)
  - **Fitur:** Pengecekan versi rilis terbaru di latar belakang dengan cache TTL 12 jam (mencegah GitHub rate limit), menampilkan banner info saat membuka `renstra chat` dan catatan `slog` saat `renstra serve` dijalankan.
- [x] **One-Line Bash Installer Script (`install.sh`)**
  - **Lokasi:** [`install.sh`](install.sh)
  - **Fitur:** Mendukung instalasi instan via `curl -fsSL https://raw.githubusercontent.com/firdausmntp/RENSTRA/main/install.sh | bash` dengan auto-deteksi OS (Linux/macOS) dan arsitektur (amd64/arm64), fallback download direct, penempatan binary otomatis di `$PATH`, serta instruksi langkah awal yang rapi.
- [x] **Formating Pesan Error Rapi & Keren (Telegram & CLI)**
  - **Lokasi:** [`internal/telegram/error.go`](internal/telegram/error.go), [`internal/telegram/format.go`](internal/telegram/format.go), [`internal/cli/error.go`](internal/cli/error.go), [`internal/telegram/bot.go`](internal/telegram/bot.go), [`internal/cli/repl.go`](internal/cli/repl.go)
  - **Fitur:** Parsing error HTTP status code (401, 403, 404, 429, 500, dll.), endpoint, dan JSON body dari provider AI. Merender pesan error dengan tampilan visual terstruktur: badge judul, detail endpoint/kode error, kutipan provider dalam `<blockquote>` Telegram atau ANSI box CLI, serta actionable tips solusi langkah demi langkah.
- [x] **Peningkatan Wizard Setup & Auto-Load `.env`**
  - **Lokasi:** [`cmd/renstra/setup.go`](cmd/renstra/setup.go), [`internal/config/config.go`](internal/config/config.go)
  - **Fitur:** Wizard `renstra setup` kini langsung meminta input API Key dan menyimpannya secara aman dengan permission `0600` ke `$RENSTRA_HOME/.env`. `config.Load()` otomatis memuat `.env` saat aplikasi dinyalakan, mencegah terjadinya HTTP 401 unauthenticated akibat environment variable belum di-export di VPS.

---

## Rilis v1.1.0 (Security Hardening, Reliability & Hermes Migration)

- [x] **Migrasi lengkap dari `$HERMES_HOME` (`~/.hermes`) ke `$RENSTRA_HOME`** (`internal/migrate/hermes.go`)
- [x] **Perketat evaluasi aturan `always_allowed` di `ApprovalConfig`** (`internal/config/config.go`, `internal/config/approval_test.go`)
- [x] **Proteksi SSRF pada `web_fetch`** (`internal/tool/webfetch.go`, `internal/tool/webfetch_test.go`)
- [x] **Proteksi file sensitif untuk `read_file`, `write_file`, `patch`, dan `send_file`** (`internal/tool/file.go`, `internal/tool/send_file.go`)
- [x] **Isolasi model per chat & cron (`Runner` / `telegramHandler`)** (`cmd/renstra/commands.go`)
- [x] **Timeout startup `mcp.ConnectAll` & refresh daftar tool saat `Reconnect`** (`cmd/renstra/commands.go`, `internal/mcp/client.go`)
- [x] **Bind context pembatalan turn ke `StreamWriter`** (`internal/telegram/bot.go`)
- [x] **Bersihkan `Runner.chatLocks` untuk mencegah memory leak subagent** (`internal/agent/runner.go`)
- [x] **Perbaiki `pendingApproval` per chat & edit pesan saat timeout/cancel** (`internal/telegram/bot.go`)
- [x] **Batasi fallback plain text Telegram hanya untuk error parsing HTML** (`internal/telegram/bot.go`)
- [x] **Hitung System Prompt + Tool Schemas di `ShouldCompact` & perbaiki `AuxModel`** (`internal/agent/compress.go`, `internal/agent/summarize.go`)
- [x] **Retensi & pembersihan otomatis file sementara** (`cmd/renstra/commands.go`, `internal/tool/tool.go`)
- [x] **Perbarui wizard `renstra setup` & validasi `config.Load()`** (`cmd/renstra/setup.go`)
- [x] **Paritas slash command di CLI `renstra chat`** (`internal/cli/repl.go`)
- [x] **Import skill dari direktori lokal atau git URL (`renstra skill add`)** (`cmd/renstra/commands.go`)
- [x] **Transkripsi Voice Note Telegram (`voice.ogg` / `audio`)** (`cmd/renstra/commands.go`, `internal/telegram/bot.go`)
- [x] **Observability & Audit Trail Tambahan** (`internal/agent/runner.go`, `internal/agent/fallback.go`)

---

## Checklist Verifikasi Sebelum Rilis v1.2.0

```powershell
go test -count=1 -timeout 2m ./...
go vet ./...
```
- [x] Seluruh test unit `telegram`, `cli`, `update`, `config`, `tool`, `agent`, `cmd/renstra` lulus 100%.
- [x] Cross-compile 5 target platform (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`) zero-CGO lulus.
- [x] Script `install.sh` tervalidasi sintaks bash dan siap digunakan lewat curl.
- [x] Update `README.md`, `AGENTS.md`, dan `renstra version` ke `v1.2.0`.
