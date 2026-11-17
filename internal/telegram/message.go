package telegram

import (
	"strconv"
	"strings"
)

// SplitMessage cuts s into chunks of at most limit runes, preferring newline
// boundaries. No chunk is empty and none exceeds limit.
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
		if chunk := strings.TrimRight(string(r[:cut]), "\n"); chunk != "" {
			out = append(out, chunk)
		}
		r = r[cut:]
		for len(r) > 0 && r[0] == '\n' {
			r = r[1:]
		}
	}
	return out
}

const helpText = "Halo! Saya renstra, asisten AI kamu.\n" +
	"Perintah yang tersedia:\n" +
	"/help - bantuan ini\n" +
	"/new atau /reset - mulai sesi baru (potong riwayat lama)\n" +
	"/stop - hentikan tugas/turn yang sedang berjalan\n" +
	"/model - cek atau ganti model aktif (/model <nama>)\n" +
	"/skills - daftar skill aktif (/skills reload untuk muat ulang)\n" +
	"/memory - lihat catatan memori & profil user\n" +
	"/cron - kelola jadwal otomatis (/cron list|add|toggle|rm)\n" +
	"/usage - cek token dan estimasi biaya hari ini\n" +
	"/insights - ringkasan pemakaian harian, 7 hari, dan 30 hari\n" +
	"/compact - paksa kompaksi ringkasan riwayat sekarang\n\n" +
	"Kirim pesan teks, dokumen, atau foto untuk mulai tugas."

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
