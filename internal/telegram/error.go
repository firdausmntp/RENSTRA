package telegram

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	httpStatusRE   = regexp.MustCompile(`(?i)HTTP\s+(\d{3})`)
	providerURLRE  = regexp.MustCompile(`https?://[^\s:]+(?::\d+)?(?:/[^\s]*)?`)
	knownProviders = []string{"anthropic", "google", "gemini", "openai", "openrouter", "groq", "ollama", "mistral", "xai"}
	secretKeyRE    = regexp.MustCompile(`(?i)(sk-[a-zA-Z0-9_\-]{8,}|ghp_[a-zA-Z0-9]{8,}|xox[baprs]-[a-zA-Z0-9_\-]{8,})`)
)

func redactSecrets(s string) string {
	return secretKeyRE.ReplaceAllStringFunc(s, func(m string) string {
		if len(m) > 7 {
			return m[:4] + "****"
		}
		return "****"
	})
}

type parsedError struct {
	StatusCode int
	Endpoint   string
	ErrorCode  string
	ErrorType  string
	Message    string
	RawJSON    bool
}

func parseError(err error) parsedError {
	if err == nil {
		return parsedError{}
	}
	s := err.Error()
	pe := parsedError{}

	// 1. Status Code
	if m := httpStatusRE.FindStringSubmatch(s); len(m) > 1 {
		if code, e := strconv.Atoi(m[1]); e == nil {
			pe.StatusCode = code
		}
	}

	// 2. Endpoint / Provider
	if u := providerURLRE.FindString(s); u != "" {
		pe.Endpoint = strings.TrimRight(u, "/:")
	} else {
		lower := strings.ToLower(s)
		for _, kp := range knownProviders {
			if strings.Contains(lower, kp) {
				pe.Endpoint = kp
				break
			}
		}
	}

	// 3. JSON Payload Extraction
	firstBrace := strings.Index(s, "{")
	lastBrace := strings.LastIndex(s, "}")
	if firstBrace >= 0 && lastBrace > firstBrace {
		jsonStr := s[firstBrace : lastBrace+1]
		var payload struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    any    `json:"code"`
				Status  string `json:"status"`
			} `json:"error"`
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Status  string `json:"status"`
		}
		if json.Unmarshal([]byte(jsonStr), &payload) == nil {
			pe.RawJSON = true
			if payload.Error.Message != "" {
				pe.Message = strings.TrimSpace(payload.Error.Message)
			} else if payload.Message != "" {
				pe.Message = strings.TrimSpace(payload.Message)
			}

			if payload.Error.Code != nil {
				pe.ErrorCode = fmt.Sprintf("%v", payload.Error.Code)
			} else if payload.Code != nil {
				pe.ErrorCode = fmt.Sprintf("%v", payload.Code)
			} else if payload.Error.Status != "" {
				pe.ErrorCode = payload.Error.Status
			}

			if payload.Error.Type != "" {
				pe.ErrorType = payload.Error.Type
			} else if payload.Type != "" {
				pe.ErrorType = payload.Type
			}
		}
	}

	// 4. Jika pesan kosong, gunakan pesan non-JSON yang dibersihkan
	if pe.Message == "" {
		cleaned := strings.TrimPrefix(s, "error: ")
		if idx := strings.Index(cleaned, "<!DOCTYPE"); idx >= 0 {
			cleaned = strings.TrimSpace(cleaned[:idx])
		}
		if idx := strings.Index(cleaned, "<html"); idx >= 0 {
			cleaned = strings.TrimSpace(cleaned[:idx])
		}
		pe.Message = strings.TrimSpace(cleaned)
	}
	pe.Message = redactSecrets(pe.Message)

	return pe
}

