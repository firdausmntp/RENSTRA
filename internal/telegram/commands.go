package telegram

import "context"

// DefaultCommands returns the slash command menu registered by renstra serve.
func DefaultCommands() []BotCommand {
	return []BotCommand{
		{Command: "start", Description: "Mulai & tampilkan sambutan"},
		{Command: "help", Description: "Panduan lengkap semua perintah"},
		{Command: "new", Description: "Mulai sesi baru (potong riwayat lama)"},
		{Command: "restart", Description: "Restart sesi & potong riwayat lama"},
		{Command: "stop", Description: "Hentikan tugas yang sedang berjalan"},
		{Command: "approval", Description: "Atur mode persetujuan (prompt/auto/deny)"},
		{Command: "config", Description: "Ubah pengaturan default bot (compact/limit/dll)"},
		{Command: "model", Description: "Cek atau ganti model aktif"},
		{Command: "fallback", Description: "Status & kontrol Smart Fallback provider"},
		{Command: "skills", Description: "Daftar skill aktif (/skills reload)"},
		{Command: "memory", Description: "Lihat & kelola memori & profil user"},
		{Command: "remember", Description: "Simpan catatan langsung ke memori"},
		{Command: "cron", Description: "Kelola jadwal otomatis"},
		{Command: "export", Description: "Export riwayat percakapan ke Markdown"},
		{Command: "logs", Description: "Unduh log error & diagnostik bot"},
		{Command: "status", Description: "Info status sesi, model, konteks & biaya"},
		{Command: "usage", Description: "Cek token & estimasi biaya hari ini"},
		{Command: "insights", Description: "Ringkasan pemakaian harian, 7 & 30 hari"},
		{Command: "compact", Description: "Kompaksi ringkasan riwayat sekarang"},
		{Command: "version", Description: "Tampilkan versi binary RENSTRA"},
	}
}

// SetMyCommands registers the bot's slash command menu via Telegram setMyCommands.
func (b *Bot) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	_, err := b.post(ctx, "setMyCommands", map[string]any{"commands": cmds})
	return err
}
