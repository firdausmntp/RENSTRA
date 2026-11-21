package cli

import (
	"fmt"
	"io"
	"strings"
)

// printHelp renders the grouped, colored command reference in "id" or "en".
func printHelp(out io.Writer, lang string) {
	section := func(title string) {
		fmt.Fprintf(out, "\n%s%s%s\n", cCyan, title, cReset)
	}
	row := func(cmd, desc string) {
		fmt.Fprintf(out, "  %s%-30s%s %s%s%s\n", cYellow, cmd, cReset, cDim, desc, cReset)
	}
	if strings.EqualFold(lang, "en") {
		fmt.Fprintf(out, "%s╭─ RENSTRA CLI Commands%s\n", cCyan, cReset)
		section("💬 Session & Control")
		row("/new, /restart", "start a new session (break history)")
		row("/approval [prompt|auto|deny]", "set tool execution approval mode")
		row("/compact", "summarize conversation history now")
		row("/status", "session info: model, context, cost")
		section("🧠 Model & Intelligence")
		row("/model", "active model + list providers/models")
		row("/model <name>", "switch model (name/alias/provider:model)")
		row("/fallback [reset]", "inspect or reset smart fallback circuit breakers")
		row("/skills", "list registered skills")
		row("/skills <name>", "view full content of a skill")
		row("/skills reload", "rescan skills from disk")
		row("/memory", "view saved memory & user profile")
		row("/remember <note>", "save quick note to MEMORY.md (or user:)")
		row("/export [file]", "export active conversation to Markdown")
		row("/logs [clear]", "view or clear recent error logs")
		section("⏰ Automation")
		row("/cron list", "list scheduled cron jobs")
		row("/cron add <sched> | <prompt>", "add cron job (5-field / @daily)")
		row("/cron toggle <id>", "enable / disable a cron job")
		row("/cron rm <id>", "delete a cron job")
		section("📊 Analytics")
		row("/usage", "today's token & cost usage")
		row("/insights", "today, 7-day & 30-day summary")
		section("ℹ️ Other")
		row("/lang [id|en]", "switch CLI help language")
		row("/version", "show binary version")
		row("/help [id|en]", "show this help guide")
		row("/exit", "exit terminal chat")
		row("ctrl+c", "cancel the currently running turn")
		fmt.Fprintf(out, "\n%sCron example: /cron add 0 9 * * * | summarize morning news%s\n", cDim, cReset)
		fmt.Fprintf(out, "%sAnything else is sent directly to the agent.%s\n", cDim, cReset)
		return
	}

	fmt.Fprintf(out, "%s╭─ Perintah RENSTRA CLI%s\n", cCyan, cReset)
	section("💬 Sesi & Kontrol")
	row("/new, /restart", "mulai sesi baru (potong riwayat)")
	row("/approval [prompt|auto|deny]", "atur mode persetujuan eksekusi tool")
	row("/compact", "ringkas riwayat percakapan sekarang")
	row("/status", "info sesi: model, konteks, biaya")
	section("🧠 Model & Kecerdasan")
	row("/model", "model aktif + daftar provider/model")
	row("/model <nama>", "ganti model (nama/alias/provider:model)")
	row("/fallback [reset]", "cek status & reset cooldown smart fallback")
	row("/skills", "daftar skill terdaftar")
	row("/skills <nama>", "lihat isi lengkap satu skill")
	row("/skills reload", "scan ulang skill dari disk")
	row("/memory", "lihat memori & profil tersimpan")
	row("/remember <catatan>", "simpan catatan ke MEMORY.md (atau user:)")
	row("/export [file]", "export percakapan aktif ke Markdown")
	row("/logs [clear]", "lihat atau bersihkan log error terbaru")
	section("⏰ Otomasi")
	row("/cron list", "lihat jadwal cron")
	row("/cron add <jadwal> | <prompt>", "tambah jadwal (5-field / @daily)")
	row("/cron toggle <id>", "aktif / nonaktifkan jadwal")
	row("/cron rm <id>", "hapus jadwal")
	section("📊 Analitik")
	row("/usage", "token & biaya hari ini")
	row("/insights", "rekap hari ini, 7 & 30 hari")
	section("ℹ️ Lainnya")
	row("/lang [id|en]", "ganti bahasa bantuan CLI")
	row("/version", "versi binary")
	row("/help [id|en]", "panduan ini")
	row("/exit", "keluar")
	row("ctrl+c", "batalkan turn yang sedang berjalan")
	fmt.Fprintf(out, "\n%sContoh cron: /cron add 0 9 * * * | ringkas berita pagi%s\n", cDim, cReset)
	fmt.Fprintf(out, "%sSelain perintah di atas, pesan dikirim langsung ke agent.%s\n", cDim, cReset)
}

