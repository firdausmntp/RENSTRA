# RENSTRA Agent — Desain

Tanggal: 2026-11-15
Status: menunggu persetujuan
Revisi: 3 (paritas Hermes: scope memory/skills, biaya, `--global`)

## 1. Intent

Agent AI self-hosted berbahasa Go, dioperasikan dari Telegram (utama) dan CLI. Setiap orang yang berminat menjalankan instance-nya sendiri dan memasukkan bot token + API key miliknya lewat wizard/config. Perilaku mengikuti Hermes Agent: skills, memory, compression, multi-provider, cron, subagent, MCP, plugin.

Alasan: Hermes berat (21 channel, 7 backend terminal, desktop app, plugin catalog). Yang diambil fiturnya, bukan permukaannya.

## 2. Goals

- Satu binary tanpa cgo, deploy di VPS murah, dijalankan systemd.
- Telegram sebagai antarmuka utama: teks, dokumen, foto; agent bekerja di server; hasil mengalir balik.
- Konfigurasi per instance: bot token, provider (termasuk custom penuh), API key, model, alias model.
- Fitur v1: loop agent, multi-provider (2 aktif + custom), skills, memory, compression, cron, subagent, MCP, plugin-via-MCP, dedup update, potong output tool, pelaporan biaya.
- Satu instance = satu agent. Semua `allowed_users` berbagi memory, workspace, dan skills — sama seperti Hermes. Isolasi dicapai dengan menjalankan instance terpisah (`RENSTRA_HOME`).

## 3. Non-goals (v1)

Desktop app, web dashboard, browser automation, voice/TTS, image generation, platform selain Telegram dan CLI, group chat, plugin catalog, Nix/Termux packaging, container sandbox, git worktree isolation, memory provider eksternal (Honcho dkk), isolasi data antar allowed user.

## 4. Anti-goals

- Menjalankan perintah destruktif tanpa persetujuan.
- Memory membusuk atau overflow senyap.
- Memperlakukan isi halaman web atau output tool sebagai instruksi.
- Menyimpan secret ke database, log, atau transcript.
- Mematikan agent tanpa peringatan saat biaya naik.

## 5. Constraints

- Go 1.23+, tanpa cgo. Cross-compile ke linux/amd64 dan linux/arm64 dari satu mesin.
- Telegram Bot API: pesan maksimal 4.096 karakter; edit message di-throttle; update bisa terkirim ulang setelah restart.
- Long polling, bukan webhook: tanpa domain dan TLS.
- Satu proses: gateway, cron, CLI berbagi paket inti.
- Konteks model bervariasi 8k-200k token; semua asumsi ukuran datang dari tabel kapabilitas model, bukan hardcode.

### 5.1 SQLite

- Driver: **`modernc.org/sqlite`** (pure Go, tanpa cgo) → cross-compile tetap jalan, satu binary, tanpa gcc di VPS.
- Kenapa bukan `mattn/go-sqlite3`: butuh cgo, memperlambat build dan mematahkan cross-compile. Selisih kecepatannya tidak relevan untuk beban ini.
- Beban nyata: puluhan sampai ratusan ribu baris pesan setahun, beberapa query per turn. Target < 5 ms per operasi.
- Konfigurasi wajib: `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`, `temp_store=MEMORY`, `mmap_size=64MB`.
- **Satu koneksi tulis** (`SetMaxOpenConns(1)`) untuk menghilangkan `SQLITE_BUSY`. Kalau `/usage` melambat, read pool (`?mode=ro`) dipisah — perubahan terkurung di satu paket.
- Migrasi: `schema_version` di tabel `kv`, dijalankan saat start, idempoten.

## 6. Approaches

**A. Monolith dengan MCP sebagai satu-satunya bus ekstensi (dipilih).**
Satu binary berisi loop, provider, tool, store, adapter. Plugin = MCP server eksternal + entri config. Tradeoff: penulis plugin harus bicara MCP, tidak ada hot-reload in-process. Untung: satu mekanisme untuk plugin + integrasi, isolasi crash, ekosistem MCP tersedia, dependency kecil.

