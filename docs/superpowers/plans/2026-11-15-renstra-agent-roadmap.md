# Rencana Implementasi — RENSTRA Agent

- Spec: [`specs/2026-11-15-renstra-agent-design.md`](../specs/2026-11-15-renstra-agent-design.md)
- Plan 1 (detail, 10 task): [`plans/2026-11-15-renstra-agent-core.md`](../plans/2026-11-15-renstra-agent-core.md)

## Urutan

| # | Plan | Hasil | Status |
|---|------|-------|--------|
| 1 | Core CLI agent | Binary yang bisa ngobrol, pakai `terminal` + tool file, simpan sesi di SQLite | siap dieksekusi |
| 2 | Telegram | DM bot, streaming, inline approval, dedup, interrupt | belum ditulis |
| 3 | Memory, skills, compression | `MEMORY.md`/`USER.md`, skills dua lapis + `--global`, kompaksi, `/usage` | belum ditulis |
| 4 | Provider lain, cron, delegate, MCP | `custom` provider, stub folder, scheduler, subagent, MCP + plugin | belum ditulis |

Tiap plan menghasilkan software yang bisa dipakai sendiri. Plan berikutnya ditulis setelah plan sebelumnya selesai, supaya interface-nya sudah nyata, bukan tebakan.