// PrintCLIUsage renders the top-level `renstra help` usage guide with ANSI
// colors on interactive terminals and plain text when piped or redirected.
func PrintCLIUsage(out io.Writer, lang, version string) {
	out = plainIfNotTTY(out)
	section := func(title string) {
		fmt.Fprintf(out, "\n%s%s%s\n", cCyan, title, cReset)
	}
	row := func(cmd, desc string) {
		fmt.Fprintf(out, "  %s%-34s%s %s%s%s\n", cYellow, cmd, cReset, cDim, desc, cReset)
	}

	verTag := ""
	if version != "" {
		verTag = " " + cGreen + version + cReset
	}
	fmt.Fprintf(out, "%s⚡ RENSTRA%s%s %s— Self-Hosted Telegram-First AI Agent%s\n", cBrightCyan, cReset, verTag, cDim, cReset)

	if strings.EqualFold(lang, "en") {
		section("🚀 Gateway & Chat")
		row("renstra setup", "interactive setup wizard (bot token, API keys, etc.)")
		row("renstra serve", "start Telegram gateway + cron scheduler + MCP")
		row("renstra restart [--start]", "restart running renstra service/server if present")
		row("renstra chat [model]", "interactive terminal REPL chat session")
		section("🛠 Management & Tools")
		row("renstra update [check]", "check and update binary to the latest release")
		row("renstra migrate hermes [flags]", "import memory, skills, config & history from ~/.hermes")
		row("renstra model list", "list active providers, configured models & aliases")
		row("renstra model set <ref>", "set default model in config.yaml (model / provider:model / alias)")
		row("renstra skill list [--global]", "list registered skills")
		row("renstra skill add <dir|url>", "import skill from local directory or git URL")
		row("renstra skill add <n> <d> <b>", "create a new skill inline")
		row("renstra skill rm <n> [--global]", "remove a skill")
		row("renstra mcp list", "list configured MCP servers")
		section("📊 Info & Help")
		row("renstra usage", "token & cost usage report")
		row("renstra config path", "show active $RENSTRA_HOME directory")
		row("renstra help [--lang id|en]", "show this help message")
		row("renstra version", "show binary version")
		fmt.Fprintf(out, "\n%sTip: set RENSTRA_LANG=en or pass --lang en for English.%s\n", cDim, cReset)
		return
	}

	section("🚀 Gateway & Chat")
	row("renstra setup", "wizard konfigurasi awal (token bot, API key, dll.)")
	row("renstra serve", "jalankan gateway Telegram + cron scheduler + MCP")
	row("renstra restart [--start]", "restart service/server renstra yang sedang berjalan (jika ada)")
	row("renstra chat [model]", "sesi chat interaktif di terminal REPL")
	section("🛠 Manajemen & Tool")
	row("renstra update [check]", "periksa & perbarui binary renstra ke rilis terbaru")
	row("renstra migrate hermes [flags]", "impor memori, skill, config & riwayat dari ~/.hermes")
	row("renstra model list", "daftar provider aktif, model & alias")
	row("renstra model set <ref>", "ubah model default di config.yaml (model / provider:model / alias)")
	row("renstra skill list [--global]", "lihat skill terdaftar")
	row("renstra skill add <dir|url>", "impor skill dari direktori lokal atau git URL")
	row("renstra skill add <n> <d> <b>", "tambah skill baru secara langsung")
	row("renstra skill rm <n> [--global]", "hapus skill")
	row("renstra mcp list", "daftar server MCP terdaftar")
	section("📊 Info & Bantuan")
	row("renstra usage", "laporan pemakaian token & biaya")
	row("renstra config path", "lokasi direktori data ($RENSTRA_HOME)")
	row("renstra help [--lang id|en]", "tampilkan panduan ini")
	row("renstra version", "tampilkan versi binary")
	fmt.Fprintf(out, "\n%sTips: gunakan --lang en atau RENSTRA_LANG=en untuk bahasa Inggris.%s\n", cDim, cReset)
}