**B. Core + plugin host gRPC (`hashicorp/go-plugin`).**
Plugin Go bertipe, kontrak eksplisit. Tradeoff: gRPC + protobuf + versioning plugin, mengulang yang sudah disediakan MCP. Ditolak: biaya tetap besar untuk manfaat yang sama.

**C. Gateway Go + core agent Python.**
Paling cepat untuk fitur learning-loop. Tradeoff: dua runtime, dua toolchain, dua cara deploy. Ditolak: menghilangkan alasan memilih Go.

## 7. Keputusan

### 7.1 Layout

```
cmd/renstra/main.go            # setup, serve, chat, config, model, skill, mcp, cron, usage
internal/config/               # config.go, paths.go, setup.go
internal/agent/                # runner.go, loop.go, prompt.go, compress.go, budget.go, capability.go
internal/provider/             # provider.go (interface + registry), custom.go
internal/provider/openai/      # diimplementasikan
internal/provider/anthropic/   # diimplementasikan
internal/provider/openrouter/  # stub
internal/provider/groq/        # stub
internal/provider/google/      # stub
internal/provider/ollama/      # stub
internal/provider/mistral/     # stub
internal/provider/xai/         # stub
internal/tool/                 # registry.go, terminal.go, file.go, webfetch.go, memory.go, skill.go, cron.go, delegate.go
internal/mcp/                  # client.go (stdio + http), inject.go
internal/store/                # sqlite.go, schema.sql, messages.go, jobs.go, usage.go
internal/telegram/             # bot.go, stream.go, render.go, approve.go, dedup.go
internal/cli/                  # repl.go, render.go
```

Stub provider: folder ada, constructor mengembalikan `ErrNotImplemented`, `renstra model list` menampilkannya sebagai belum tersedia. Tujuannya supaya daftar provider terlihat sejak awal dan menambah implementasi nanti tidak mengubah struktur.

### 7.2 Layout data (paritas Hermes)

```
$RENSTRA_HOME/                  # default: ~/.renstra (Linux/macOS), %APPDATA%\renstra (Windows)
  config.yaml                   # 0600
  state.db                      # SQLite
  MEMORY.md                     # catatan agent, 2.200 char
  USER.md                       # profil user, 1.375 char
  skills/                       # skills milik instance (boleh ditulis agent)
  workspace/                    # cwd untuk `terminal` dan tool file
  logs/

<global>/                       # Linux/macOS: ~/.config/renstra/global
                                # Windows: %APPDATA%\renstra\global
  skills/                       # skills yang dipakai semua profil
```

- Satu instance melayani banyak `allowed_users`, tetapi **memory, workspace, dan skills dibagi**. Sama seperti Hermes: identitas yang dipisah adalah profil, bukan user Telegram.
- Konsekuensi yang diterima: antar allowed user tidak ada privasi data. Kalau butuh pisah, jalankan instance kedua (`RENSTRA_HOME` lain + bot token lain).
- `--global` berlaku untuk skills dan MCP server. Selain itu per instance.

### 7.3 Core loop

`agent.Runner` adalah struct (bukan singleton): provider, registry tool, store, workspace, budget, approver, logger, hooks.

```
Run(ctx, convo) → for {
    stream := provider.Stream(ctx, req)
    akumulasi text + tool_calls
    jika ada tool_calls: eksekusi (paralel, urutan hasil dipertahankan), append, ulang
    jika teks selesai: persist, kembalikan
}
```

- Rekursif-aman: `delegate` membuat `Runner` anak dengan history sendiri, subset tool, dan jatah budget dari induk.
- Cancel: `ctx` dibatalkan oleh `/stop`; adapter Telegram memegang cancel func per chat.
- Budget: maksimum 40 iterasi, batas token per turn, batas waktu 15 menit.
- Deteksi loop: tool sama + argumen sama 3× → berhenti dengan pesan ke model.
- Turn dicatat di tabel `turns` (status `running`/`done`/`interrupted`). Saat start, semua `running` dari proses sebelumnya ditandai `interrupted` dan chat diberi tahu.

### 7.4 Provider

```go
type Provider interface {
    Stream(ctx context.Context, req Request) (<-chan Event, error)
    Name() string
}
// Event: TextDelta | ToolCall | Usage | Done | Err
```

