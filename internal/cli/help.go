package cli

import (
	"fmt"
	"io"
	"strings"
)

var cliUpdateNotice string

// SetUpdateNotice sets a global update notification shown in CLI help footers.
func SetUpdateNotice(msg string) {
	cliUpdateNotice = strings.TrimSpace(msg)
}

// printHelp renders the grouped, colored REPL command reference in "id" or "en".
func printHelp(out io.Writer, lang string) {
	section := func(title string) {
		fmt.Fprintf(out, "\n%s%s%s\n", cCyan, title, cReset)
	}
	row := func(cmd, desc string) {
		fmt.Fprintf(out, "  %s%-32s%s %s%s%s\n", cYellow, cmd, cReset, cDim, desc, cReset)
	}
	if strings.EqualFold(lang, "en") {
		fmt.Fprintf(out, "%s╭─ RENSTRA CLI Commands%s\n", cCyan, cReset)
		fmt.Fprintf(out, "%sUsage:%s %s/<command> [arguments]%s  |  %s<message>%s (sent directly to agent)\n", cCyan, cReset, cYellow, cReset, cWhite, cReset)
		section("Session & Control:")
		row("/new, /restart", "Start a new session (break history)")
		row("/approval [prompt|auto|deny]", "Set tool execution approval mode")
		row("/undo", "Restore the last modified file from checkpoint")
		row("/compact", "Summarize conversation history now")
		row("/context [size]", "Show or set model context window (e.g. 128k, 200k, 1m)")
		row("/status", "Session info: model, context, cost")
		section("Model, Accounts & Memory:")
		row("/model [name]", "Show active model or switch (name/alias/provider:model)")
		row("/model add <name> <prov> [ctx]", "Register & switch to a new model (e.g. 128k, vision)")
		row("/model rm <name>", "Remove a model or alias from config.yaml")
		row("/account [list|add|key|use|rm]", "Manage multiple provider accounts & API keys (.env)")
		row("/fallback [reset]", "Inspect or reset smart fallback circuit breakers")
		row("/skills [<name>|reload]", "List skills, view <name>, or rescan from disk")
		row("/memory [set|user|soul|del|limit]", "View or edit MEMORY.md, USER.md, SOUL.md & limits")
		row("/soul [reset|<text>]", "View or update SOUL.md (default Zero Refusal)")
		row("/persona [reset|<text>]", "View or set session persona (system prompt prefix)")
		row("/remember <note>", "Save quick note to MEMORY.md (or user:/soul:)")
		row("/export [file]", "Export active conversation to Markdown")
		row("/logs [clear]", "View or clear recent error logs")
		section("Automation & Analytics:")
		row("/cron [list|add|toggle|rm]", "Manage scheduled cron jobs (/cron add <sched> | <prompt>)")
		row("/usage", "Today's token & cost usage")
		row("/insights", "Today, 7-day & 30-day usage summary")
		section("General:")
		row("/lang [id|en]", "Switch CLI help language")
		row("/version", "Show binary version")
		row("/help [id|en]", "Show this help guide")
		row("/exit, /quit", "Exit terminal chat (Ctrl+C cancels active turn)")
		if cliUpdateNotice != "" {
			fmt.Fprintf(out, "\n%s💡 %s%s\n", cYellow, cliUpdateNotice, cReset)
		}
		return
	}

	fmt.Fprintf(out, "%s╭─ Perintah RENSTRA CLI%s\n", cCyan, cReset)
	fmt.Fprintf(out, "%sUsage:%s %s/<command> [argumen]%s  |  %s<pesan>%s (dikirim langsung ke agent)\n", cCyan, cReset, cYellow, cReset, cWhite, cReset)
	section("Session & Control:")
	row("/new, /restart", "Mulai sesi baru (potong riwayat)")
	row("/approval [prompt|auto|deny]", "Atur mode persetujuan eksekusi tool")
	row("/undo", "Kembalikan file terakhir dari checkpoint")
	row("/compact", "Ringkas riwayat percakapan sekarang")
	row("/context [ukuran]", "Lihat atau ubah context window (mis. 128k, 200k, 1m)")
	row("/status", "Info sesi: model, konteks, biaya")
	section("Model, Akun & Memori:")
	row("/model [nama]", "Model aktif + daftar provider/model, atau ganti model")
	row("/model add <nama> <prov> [ctx]", "Tambah & aktifkan model baru (mis. 128k, vision)")
	row("/model rm <nama>", "Hapus model atau alias dari config.yaml")
	row("/account [list|add|key|use|rm]", "Kelola multi-akun provider & API key (.env)")
	row("/fallback [reset]", "Cek status & reset cooldown smart fallback")
	row("/skills", "Daftar skill terdaftar")
	row("/skills <nama> | reload", "Lihat isi lengkap satu skill atau scan ulang disk")
	row("/memory [set|user|soul|del|limit]", "Lihat atau kelola MEMORY.md, USER.md, SOUL.md & kuota")
	row("/soul [reset|<teks>]", "Lihat atau ubah SOUL.md (default Zero Refusal)")
	row("/persona [reset|<teks>]", "Lihat atau ubah persona sesi (system prompt prefix)")
	row("/remember <catatan>", "Simpan catatan ke MEMORY.md (atau user:/soul:)")
	row("/export [file]", "Export percakapan aktif ke Markdown")
	row("/logs [clear]", "Lihat atau bersihkan log error terbaru")
	section("Automation & Analytics:")
	row("/cron list", "Lihat jadwal cron")
	row("/cron add <jadwal> | <prompt>", "Tambah jadwal (5-field / @daily)")
	row("/cron toggle <id> | rm <id>", "Aktif/nonaktifkan atau hapus jadwal")
	row("/usage", "Token & biaya hari ini")
	row("/insights", "Rekap hari ini, 7 & 30 hari")
	section("General:")
	row("/lang [id|en]", "Ganti bahasa bantuan CLI")
	row("/version", "Versi binary")
	row("/help [id|en]", "Panduan ini")
	row("/exit, /quit", "Keluar (Ctrl+C: batalkan turn berjalan)")
	if cliUpdateNotice != "" {
		fmt.Fprintf(out, "\n%s💡 %s%s\n", cYellow, cliUpdateNotice, cReset)
	}
}

