# Rencana Implementasi: 5 Fitur Kunci Hermes untuk RENSTRA

Tanggal: 2026-11-16
Tujuan: Menutup celah kapabilitas RENSTRA agar setara dengan Hermes Agent di area Telegram UX, reliability model, long-term memory retrieval, web intelligence, dan modularitas skill proyek.

---

## Task 1: Telegram In-Chat Slash Commands
**Tujuan**: Tangani `/new`, `/reset`, `/usage`, `/stop`, `/compact`, `/model` langsung di Telegram tanpa perlu SSH ke VPS.

### Sub-tasks:
1. Tambah method `CancelChat(chatID int64) bool` di `telegram.Bot` yang membatalkan `turnHandle.cancel` yang sedang berjalan.
2. Di `telegramHandler.Handle`:
   - Deteksi perintah diawali `/`.
   - `/stop`: panggil `bot.CancelChat(chatID)` -> balas "proses dihentikan".
   - `/new` atau `/reset`: panggil `store.SessionBreak(ctx, chatID)` + `runner.InvalidatePrompt()` -> balas "sesi baru dimulai. catatan memori di-freeze ulang."
   - `/usage`: panggil `store.UsageForDay(ctx, today)` -> format input/output token & estimasi USD -> balas.
   - `/compact`: paksa kompaksi riwayat chat saat itu juga -> balas ringkasan.
   - `/model`: tampilkan model saat ini dan daftar model yang tersedia, atau `/model <nama>` untuk mengganti model sesi chat.
   - `/help`: tampilkan daftar perintah.
3. Unit test di `internal/telegram/commands_test.go`.

---

## Task 2: Past Conversations Search (`session_search` tool)
**Tujuan**: Agent dapat mencari kembali obrolan lama atau informasi masa lalu yang sudah terpotong kompaksi.

### Sub-tasks:
1. Di `internal/store/messages.go`: tambah method `SearchMessages(ctx context.Context, chatID int64, query string, limit int) ([]Message, error)`.
   - Query SQL: `SELECT id, role, content, created_at FROM messages WHERE chat_id = ? AND content LIKE ? AND role IN ('user', 'assistant') ORDER BY id DESC LIMIT ?`.
2. Di `internal/tool/session_search.go`: buat tool `session_search`:
   - Name: `session_search`.
   - Description: "Mencari riwayat percakapan lama di chat ini berdasarkan kata kunci. Berguna untuk mengingat detail, konfigurasi, atau diskusi sesi terdahulu yang sudah terpotong dari konteks aktif."
   - Schema: `query` (string, required), `limit` (int, default 5).
3. Sambungkan `tool.NewSessionSearch(st)` di `commands.go` dan `main.go`.
4. Unit test di `internal/store/search_test.go` dan `internal/tool/session_search_test.go`.

---

## Task 3: Zero-API-Key Web Search (`web_search` tool)
**Tujuan**: Agent bisa melakukan riset web secara mandiri tanpa tergantung pengguna memberikan URL dan tanpa butuh API key berbayar.

### Sub-tasks:
1. Di `internal/tool/websearch.go`: implementasikan scraper DuckDuckGo HTML (`https://html.duckduckgo.com/html/?q=...`).
   - Gunakan `net/http` stdlib dengan User-Agent browser modern.
   - Ekstrak title, URL, dan cuplikan snippet teks (bersihkan tag HTML dan decoding entitas dasar).
   - Format output: 5 hasil teratas ringkas (Title, URL, Snippet).
2. Daftarkan di registry tool `web_search`.
3. Unit test di `internal/tool/websearch_test.go` (mock HTTP server).

---

## Task 4: Project-Local Skills (`.skills/` di CWD)
**Tujuan**: Jika user atau agent sedang berada di sebuah folder proyek git/repo yang memiliki folder `.skills/` atau `skills/`, agent otomatis memuat skill spesifik proyek tersebut.

### Sub-tasks:
1. Di `internal/skill/skill.go`: tambah fungsi `ReloadWithLocal(layers ...Layer)` atau izinkan pemindaian dinamis local layer.
2. Di `commands.go`: saat inisialisasi skill registry, masukkan `filepath.Join(workspace, ".skills")` dan `filepath.Join(workspace, "skills")` sebagai layer ketiga/keempat.
3. Di `internal/agent/prompt.go`: muat ulang skill index saat build prompt jika direktori lokal memiliki perubahan atau tersedia.
4. Unit test di `internal/skill/skill_test.go`.

---

## Task 5: Provider Failover / Fallbacks
**Tujuan**: Jika provider utama gagal (rate limit 429 berulang, server 5xx, atau kredit habis), otomatis coba provider cadangan (fallback chain).

### Sub-tasks:
1. Di `internal/config/config.go`: tambah field `Fallbacks []string` di root config.
2. Di `internal/agent/runner.go`:
   - Tambah struktur `FallbackProvider struct { Provider provider.Provider; Model string; MaxTokens int; PriceIn, PriceOut float64 }`.
   - Saat call utama gagal dengan error non-recoverable atau timeout, fallback dieksekusi secara berurutan.
   - Log pesan peringatan pergantian provider agar user tahu bot beralih ke cadangan.
3. Unit test di `internal/agent/fallback_test.go`.