- `openai`: chat completions + tool calls.
- `anthropic`: Messages API, konversi schema tool, prompt caching.
- `custom`: provider generik yang dikonfigurasi penuh — base URL, path, header, proxy, auth, dan `api_style` (`openai_chat` | `openai_responses` | `anthropic`). Semua endpoint OpenAI-compatible bisa dipakai tanpa adapter khusus.
- Kapabilitas per model: `context`, `max_output`, `tools`, `vision`, `price_in`, `price_out`. Compression, budget, dan estimasi biaya membaca tabel ini. Default bila kosong: `context: 32000`, `tools: true`.
- Alias: `aliases: {murah: openrouter:meta-llama/llama-3.3-70b}`.
- Model `tools: false` memakai mode "prompted tools": schema di system prompt, output JSON diparse, satu retry lalu error jelas.
- Retry: 429/5xx backoff eksponensial + jitter, maksimal 3×, menghormati `Retry-After`.
- Override model per chat disimpan di `chats.model`.

Contoh config:

```yaml
providers:
  lokal:
    kind: custom
    api_style: openai_chat
    base_url: http://10.0.0.5:8080/v1
    path: /chat/completions
    api_key_env: LOKAL_KEY
    headers: { X-Title: renstra }
    proxy: socks5://127.0.0.1:1080
    timeout: 120s
    models:
      qwen3-32b: { context: 32768, max_output: 4096, tools: true, vision: false }
    default_model: qwen3-32b
aliases:
  murah: openrouter:meta-llama/llama-3.3-70b
  sonnet: anthropic:claude-sonnet-4-5
```

### 7.5 Tool

Registry: map nama → `Tool{Name, Description, Schema, Handler, Danger}`.

v1: `terminal`, `read_file`, `write_file`, `patch`, `web_fetch`, `memory`, `skill`, `cron`, `delegate`.
Tool MCP di-inject sebagai `mcp__<server>__<tool>` dengan **Danger default = butuh approval**.

Output tool dipotong di 30 KB (UTF-8 aman, tidak memotong di tengah rune). Sisanya ditulis ke `$RENSTRA_HOME/workspace/.tool-output/<turn>-<n>.txt` dan hasil tool berisi path-nya.

Approval: `Danger >= threshold` → approver. Telegram: inline keyboard, timeout 120 detik, default tolak. CLI: `y/N`. Ditolak → hasil tool berisi error, loop lanjut.

### 7.6 Skills (paritas Hermes)

Satu direktori per lapis, urutan prioritas dari atas:

1. `$RENSTRA_HOME/skills/` — milik instance, ditulis agent bila `allow_self_skills: true`.
2. `<global>/skills/` — dipakai semua profil di mesin ini; ditulis hanya bila `--global` dan `allow_self_skills: true`.
3. `skills.extra_dirs` di config — selalu read-only.

Nama bentrok: lapis atas menang; benturan dicatat di log.

- `renstra skill add <dir|git-url> [--global]` — tanpa flag masuk lapis 1, dengan `--global` masuk lapis 2.
- `renstra skill list [--global]`, `renstra skill rm <name> [--global]`.
- Index (nama + deskripsi) di-inject ke system prompt; body dimuat saat `/nama` atau saat tool `skill` dipanggil.
- Skill dibuat agent setelah tugas kompleks bila `allow_self_skills: true`; tiap `renstra update` tidak menimpa skill yang sudah diedit.

### 7.7 Memory (paritas Hermes)

- `$RENSTRA_HOME/MEMORY.md` (2.200 char) dan `USER.md` (1.375 char).
- Snapshot dibekukan saat sesi mulai; perubahan berlaku sesi berikutnya (menjaga prefix cache). `/new` membekukan ulang.
- Overflow → tool `memory` mengembalikan error, agent merapikan sendiri. Tidak ada pemotongan diam-diam.
- Penulisan hanya lewat tool `memory` + pengingat murah tiap 12 turn (satu baris tambahan di pesan sistem turn itu, tanpa panggilan LLM tambahan).
- Subagent dan cron tidak menulis memory kecuali diminta eksplisit di prompt job.

