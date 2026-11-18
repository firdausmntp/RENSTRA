# TODO — Rencana Perbaikan & Peningkatan v1.1.0

Daftar temuan audit dari **v1.0.0** untuk ditutup dan ditingkatkan pada rilis **v1.1.0**.

---

## Urgent — Migrasi Data dari Hermes Agent (`renstra migrate hermes`)

- [x] **Migrasi lengkap dari `$HERMES_HOME` (`~/.hermes`) ke `$RENSTRA_HOME`**
  - **Lokasi:** `internal/migrate/hermes.go`, `cmd/renstra/commands.go`, `cmd/renstra/main.go`
  - **Cakupan:**
    - `memories/MEMORY.md` & `memories/USER.md` → `$RENSTRA_HOME/memory/` (dengan penjagaan batas karakter 2200/1375).
    - `skills/` (termasuk skill dalam subdirektori kategori) → `$RENSTRA_HOME/skills/`.
    - `config.yaml` + `.env` + `SOUL.md` → `$RENSTRA_HOME/config.yaml` & `$RENSTRA_HOME/mcp.yaml` (mapping provider, model, Telegram token, `allowed_users`, `system_prompt_prefix`, dan `mcp_servers`).
    - `state.db` (`sessions` & `messages`) → `$RENSTRA_HOME/state.db` (idempoten, menjaga pemetaan chat Telegram & CLI agar langsung bisa dicari lewat `session_search`).
    - Dukungan flag `--from <dir>`, `--dry-run`, dan `--force`.

---

## Prioritas Tinggi — Keamanan (Security Hardening)

