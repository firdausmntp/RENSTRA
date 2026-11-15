# RENSTRA Agent — Plan 2-4: Gateway, Memory, Provider (gabungan)

**Goal:** Menyelesaikan sisa spec: bot Telegram, memory/skills/compression, provider tambahan, cron, delegate, MCP.

**Architecture:** Empat paket baru dibangun paralel karena tidak bersinggungan file: `internal/telegram`, `internal/memory` + `internal/skill`, `internal/provider/*`, `internal/cron` + `internal/mcp`. Penyambungan ke `internal/agent`, `internal/tool`, dan `cmd/renstra` dikerjakan setelahnya oleh satu pelaksana.

**Spec:** `docs/superpowers/specs/2026-11-15-renstra-agent-design.md`

## Global Constraints

- Go 1.24+, tanpa cgo, `CGO_ENABLED=0`.
- Paket baru hanya boleh mengimpor `internal/provider`, `internal/store`, `internal/config`, `internal/tool` — tidak boleh mengimpor `internal/agent` (menghindari siklus; penyambungan ada di `agent`).
- Setiap paket baru punya test sendiri; `go test ./...` hijau sebelum diserahkan.
- Secret tidak pernah ditulis ke DB atau log.
- Error berbahasa Indonesia, gaya sama dengan paket yang sudah ada.
- Jangan mengubah file di luar paket yang ditugaskan.

## Review Focus

- Bot Telegram menerima update yang sama dua kali setelah restart → harus diabaikan lewat `updates_seen`.
- Pesan balasan > 4.096 karakter → dipecah, bukan ditolak API.
- Memory melewati batas karakter → error jelas, bukan potong diam-diam.
- Skill dengan nama bentrok di dua lapis → lapis atas menang, benturan dicatat.
- Stream provider Anthropic memakai format event berbeda → jangan asumsikan format OpenAI.

## Kontrak antar paket

```go
// internal/memory
func Open(dir string) (*Store, error)
func (s *Store) Snapshot() Snapshot          // dibekukan saat dipanggil
func (s *Store) Write(kind Kind, content string) error  // KindMemory | KindUser, menegakkan batas
const MemoryLimit = 2200
const UserLimit = 1375

// internal/skill
func Load(layers []Layer) (*Registry, error) // urut prioritas, lapis 0 menang
type Layer struct{ Dir string; Writable bool }
func (r *Registry) Index() string            // nama + deskripsi untuk system prompt
func (r *Registry) Get(name string) (Skill, bool)
func (r *Registry) Create(name, description, body string) error // hanya lapis writable
func (r *Registry) Conflicts() []string

// internal/telegram
type Bot struct{ ... }
func New(cfg Config) (*Bot, error)
type Config struct{ Token string; AllowedUsers []int64; PollTimeout time.Duration }
type Handler interface {
    Handle(ctx context.Context, chatID int64, text string) (string, error)
    Approve(ctx context.Context, chatID int64, tool string, args string) (bool, error)
}
func (b *Bot) Run(ctx context.Context, h Handler) error

// internal/cron
func Run(ctx context.Context, st *store.Store, fire func(context.Context, Job)) error
type Job struct{ ID, ChatID int64; Schedule, Prompt string }

// internal/mcp
type ServerConfig struct{ Name, Command string; Args, Env []string; URL string; Headers map[string]string }
func Connect(ctx context.Context, cfg ServerConfig) (*Client, error)
func (c *Client) Tools() []tool.Tool
func (c *Client) Close() error
```

## Urutan penyambungan (setelah paket baru hijau)

1. `store`: helper `UpsertUser`, `UpsertChat`, `MarkUpdateSeen`, `RecordUsage`, `UsageTotals`.
2. `tool`: `memory`, `skill`, `cron`, `delegate`, `web_fetch`.
3. `agent`: `Runner.Memory`, `Runner.Skills`, kompaksi `compress.go`, `/usage` lewat `UsageTotals`.
4. `cmd/renstra`: `serve` (gateway + cron), `setup`, `model list`, `skill add/list/rm [--global]`, `mcp list`.
5. `provider/anthropic` didaftarkan di `main.go`; stub provider mengembalikan `ErrNotImplemented`.