### 7.8 Compression

- Trigger saat pemakaian > 70% `context` model (pakai `usage` provider; fallback estimasi chars/4).
- Pertahankan system prompt + 6 turn terakhir verbatim; sisanya diringkas model `aux_model`.
- Pesan asli tidak dihapus: baris ringkasan ditambahkan, pesan lama ditandai `compacted_at`. Rebuild history = system + ringkasan + pesan setelah `compacted_at`.
- Pemotongan tidak pernah memisahkan grup `assistant tool_calls` dari hasil `tool`-nya.

### 7.9 Store

SQLite `$RENSTRA_HOME/state.db`.

```
users(id, telegram_id, username, created_at)
chats(id, user_id, model, created_at)
messages(id, chat_id, role, content, tool_calls_json, tool_call_id, tokens, compacted_at, created_at)
turns(id, chat_id, status, started_at, finished_at, error)
jobs(id, chat_id, schedule, prompt, enabled, next_run_at, last_run_at)
usage_daily(day, tokens_in, tokens_out, cost_usd)   -- per instance, bukan per user
updates_seen(update_id PRIMARY KEY, seen_at)
kv(key, value)
```

- Sesi = chat. `/new` menulis penanda `session_break`, bukan menghapus.
- `updates_seen` mencegah balasan dobel saat long polling mengirim ulang update; dibersihkan > 7 hari.
- `usage_daily` dipakai `/usage` dan `/insights`.

### 7.10 Telegram

- DM saja. Di grup, bot membalas sekali "DM saya" lalu diam.
- Long polling + dedup `update_id` sebelum memproses.
- Satu turn aktif per chat. Pesan baru saat turn jalan = **interrupt** turn lama, lalu mulai turn baru (perilaku Hermes). Cron untuk chat sibuk menunggu.
- Streaming: kirim placeholder, edit tiap ~1 detik atau 800 karakter; pecah jadi beberapa pesan bila > 4.096 karakter.
- Perintah: `/new`, `/stop`, `/model`, `/skills`, `/usage`, `/insights`, `/cron`, `/help`.
- Dokumen/foto disimpan ke workspace; agent menerima path. PDF/docx dibaca lewat `terminal` (`pdftotext`, `unzip`) di v1; tool `read_document` di v1.1.
- Rate limit: hormati `429 retry_after`, antrean kirim per chat.

### 7.11 Biaya dan kuota (paritas Hermes)

- **Tidak ada batas keras.** Biaya dicatat per turn (`usage_daily`) dan dilaporkan: `/usage` untuk sesi berjalan, `/insights` untuk agregat harian/bulanan, lengkap dengan token dan estimasi USD dari `price_in`/`price_out`.
- Peringatan: pesan singkat di akhir balasan bila biaya harian melewati `limits.warn_daily_usd` (default 1.0) atau konteks > 70%.
- Opsi `limits.max_daily_tokens` dan `limits.max_daily_usd` **default 0 = mati**. Kalau diisi, turn berikutnya ditolak dengan pesan jelas. Ini satu-satunya jalur pembatasan, dan tidak aktif kecuali kamu menyalakannya.

### 7.12 Konfigurasi per instance

- `renstra setup` menanyakan: bot token, provider, API key, model, allowed users.
- Disimpan di `$RENSTRA_HOME/config.yaml` (0600); default `~/.renstra` (Windows: `%APPDATA%\renstra`).
- Secret dari env (`api_key_env`) diutamakan; config tidak menyimpan key bila env tersedia.
- Profil ganda: `RENSTRA_HOME` untuk instance terpisah (bot lain, orang lain, eksperimen).

### 7.13 MCP dan plugin

- Config: `mcp: [{name, command, args, env} | {name, url, headers}]`, per instance; `--global` mendaftarkan ke `<global>/mcp.yaml` dan digabung saat start.
- Saat start: connect, `tools/list`, inject ke registry; reconnect backoff bila putus.
- Output MCP ditandai `[untrusted]`.
- Tool MCP default butuh approval; bisa diturunkan per tool di config (`mcp[].tools.<name>.danger`).
- Cron tidak boleh memakai tool MCP kecuali diizinkan eksplisit per job.
- Plugin = MCP server + entri config. Tidak ada sistem plugin terpisah.

