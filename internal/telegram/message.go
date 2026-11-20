package telegram

import (
	"strconv"
	"strings"
)

// SplitMessage cuts s into chunks of at most limit runes, preferring newline
// boundaries. If a cut lands inside a fenced code block (```lang), the block is
// cleanly closed in the current chunk and re-opened in the next chunk so
// markdown rendering and syntax highlighting are never broken.
func SplitMessage(s string, limit int) []string {
	if limit <= 0 {
		limit = MaxMessageLength
	}
	r := []rune(s)
	var out []string
	for len(r) > 0 {
		if len(r) <= limit {
			if chunk := strings.TrimRight(string(r), "\n"); chunk != "" {
				out = append(out, chunk)
			}
			break
		}
		cut := limit
		for i := limit - 1; i > 0; i-- {
			if r[i] == '\n' {
				cut = i + 1
				break
			}
		}
		chunk := strings.TrimRight(string(r[:cut]), "\n")
		rem := r[cut:]
		for len(rem) > 0 && rem[0] == '\n' {
			rem = rem[1:]
		}

		inBlock, lang := findOpenCodeBlock(chunk)
		if inBlock {
			chunk += "\n```"
			rem = append([]rune("```"+lang+"\n"), rem...)
		}

		if chunk != "" {
			out = append(out, chunk)
		}
		r = rem
	}
	return out
}

func findOpenCodeBlock(s string) (bool, string) {
	inBlock := false
	blockLang := ""
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inBlock {
				inBlock = true
				blockLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			} else {
				inBlock = false
				blockLang = ""
			}
		}
	}
	return inBlock, blockLang
}

// startText is the /start welcome: identity, capabilities, quick start, and a
// compact command menu. Only markdown supported by markdownToHTML is used
// (bold, italic, inline code, fenced blocks, blockquotes, links).
const startText = "⚡ **RENSTRA** • Autonomous AI Agent\n" +
	"*Self-hosted • Telegram-first • Single-binary Go*\n\n" +
	"Halo! Saya **RENSTRA**, asisten AI mandiri yang hidup langsung di server kamu — bukan sekadar chatbot. Saya bekerja nyata: eksekusi terminal, edit kode, riset web, sampai tugas terjadwal.\n\n" +
	"🛠 **Kapabilitas**\n" +
	"• 💻 Terminal & scripting langsung di server\n" +
	"• 📂 Baca / tulis / edit file dengan checkpoint aman\n" +
	"• 🌐 Riset web real-time (fetch + search)\n" +
	"• 🖼️ Vision: pahami foto & dokumen yang kamu kirim\n" +
	"• 🎙️ Transkripsi voice note otomatis\n" +
	"• ⏰ Cron scheduler + subagent untuk tugas berat\n" +
	"• 🧠 Memori persisten & skill yang bisa belajar sendiri\n\n" +
	"🚀 **Mulai cepat**\n" +
	"> Cukup kirim pesan biasa, misalnya:\n" +
	"> \"buatkan script backup folder /var/www setiap malam\"\n" +
	"> Lampirkan foto, dokumen, atau voice note — langsung saya proses.\n\n" +
	"📌 **Perintah cepat**\n" +
	"• `/new` / `/restart` sesi baru • `/stop` hentikan tugas\n" +
	"• `/model` ganti model • `/skills` daftar skill\n" +
	"• `/cron` jadwal otomatis • `/usage` biaya hari ini\n\n" +
	"💡 *Ketik /help untuk panduan lengkap. Selamat bekerja!*"

// helpText is the /help full command reference, grouped by category.
const helpText = "📖 **RENSTRA — Panduan Lengkap**\n\n" +
	"💬 **Sesi & Kontrol**\n" +
	"• `/new`, `/reset`, atau `/restart` — mulai sesi baru; riwayat diputus, memori di-freeze ulang\n" +
	"• `/stop` — hentikan tugas yang sedang berjalan\n" +
	"• `/status` — ringkasan status sesi, model, konteks & biaya\n" +
	"• `/compact` — ringkas riwayat sekarang supaya konteks lega\n\n" +
	"🧠 **Model & Kecerdasan**\n" +
	"• `/model` — model aktif + daftar provider, model & alias\n" +
	"• `/model <nama>` — ganti model (nama, alias, atau provider:model)\n" +
	"• `/skills` — daftar skill terdaftar (/skills <nama> untuk isi lengkap)\n" +
	"• `/skills reload` — scan ulang skill dari disk\n" +
	"• `/memory` — lihat memori jangka panjang & profil user\n\n" +
	"⏰ **Otomasi (Cron)**\n" +
	"• `/cron list` — lihat semua jadwal\n" +
	"• `/cron add <schedule> | <prompt>` — tambah jadwal baru\n" +
	"• `/cron toggle <id>` — aktif / nonaktifkan jadwal\n" +
	"• `/cron rm <id>` — hapus jadwal\n\n" +
	"Contoh jadwal harian jam 9 pagi:\n" +
	"```\n/cron add 0 9 * * * | ringkas berita AI pagi ini\n```\n\n" +
	"📊 **Analitik Pemakaian**\n" +
	"• `/usage` — token & estimasi biaya hari ini\n" +
	"• `/insights` — rekap hari ini, 7 & 30 hari terakhir\n\n" +
	"ℹ️ **Bantuan & Info**\n" +
	"• `/version` — versi binary RENSTRA\n" +
	"• `/help` — panduan ini\n" +
	"• `/start` — pesan sambutan & mulai cepat\n\n" +
	"> Kirim teks, foto, dokumen, atau voice note kapan saja — tanpa perintah.\n" +
	"> Interrupt turn yang sedang jalan dengan /stop, lalu lanjutkan seperti biasa."

func command(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return ""
	}
	cmd, _, _ := strings.Cut(fields[0], "@")
	return cmd
}

func parseCallback(data string) (action string, id int64, ok bool) {
	action, raw, found := strings.Cut(data, ":")
	if !found {
		return "", 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return action, n, true
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
