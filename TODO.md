# TODO — Rencana Perbaikan & Peningkatan RENSTRA

Daftar perbaikan, audit keamanan, dan pengembangan fitur antar versi.

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