### 7.14 Cron

- Tabel `jobs` + ticker 30 detik. `schedule` menerima 5-field cron dan `@daily`/`@hourly`. Timezone dari config.
- Job terlewat (server mati) → dijalankan **sekali**, tidak membackfill semua slot; `last_run_at` dicatat.
- Job = `{chat_id, schedule, prompt}`; Runner baru dengan konteks minim; hasil dikirim ke chat.
- Tool `Danger >= threshold` di job tanpa pengawas → langsung ditolak, kegagalan dikirim ke chat supaya bisa dijalankan manual.
- `/cron add|list|rm|pause`.

### 7.15 Keamanan

- Allowlist Telegram ID; default hanya owner. Model kepercayaan di §7.2.
- **Agent bebas membaca dan menulis di seluruh filesystem yang bisa diakses user OS** — sama seperti Hermes. Tidak ada jail. `cwd` default `$RENSTRA_HOME/workspace/`, tetapi path absolut dan `cd ..` diperbolehkan. Batasnya adalah izin OS, bukan aturan aplikasi.
- Yang menahan bukan sandbox, melainkan approval (§7.5) untuk perintah destruktif dan penanda `[untrusted]` untuk konten luar. Checkpoint/rollback (shadow git) masuk v1.1.
- Env subprocess: allowlist (`PATH`, `HOME`, `LANG`, `TZ`) + tambahan dari config. Key agent dan `RENSTRA_*` tidak diwariskan.
- Hasil `web_fetch`, tool MCP, dan output tool asing diberi penanda `[untrusted]`; system prompt melarang mengikuti instruksi di dalamnya.
- Secret disensor saat logging; `config.yaml` 0600.

### 7.16 Observability

`slog` JSON ke stderr. Per turn: provider, model, token in/out, durasi, jumlah tool, hasil approval, biaya estimasi. `/usage` dan `/insights` membaca `usage_daily`.

### 7.17 Urutan pembangunan

1. config + store (schema, pragma, migrasi) + provider openai + loop + CLI REPL.
2. Tabel kapabilitas model + budget + potong output tool.
3. Adapter Telegram: long polling, dedup, streaming, approval, interrupt.
4. Skills (instance + global + `--global`) + memory.
5. Compression + `/usage` + `/insights`.
6. Provider anthropic + `custom` + `/model` + stub folder provider lain.
7. Cron.
8. `delegate`.
9. MCP client + wiring plugin (+ `--global`).
10. Hardening: allowlist, workspace, redaction, restart recovery, dokumentasi.

## 8. Yang diserahkan ke builder

- Nama produk dan binary (sementara: `renstra`).
- Library Telegram: hand-rolled long polling vs `go-telegram-bot-api`.
- Library cron: `robfig/cron` vs ticker manual.
- Estimasi token saat `usage` tidak tersedia.
- Apakah `terminal` memakai PTY.
- Format config persis dan nama env.
- Bentuk `patch` (unified diff vs blok cari-ganti).

## 9. Risiko

- Rate limit edit Telegram → throttle wajib; fallback kirim pesan baru tiap 3.000 karakter.
- Loop boros token saat tool gagal berulang → budget + deteksi pengulangan.
- Shell di host = blast radius besar → diterima per §7.2. Approval + penanda untrusted yang menahan, bukan sandbox.
- Semua allowed user berbagi workspace, filesystem, dan memory → siapa pun bisa membaca file siapa pun. Diterima; isolasi = instance terpisah.
- Agent bebas menulis di luar workspace → kerusakan tidak terbatas pada folder proyek. Checkpoint/rollback menyusul di v1.1.
- Prompt injection dari halaman web atau tool MCP → penanda untrusted + approval default.
- `pdftotext` tidak ada di VPS → dependensi opsional, error jelas dari `terminal`.
- Tanpa batas biaya keras → pengeluaran bisa naik tanpa henti; ditutup peringatan + opsi cap yang bisa dinyalakan.