// FormatError formats runtime errors into clean, informative, and visually pleasing
// Markdown messages for Telegram.
func FormatError(err error) string {
	if err == nil {
		return ""
	}

	pe := parseError(err)
	var b strings.Builder

	switch {
	case pe.StatusCode == 401:
		b.WriteString("⚠️ **Autentikasi Provider Gagal (HTTP 401)**\n\n")
		if pe.Endpoint != "" {
			fmt.Fprintf(&b, "• **Endpoint:** `%s`\n", pe.Endpoint)
		}
		if pe.ErrorCode != "" {
			fmt.Fprintf(&b, "• **Kode Error:** `%s`\n", pe.ErrorCode)
		}
		b.WriteString("• **Penyebab:** API Key tidak valid, belum diset, atau ditolak provider.\n\n")

		if pe.Message != "" {
			b.WriteString("> " + formatQuote(pe.Message) + "\n\n")
		}

		b.WriteString("💡 **Tips Solusi:**\n")
		b.WriteString("1. Jalankan `renstra setup` di terminal untuk memperbarui API key.\n")
		b.WriteString("2. Atau isi API key ke file `.env` di direktori config RENSTRA.\n")
		b.WriteString("3. Pastikan environment variable sudah di-export di terminal.\n")
		b.WriteString("4. Beralih ke model atau provider lain: `/model <nama>`")

	case pe.StatusCode == 403:
		b.WriteString("🚫 **Akses Dilarang oleh Provider (HTTP 403)**\n\n")
		if pe.Endpoint != "" {
			fmt.Fprintf(&b, "• **Endpoint:** `%s`\n", pe.Endpoint)
		}
		if pe.ErrorCode != "" {
			fmt.Fprintf(&b, "• **Kode Error:** `%s`\n", pe.ErrorCode)
		}
		b.WriteString("• **Penyebab:** Akun atau API key tidak memiliki izin untuk fitur/model ini.\n\n")

		if pe.Message != "" {
			b.WriteString("> " + formatQuote(pe.Message) + "\n\n")
		}

		b.WriteString("💡 **Tips Solusi:**\n")
		b.WriteString("1. Periksa izin akun, batasan wilayah (region), atau tier di dashboard provider.\n")
		b.WriteString("2. Ganti model atau provider aktif dengan: `/model <nama>`")

	case pe.StatusCode == 404:
		b.WriteString("🔍 **Model atau Endpoint Tidak Ditemukan (HTTP 404)**\n\n")
		if pe.Endpoint != "" {
			fmt.Fprintf(&b, "• **Endpoint:** `%s`\n", pe.Endpoint)
		}
		if pe.ErrorCode != "" {
			fmt.Fprintf(&b, "• **Kode Error:** `%s`\n", pe.ErrorCode)
		}
		b.WriteString("• **Penyebab:** Nama model tidak terdaftar atau path URL salah.\n\n")

		if pe.Message != "" {
			b.WriteString("> " + formatQuote(pe.Message) + "\n\n")
		}

		b.WriteString("💡 **Tips Solusi:**\n")
		b.WriteString("1. Cek daftar provider & model terdaftar dengan perintah `/model`.\n")
		b.WriteString("2. Periksa kembali `base_url` dan `default_model` di `config.yaml`.\n")
		b.WriteString("3. Beralih ke model lain dengan: `/model <nama>`")

	case pe.StatusCode == 429:
		b.WriteString("⏳ **Batas Kuota / Rate Limit Terlampaui (HTTP 429)**\n\n")
		if pe.Endpoint != "" {
			fmt.Fprintf(&b, "• **Endpoint:** `%s`\n", pe.Endpoint)
		}
		if pe.ErrorCode != "" {
			fmt.Fprintf(&b, "• **Kode Error:** `%s`\n", pe.ErrorCode)
		}
		b.WriteString("• **Penyebab:** Batas frekuensi permintaan (RPM/TPM) terlampaui atau saldo kuota habis.\n\n")

		if pe.Message != "" {
			b.WriteString("> " + formatQuote(pe.Message) + "\n\n")
		}

		b.WriteString("💡 **Tips Solusi:**\n")
		b.WriteString("1. Tunggu 1–2 menit sebelum mengirim pesan kembali.\n")
		b.WriteString("2. Periksa kuota atau top-up saldo billing di dashboard provider Anda.\n")
		b.WriteString("3. Beralih ke model atau provider alternatif dengan: `/model <nama>`")

	case pe.StatusCode >= 500:
		fmt.Fprintf(&b, "🔥 **Layanan Provider Sedang Gangguan (HTTP %d)**\n\n", pe.StatusCode)
		if pe.Endpoint != "" {
			fmt.Fprintf(&b, "• **Endpoint:** `%s`\n", pe.Endpoint)
		}
		b.WriteString("• **Penyebab:** Server penyedia AI sedang overload, down, atau dalam pemeliharaan.\n\n")

		if pe.Message != "" {
			b.WriteString("> " + formatQuote(pe.Message) + "\n\n")
		}

		b.WriteString("💡 **Tips Solusi:**\n")
		b.WriteString("1. Tunggu sesaat dan coba kirim ulang pesan Anda.\n")
		b.WriteString("2. Gunakan provider cadangan lain dengan perintah: `/model <nama>`")

	case strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") || strings.Contains(strings.ToLower(err.Error()), "timeout"):
		b.WriteString("⏱️ **Waktu Permintaan Habis (Timeout)**\n\n")
		if pe.Endpoint != "" {
			fmt.Fprintf(&b, "• **Endpoint:** `%s`\n", pe.Endpoint)
		}
		b.WriteString("• **Penyebab:** Provider tidak memberikan respons sebelum batas waktu habis.\n\n")
		b.WriteString("💡 **Tips Solusi:**\n")
		b.WriteString("1. Coba ulangi pertanyaan atau instruksi dengan kalimat lebih ringkas.\n")
		b.WriteString("2. Periksa kestabilan koneksi internet ke provider.")

	case strings.Contains(strings.ToLower(err.Error()), "connection refused") || strings.Contains(strings.ToLower(err.Error()), "no such host"):
		b.WriteString("🌐 **Gagal Terhubung ke Provider (Network Error)**\n\n")
		if pe.Endpoint != "" {
			fmt.Fprintf(&b, "• **Target:** `%s`\n", pe.Endpoint)
		}
		b.WriteString("• **Penyebab:** Host tidak dapat dihubungi atau server lokal tidak berjalan.\n\n")
		b.WriteString("💡 **Tips Solusi:**\n")
		b.WriteString("1. Periksa koneksi internet VPS/server Anda.\n")
		b.WriteString("2. Jika menggunakan model lokal (misal Ollama), pastikan servicenya aktif.")

	default:
		b.WriteString("⚠️ **Gagal Memproses Permintaan**\n\n")
		if pe.Endpoint != "" {
			fmt.Fprintf(&b, "• **Endpoint:** `%s`\n", pe.Endpoint)
		}
		if pe.Message != "" {
			b.WriteString("> " + formatQuote(pe.Message) + "\n\n")
		}
		b.WriteString("💡 **Bantuan:**\n")
		b.WriteString("• Ketik `/help` untuk panduan perintah bot.\n")
		b.WriteString("• Gunakan `/reset` jika ingin memulai sesi baru dan memotong riwayat lama.")
	}

	return b.String()
}

func formatQuote(s string) string {
	lines := strings.Split(s, "\n")
	var res []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			res = append(res, l)
		}
	}
	return strings.Join(res, "\n> ")
}
