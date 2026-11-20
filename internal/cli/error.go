package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	cliHTTPStatusRE   = regexp.MustCompile(`(?i)HTTP\s+(\d{3})`)
	cliProviderURLRE  = regexp.MustCompile(`https?://[^\s:]+(?::\d+)?(?:/[^\s]*)?`)
	cliKnownProviders = []string{"anthropic", "google", "gemini", "openai", "openrouter", "groq", "ollama", "mistral", "xai"}
	cliSecretRE       = regexp.MustCompile(`(?i)(sk-[a-zA-Z0-9_\-]{8,}|ghp_[a-zA-Z0-9]{8,}|xox[baprs]-[a-zA-Z0-9_\-]{8,})`)
)

func redactSecrets(s string) string {
	return cliSecretRE.ReplaceAllStringFunc(s, func(m string) string {
		if len(m) > 7 {
			return m[:4] + "****"
		}
		return "****"
	})
}

type cliParsedError struct {
	StatusCode int
	Endpoint   string
	ErrorCode  string
	Message    string
}

func parseCLIError(err error) cliParsedError {
	if err == nil {
		return cliParsedError{}
	}
	s := err.Error()
	pe := cliParsedError{}

	if m := cliHTTPStatusRE.FindStringSubmatch(s); len(m) > 1 {
		if code, e := strconv.Atoi(m[1]); e == nil {
			pe.StatusCode = code
		}
	}

	if u := cliProviderURLRE.FindString(s); u != "" {
		pe.Endpoint = strings.TrimRight(u, "/:")
	} else {
		lower := strings.ToLower(s)
		for _, kp := range cliKnownProviders {
			if strings.Contains(lower, kp) {
				pe.Endpoint = kp
				break
			}
		}
	}

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
		}
		if json.Unmarshal([]byte(jsonStr), &payload) == nil {
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
			} else if payload.Error.Type != "" {
				pe.ErrorCode = payload.Error.Type
			}
		}
	}

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

// FormatCLIError renders a nicely boxed, colored ANSI error block for the terminal.
func FormatCLIError(err error) string {
	if err == nil {
		return ""
	}

	pe := parseCLIError(err)
	var b strings.Builder

	// ANSI Colors
	cRed := "\033[1;31m"
	cYellow := "\033[1;33m"
	cCyan := "\033[1;36m"
	cWhite := "\033[1;37m"
	cDim := "\033[0;2m"
	cReset := "\033[0m"

	title := "Error Operasi"
	var tips []string

	switch {
	case pe.StatusCode == 401:
		title = "Autentikasi Provider Gagal (HTTP 401)"
		tips = []string{
			"Jalankan `renstra setup` untuk konfigurasi API key baru",
			"Pastikan API key sudah disimpan di ~/.renstra/.env atau di-export di shell",
			"Beralih ke model/provider lain dengan: /model <nama>",
		}
	case pe.StatusCode == 403:
		title = "Akses Dilarang Provider (HTTP 403)"
		tips = []string{
			"Periksa hak akses, regional restrictions, atau tier akun Anda",
			"Beralih ke model/provider lain dengan: /model <nama>",
		}
	case pe.StatusCode == 404:
		title = "Model / Endpoint Tidak Ditemukan (HTTP 404)"
		tips = []string{
			"Periksa daftar model aktif menggunakan perintah: /model",
			"Pastikan Base URL dan nama model di config.yaml sudah benar",
		}
	case pe.StatusCode == 429:
		title = "Batas Kuota / Rate Limit Tercapai (HTTP 429)"
		tips = []string{
			"Tunggu beberapa saat sebelum mengirim permintaan berikutnya",
			"Periksa saldo kredit atau limit RPM/TPM di dashboard provider Anda",
			"Beralih ke provider cadangan dengan: /model <nama>",
		}
	case pe.StatusCode >= 500:
		title = fmt.Sprintf("Layanan Provider Bermasalah (HTTP %d)", pe.StatusCode)
		tips = []string{
			"Server provider AI sedang gangguan atau overload; coba lagi nanti",
			"Beralih sementara ke provider lain dengan: /model <nama>",
		}
	case strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") || strings.Contains(strings.ToLower(err.Error()), "timeout"):
		title = "Waktu Permintaan Habis (Timeout)"
		tips = []string{
			"Ulangi pertanyaan dengan kalimat yang lebih spesifik dan ringkas",
			"Periksa kestabilan jaringan internet server Anda",
		}
	case strings.Contains(strings.ToLower(err.Error()), "connection refused") || strings.Contains(strings.ToLower(err.Error()), "no such host"):
		title = "Koneksi ke Provider Gagal (Network Error)"
		tips = []string{
			"Periksa koneksi internet server/VPS Anda",
			"Jika memakai LLM lokal (Ollama/reverse proxy), pastikan service aktif",
		}
	default:
		tips = []string{
			"Ketik /help untuk panduan perintah atau /new untuk reset sesi",
		}
	}

	dashes := 57 - utf8.RuneCountInString(title)
	if dashes < 1 {
		dashes = 1
	}
	b.WriteString("\n" + cRed + "╭─ " + cWhite + title + " " + cRed + strings.Repeat("─", dashes) + cReset + "\n")
	if pe.Endpoint != "" {
		b.WriteString(cRed + "│ " + cWhite + "Endpoint : " + cCyan + pe.Endpoint + cReset + "\n")
	}
	if pe.ErrorCode != "" {
		b.WriteString(cRed + "│ " + cWhite + "Kode     : " + cYellow + pe.ErrorCode + cReset + "\n")
	}
	if pe.Endpoint != "" || pe.ErrorCode != "" {
		b.WriteString(cRed + "│" + cReset + "\n")
	}

	if pe.Message != "" {
		b.WriteString(cRed + "│ " + cWhite + "Pesan Provider:" + cReset + "\n")
		for _, line := range strings.Split(pe.Message, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				b.WriteString(cRed + "│   " + cDim + line + cReset + "\n")
			}
		}
		b.WriteString(cRed + "│" + cReset + "\n")
	}

	if len(tips) > 0 {
		b.WriteString(cRed + "│ " + cYellow + "Tips Solusi:" + cReset + "\n")
		for _, tip := range tips {
			b.WriteString(cRed + "│   • " + cReset + tip + "\n")
		}
	}

	b.WriteString(cRed + "╰─────────────────────────────────────────────────────────────" + cReset + "\n\n")
	return b.String()
}