- [x] **Perketat evaluasi aturan `always_allowed` di `ApprovalConfig`**
  - **Lokasi:** [`internal/config/config.go`](internal/config/config.go#L206-L219) (`matchRule`)
  - **Masalah:** `matchRule` saat ini memakai `strings.Contains(argStr, sub)` pada string JSON mentah. Aturan seperti `terminal:git status` bisa ter-bypass oleh `{"cmd":"git status && rm -rf /"}` atau komentar `# git status`.
  - **Solusi:**
    - Parse argumen JSON terlebih dahulu (khusus `terminal`, ekstrak field `cmd`).
    - Untuk `always_allowed`, wajibkan kecocokan prefix/command yang bersih dan tolak operator shell berantai (`&&`, `||`, `;`, `|`, `` ` ``, `$(`, `>`, `<`) kecuali diizinkan eksplisit dengan pola wildcard.
    - Pertahankan `always_denied` tetap menggunakan substring/pattern match agar pemblokiran tetap agresif.
    - Tambah unit test bypass di `internal/config/approval_test.go`.

- [x] **Proteksi SSRF pada `web_fetch`**
  - **Lokasi:** [`internal/tool/webfetch.go`](internal/tool/webfetch.go#L40-L66)
  - **Masalah:** `web_fetch` (`Danger() == false`) hanya mengecek skema `http`/`https`, sehingga rentan terhadap akses ke IP loopback (`127.0.0.1`, `::1`), jaringan privat (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), dan metadata cloud VPS (`169.254.169.254`) bila agent terkena prompt injection.
  - **Solusi:**
    - Resolusi DNS host sebelum koneksi (atau pakai custom `net.Dialer.Control`) dan tolak semua IP loopback, private, link-local, multicast, dan unspecified (`IsLoopback`, `IsPrivate`, `IsLinkLocalUnicast`, `IsLinkLocalMulticast`, `IsUnspecified`), termasuk saat mengikuti HTTP redirect.
    - Tambah opsi config eksplisit bila akses host lokal memang dibutuhkan (default: blokir).
    - Tambah unit test di `internal/tool/webfetch_test.go`.

- [x] **Proteksi file sensitif untuk `read_file`, `write_file`, `patch`, dan `send_file`**
  - **Lokasi:** [`internal/tool/file.go`](internal/tool/file.go#L18-L26), [`internal/tool/send_file.go`](internal/tool/send_file.go#L59-L69)
  - **Masalah:** Keempat tool file bertanda `Danger() == false` dan bebas mengakses path absolut. File seperti `$RENSTRA_HOME/config.yaml` (berisi `telegram.token`), `.env`, `~/.ssh/`, atau `state.db` dapat dibaca atau dikirim keluar via `send_file` tanpa approval user.
  - **Solusi:**
    - Blokir atau wajibkan approval eksplisit (`Danger` dinamis atau denylist path) untuk `$RENSTRA_HOME/config.yaml`, `$RENSTRA_HOME/state.db*`, `.env*`, dan direktori kredensial seperti `.ssh`, `.aws`, `.gnupg`.
    - Tambah unit test di `internal/tool/hardening_test.go`.

---

## Prioritas Menengah — Concurrency & Reliabilitas

- [x] **Isolasi model per chat & cron (`Runner` / `telegramHandler`)**
  - **Lokasi:** [`cmd/renstra/commands.go`](cmd/renstra/commands.go#L643-L653)
  - **Masalah:** Satu instance `agent.Runner` dipakai bersama oleh seluruh `allowed_users` dan goroutine `cron`. Ketika chat A mengganti model via `/model`, `Runner.Provider` dan `Runner.Model` global ikut berubah bagi `cron` maupun chat B sampai chat B mengirim pesan baru.
  - **Solusi:**
    - Simpan/resolve target provider + model per konteks chat (atau kloning `Runner` ringan per turn / per chat dari base config) agar perubahan `/model` di satu chat tidak pernah memengaruhi chat lain ataupun `cron` job.
    - Pastikan `cron` selalu memakai `default_model` (atau model spesifik job bila didukung).

- [x] **Timeout startup `mcp.ConnectAll` & refresh daftar tool saat `Reconnect`**
  - **Lokasi:** [`cmd/renstra/commands.go`](cmd/renstra/commands.go#L377), [`internal/mcp/client.go`](internal/mcp/client.go#L157-L169)
  - **Masalah:** `mcp.ConnectAll(ctx, ...)` memakai context utama server sehingga server MCP yang hang saat `initialize` dapat menahan startup `renstra serve` tanpa batas waktu.
  - **Solusi:**
    - Bungkus `mcp.ConnectAll` dengan `context.WithTimeout(ctx, 15*time.Second)` (atau konfigurasi per server) agar gateway Telegram tetap menyala walau salah satu server MCP bermasalah.
    - Perbarui metadata tool bila `Reconnect()` menemukan perubahan dari `tools/list`.

- [x] **Bind context pembatalan turn ke `StreamWriter`**
  - **Lokasi:** [`internal/telegram/bot.go`](internal/telegram/bot.go#L1108-L1136)
  - **Masalah:** `StreamWriter.flush()` memakai `context.Background()` tanpa mengetahui apakah `turnCtx` sudah dibatalkan oleh `/stop` atau `stopped_message_generation`, sehingga 1 request `sendRichMessageDraft` / `editMessageText` susulan masih bisa terkirim sesaat setelah turn dibatalkan.
  - **Solusi:**
    - Simpan `turnCtx` di `StreamWriter` (atau tambahkan `Cancel()` yang dipanggil saat `turnCtx` selesai) dan batalkan `flush()` seketika jika context turn sudah `Done()`.
    - Tangani kedaluwarsa draft > 30 detik (kirim ulang draft/placeholder jika tool berjalan lama).

- [x] **Bersihkan `Runner.chatLocks` untuk mencegah memory leak subagent**
  - **Lokasi:** [`internal/agent/runner.go`](internal/agent/runner.go#L150-L164) (`lockChat`)
  - **Masalah:** Setiap `RunSubagent` membuat `subID := time.Now().UnixNano()` dan menyimpannya ke `r.chatLocks`, tetapi entri map tersebut tidak pernah dihapus setelah turn selesai.
  - **Solusi:** Gunakan reference counting pada `chatLocks` (seperti `Bot.lockChat`) agar entri mutex dihapus saat `refs == 0`.

- [x] **Perbaiki `pendingApproval` per chat & edit pesan saat timeout/cancel**
  - **Lokasi:** [`internal/telegram/bot.go`](internal/telegram/bot.go#L782-L838) (`AskApproval`)
  - **Masalah:** `b.pending[chatID]` hanya menampung 1 entri per chat, dan jika timeout (`120s`) atau `ctx.Done()`, pesan inline keyboard tidak diedit sehingga tombol **Setuju / Tolak** tetap menggantung di layar user.
  - **Solusi:**
    - Indeks `pending` berdasarkan `approvalID` (bukan hanya `chatID`) agar approval paralel tidak saling menimpa.
    - Edit pesan menjadi `⌛ Approval kedaluwarsa` / `🛑 Dibatalkan` saat timeout atau cancel.

- [x] **Batasi fallback plain text Telegram hanya untuk error parsing HTML**
  - **Lokasi:** [`internal/telegram/bot.go`](internal/telegram/bot.go#L533-L555) (`SendMessageReturningID`, `EditMessage`)
  - **Masalah:** Fallback plain text dijalankan untuk *semua* error `b.post`, bukan hanya error HTTP `400` entity parsing, sehingga error jaringan atau `429` bisa memicu pengiriman ulang pesan.
  - **Solusi:** Kembalikan status code dari `b.post` (atau cek error HTTP `400`) agar fallback plain text hanya aktif saat Telegram menolak HTML entities.

- [x] **Hitung System Prompt + Tool Schemas di `ShouldCompact` & perbaiki `AuxModel`**
  - **Lokasi:** [`internal/agent/compress.go`](internal/agent/compress.go#L40-L45), [`internal/agent/summarize.go`](internal/agent/summarize.go#L32-L56)
  - **Masalah:** `ShouldCompact` belum menghitung token `systemPrompt` + JSON schema tool (termasuk tool MCP), berisiko melebihi context window model kecil (8k–16k). Selain itu, `summarize()` selalu memanggil `primary.Provider` (tidak me-resolve `aux_model` lintas provider) dan belum mencatat token ringkasan ke `usage_daily`.
  - **Solusi:**
    - Sertakan estimasi token `sysPrompt` + `Tools.Defs()` saat mengecek `ShouldCompact`.
    - Tambahkan `AuxProvider` di `Runner` (di-resolve dari `cfg.Agent.AuxModel`) dan catat pemakaian token kompaksi ke `Store.RecordUsage`.

- [x] **Retensi & pembersihan otomatis file sementara**
  - **Lokasi:** [`cmd/renstra/commands.go`](cmd/renstra/commands.go), [`internal/tool/tool.go`](internal/tool/tool.go)
  - **Masalah:** File lama di `workspace/inbox/` dan `workspace/.tool-output/` terus menumpuk tanpa pembersihan berkala.
  - **Solusi:** Tambahkan pruning berkala saat startup / cron untuk file berumur > 7–30 hari (dengan batas umur dapat dikonfigurasi).

---

## Prioritas Rendah — UX & Pengembangan Fitur

- [x] **Perbarui wizard `renstra setup` & validasi `config.Load()`**
  - **Lokasi:** [`cmd/renstra/setup.go`](cmd/renstra/setup.go#L26), [`cmd/renstra/commands.go`](cmd/renstra/commands.go#L248-L253)
  - **Masalah:** Wizard masih menampilkan opsi `openai / anthropic` saja (belum mencakup `google`, `openrouter`, `groq`, `ollama`, `mistral`, `xai`), dan `serveCmd` belum memberi peringatan saat `telegram.allowed_users` kosong.
  - **Solusi:** Lengkapi daftar ke-8 provider aktif di `renstra setup` (beserta default `BaseURL` & `APIKeyEnv`), validasi `aux_model` saat startup, dan log peringatan jika `allowed_users` kosong.

- [x] **Paritas slash command di CLI `renstra chat`**
  - **Lokasi:** [`internal/cli/repl.go`](internal/cli/repl.go#L38-L54)
  - **Masalah:** REPL terminal baru mendukung `/new`, `/help`, `/exit`, belum mendukung `/model`, `/usage`, `/compact`, `/skills`, `/memory`.
  - **Solusi:** Tambahkan slash command yang setara dengan gateway Telegram di REPL CLI.

- [x] **Import skill dari direktori lokal atau git URL (`renstra skill add`)**
  - **Lokasi:** [`cmd/renstra/commands.go`](cmd/renstra/commands.go#L168-L181)
  - **Masalah:** `renstra skill add` baru mendukung input inline `<name> <description> <body>`.
  - **Solusi:** Dukung `renstra skill add <dir|git-url> [--global]` untuk menyalin/mengkloning folder skill yang sudah memiliki `SKILL.md`.

- [x] **Transkripsi Voice Note Telegram (`voice.ogg` / `audio`)**
  - **Lokasi:** [`internal/telegram/bot.go`](internal/telegram/bot.go#L386-L405)
  - **Masalah:** File `voice`/`audio` saat ini sudah diunduh ke `workspace/inbox/`, namun baru diteruskan sebagai path file ke model teks.
  - **Solusi:** Tambahkan adapter/opsional speech-to-text (mis. OpenAI Whisper `/audio/transcriptions` atau Gemini audio input) agar pesan suara otomatis ditranskripsikan menjadi teks prompt.

- [x] **Observability & Audit Trail Tambahan**
  - **Lokasi:** [`internal/agent/runner.go`](internal/agent/runner.go#L220-L231)
  - **Rencana:** Catat hasil approval tool dan fallback provider di `slog`, serta tambahkan opsi checkpoint/rollback workspace sebelum modifikasi file besar.

---

## Checklist Verifikasi Sebelum Rilis v1.1.0

```powershell
go test -count=1 -timeout 2m ./...
go vet ./...
```
- [x] Seluruh test security bypass (`always_allowed`, SSRF `web_fetch`, proteksi file sensitif) lulus.
- [x] Test memory leak (`chatLocks`), compaction dengan system+tools overhead, dan `AskApproval` timeout edit lulus.
- [ ] CI (`ubuntu-latest`) dan Release cross-compile 5 target (`CGO_ENABLED=0`) lulus (verifikasi lokal `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` lulus; CI remote dijalankan saat push).
- [x] Update `README.md`, `AGENTS.md`, dan `renstra version` ke `v1.1.0`.