// PrintCLIUsage renders the top-level `renstra --help` standard CLI usage guide
// (Usage, Commands, Subcommands, Flags, Environment, Examples, and Update Footer).
func PrintCLIUsage(out io.Writer, lang, version string) {
	out = plainIfNotTTY(out)
	section := func(title string) {
		fmt.Fprintf(out, "\n%s%s%s\n", cCyan, title, cReset)
	}
	row := func(cmd, desc string) {
		fmt.Fprintf(out, "  %s%-36s%s %s%s%s\n", cYellow, cmd, cReset, cDim, desc, cReset)
	}

	verTag := ""
	if version != "" {
		verTag = " " + cGreen + version + cReset
	}
	fmt.Fprintf(out, "%s⚡ RENSTRA%s%s %s— Self-Hosted Telegram-First Autonomous AI Agent%s\n", cBrightCyan, cReset, verTag, cDim, cReset)

	if strings.EqualFold(lang, "en") {
		section("Usage:")
		fmt.Fprintf(out, "  %srenstra%s %s<command>%s [subcommand] [arguments] [flags]\n", cWhite, cReset, cYellow, cReset)
		section("Core Commands:")
		row("setup", "Interactive setup wizard (Telegram token, provider, .env API key)")
		row("serve", "start Telegram gateway (Bot API 10.5), cron scheduler & MCP client")
		row("restart [--start]", "Restart running renstra service/daemon (or start if --start)")
		row("chat [model]", "Start interactive terminal REPL chat session")
		row("update [check]", "Self-update binary in-place from GitHub Releases")
		row("migrate hermes [flags]", "Full migration from ~/.hermes (.env, accounts, memory, skills, MCP)")
		section("Management Commands:")
		row("model <list|set|add|rm> [args]", "List, switch, add (<name> <prov> [ctx]), or remove models")
		row("account <list|add|key|use|rm> [args]", "Manage multiple provider accounts & API keys in .env")
		row("skill <list|add|rm> [args]", "List, import (<dir|git-url>), create, or delete SKILL.md")
		row("mcp [list]", "List configured MCP servers from mcp.yaml")
		row("usage", "Show token & USD cost analytics (today, 7d, 30d)")
		row("config path", "Print active $RENSTRA_HOME data directory path")
		row("version", "Print binary version and update status")
		row("help [--lang id|en]", "Show this usage help message")
		section("Flags:")
		row("-h, --help", "Show usage help")
		row("-v, --version", "Show binary version")
		row("-l, --lang <id|en>", "Set help output language (id or en)")
		row("--global", "Use global skills layer (with `renstra skill`)")
		row("--from <dir> | --dry-run | --force", "Migration options (with `renstra migrate hermes`)")
		section("Environment:")
		row("RENSTRA_HOME", "Custom data & config directory path")
		row("RENSTRA_LANG", "Default CLI help language (id or en)")
		row("NO_COLOR", "Disable ANSI color output")
		section("Examples:")
		fmt.Fprintf(out, "  %srenstra setup && renstra serve%s\n", cDim, cReset)
		fmt.Fprintf(out, "  %srenstra model add claude-3-7-sonnet anthropic 200k vision%s\n", cDim, cReset)
		fmt.Fprintf(out, "  %srenstra account add openrouter2 openrouter sk-or-v1-xxxx anthropic/claude-3.7-sonnet%s\n", cDim, cReset)
		fmt.Fprintf(out, "  %srenstra migrate hermes --force%s\n", cDim, cReset)
		if cliUpdateNotice != "" {
			fmt.Fprintf(out, "\n%s💡 %s%s\n", cYellow, cliUpdateNotice, cReset)
		}
		return
	}

	section("Usage:")
	fmt.Fprintf(out, "  %srenstra%s %s<command>%s [subcommand] [arguments] [flags]\n", cWhite, cReset, cYellow, cReset)
	section("Core Commands:")
	row("setup", "Wizard konfigurasi interaktif (token Telegram, provider, API key .env)")
	row("serve", "jalankan gateway Telegram (Bot API 10.5), cron scheduler & client MCP")
	row("restart [--start]", "Restart service/daemon renstra aktif (atau nyalakan dengan --start)")
	row("chat [model]", "Sesi chat interaktif di terminal REPL")
	row("update [check]", "Periksa & perbarui binary renstra otomatis dari GitHub Releases")
	row("migrate hermes [flags]", "Migrasi penuh dari ~/.hermes (.env, multi-akun, memory, skill, MCP)")
	section("Management Commands:")
	row("model <list|set|add|rm> [args]", "Kelola daftar model, default model, tambah model baru, atau hapus")
	row("account <list|add|key|use|rm> [args]", "Kelola multi-akun provider & API key di .env")
	row("skill <list|add|rm> [args]", "Daftar, impor (<dir|git-url>), buat, atau hapus SKILL.md")
	row("mcp [list]", "Daftar server MCP eksternal di mcp.yaml")
	row("usage", "Laporan analitik pemakaian token & estimasi biaya USD")
	row("config path", "Tampilkan lokasi direktori aktif $RENSTRA_HOME")
	row("version", "Tampilkan versi binary dan status pembaruan")
	row("help [--lang id|en]", "Tampilkan panduan penggunaan CLI ini")
	section("Flags:")
	row("-h, --help", "Tampilkan panduan penggunaan")
	row("-v, --version", "Tampilkan versi binary")
	row("-l, --lang <id|en>", "Pilih bahasa panduan CLI (id atau en)")
	row("--global", "Targetkan direktori skill global (pada `renstra skill`)")
	row("--from <dir> | --dry-run | --force", "Opsi migrasi Hermes (pada `renstra migrate hermes`)")
	section("Environment:")
	row("RENSTRA_HOME", "Override lokasi direktori data & konfigurasi")
	row("RENSTRA_LANG", "Bahasa default bantuan CLI (id atau en)")
	row("NO_COLOR", "Nonaktifkan output warna ANSI")
	section("Examples:")
	fmt.Fprintf(out, "  %srenstra setup && renstra serve%s\n", cDim, cReset)
	fmt.Fprintf(out, "  %srenstra model add claude-3-7-sonnet anthropic 200k vision%s\n", cDim, cReset)
	fmt.Fprintf(out, "  %srenstra account add openrouter2 openrouter sk-or-v1-xxxx anthropic/claude-3.7-sonnet%s\n", cDim, cReset)
	fmt.Fprintf(out, "  %srenstra migrate hermes --force%s\n", cDim, cReset)
	if cliUpdateNotice != "" {
		fmt.Fprintf(out, "\n%s💡 %s%s\n", cYellow, cliUpdateNotice, cReset)
	}
}
