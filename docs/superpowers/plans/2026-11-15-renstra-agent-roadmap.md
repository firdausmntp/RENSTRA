# Rencana Implementasi & Roadmap — RENSTRA Agent

- Spec: [`specs/2026-11-15-renstra-agent-design.md`](../specs/2026-11-15-renstra-agent-design.md)
- Panduan Agent/Developer: [`../../../AGENTS.md`](../../../AGENTS.md)
- Plan 1 (Core): [`plans/2026-11-15-renstra-agent-core.md`](../plans/2026-11-15-renstra-agent-core.md)
- Plan 2-4 (Gateway, Memory, Provider): [`plans/2026-11-15-renstra-agent-rest.md`](../plans/2026-11-15-renstra-agent-rest.md)
- Plan 5 (Paritas Hermes): [`plans/2026-11-16-renstra-hermes-features.md`](../plans/2026-11-16-renstra-hermes-features.md)

## Status v1.0 (Selesai)

| # | Plan | Hasil | Status |
|---|---|---|---|
| 1 | Core CLI agent | Binary yang bisa ngobrol, pakai `terminal` + tool file, simpan sesi di SQLite | Selesai |
| 2 | Telegram Gateway | DM bot, live streaming (`StreamWriter`), typing, download dokumen/foto, inline approval + edit keputusan, dedup, interrupt, slash commands | Selesai |
| 3 | Memory, skills, compression | `MEMORY.md`/`USER.md`, 4 lapis skills (`.skills`, `skills`, instance, global) + `--global` + `Reload()`, auto-compaction, `/usage`, `/insights` | Selesai |
| 4 | Provider, cron, delegate, MCP | `openai`, `anthropic`, `custom`, stub provider lain, cron scheduler unattended-safe, `delegate`, MCP stdio client, restart recovery `turns` | Selesai |
| 5 | Paritas Hermes + Hardening | `web_search`, `session_search`, failover `fallbacks`, loop detection 3×, limits/warning biaya harian, approval rules | Selesai |

Tiap milestone menghasilkan software yang dapat diuji dan dijalankan secara mandiri.

---

## Rancangan Pengembangan Selanjutnya (v1.1 & v1.2)

### v1.1.1 — Subagent Depth & Scoped Tool Registry
- **Masalah saat ini:** `Runner.RunSubagent` di `internal/agent/runner.go` mewariskan `r.Tools` langsung ke runner anak, sehingga tool `delegate` masih memakai `depth: 0` dan pembatasan kedalaman (`depth >= 2` di `internal/tool/delegate.go`) belum terincrement antar generasi anak.
- **Rancangan:**
  - Tambahkan field `Depth int` pada `agent.Runner` (atau helper `Registry.CloneWithDelegate(subagentRunner, nextDepth)`).
  - Saat `RunSubagent` membuat `child`, clone registry tool dan ganti entri `delegate` dengan `tool.NewDelegate(child, r.Depth+1)` (atau sembunyikan `delegate` jika `r.Depth+1 >= 2`), serta gunakan `child.Depth = r.Depth + 1`.
  - Pastikan `child` tetap mewarisi `chatID` asli dari parent context (`tool.ChatIDFrom`) tanpa menulis `turns` induk.

### v1.1.2 — Multimodal Vision & Outbound Telegram Files
- **Masalah saat ini:** Foto dari Telegram sudah diunduh ke `workspace/inbox/` tetapi baru diteruskan sebagai teks path `[Foto diterima: ...]`. Model juga belum memiliki tool bawaan untuk mengirim file/gambar hasil generate ke chat Telegram.
- **Rancangan:**
  - Tambah slice `Attachments []Attachment` (path + mime type) opsional pada `provider.Message` dan konverter image block untuk `openai` (`image_url` data URI) dan `anthropic` (`image` base64 source) bila model memiliki `vision: true`.
  - Tambah method `SendDocument` / `SendPhoto` (multipart form-data) di `internal/telegram/bot.go` serta tool `send_file` (atau callback context) untuk mengirim berkas dari workspace langsung ke pengguna Telegram.

### v1.1.3 — MCP HTTP/SSE Transport, Auto-Reconnect & Cron Isolation
- **Masalah saat ini:** `internal/mcp/client.go` baru mendukung transport stdio (`command`); server berbasis `url` belum tersambung. Jika proses MCP mati (`Client.Dead() == true`), belum ada mekanisme reconnect otomatis.
- **Rancangan:**
  - Simpan `ServerConfig` di `mcp.Client` atau manager di `internal/mcp` untuk mendukung auto-reconnect dengan backoff eksponensial saat `Dead()`.
  - Tambahkan transport HTTP/SSE (Streamable HTTP MCP) ketika `cfg.URL != ""`.
  - Di `Runner.Run` saat `isUnattended(ctx)` (`cron`), tolak atau filter tool `mcp__*` kecuali diizinkan secara eksplisit per job (sesuai Spec §7.13).

### v1.1.4 — Aktivasi Provider Stub & Mode Prompted Tools
- **Masalah saat ini:** Paket `openrouter`, `groq`, `ollama`, `mistral`, `xai`, dan `google` masih berupa stub `ErrNotImplemented`. Model dengan `tools: false` belum otomatis memakai mode prompted JSON tools.
- **Rancangan:**
  - Sambungkan `openrouter`, `groq`, `ollama`, `mistral`, dan `xai` ke `openai.New` dengan base URL default masing-masing serta header khusus bila diperlukan (mis. `HTTP-Referer` / `X-Title` untuk OpenRouter).
  - Implementasikan adapter native `google` (Gemini `streamGenerateContent` SSE).
  - Tambahkan lapisan konversi `prompted tools` di `internal/agent` ketika `ModelConfig.Tools != nil && !*ModelConfig.Tools`.

### v1.2 — Observability Terstruktur & Shadow Git Checkpoints
- Tambahkan `log/slog` JSON logger terstruktur per turn (provider, model, durasi, token in/out, jumlah tool, status approval, biaya USD) yang dikendalikan flag/config tanpa mengganggu output CLI REPL.
- Evaluasi snapshot ringan sebelum tool modifikasi file besar dijalankan di workspace agar perubahan berisiko mudah di-rollback.
