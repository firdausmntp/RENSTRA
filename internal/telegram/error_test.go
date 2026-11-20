package telegram

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFormatError_UserReported401(t *testing.T) {
	rawErr := errors.New(`provider https://eay.ganigani.web.id/v1: HTTP 401: {"error":{"code":"invalid_api_key","message":"Authentication required. Provide an API key via Authorization: Bearer <key> header or ?key=<key> query parameter.","type":"authentication_error"}}`)

	formatted := FormatError(rawErr)

	if !strings.Contains(formatted, "HTTP 401") {
		t.Errorf("expected 'HTTP 401' in formatted error, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "https://eay.ganigani.web.id/v1") {
		t.Errorf("expected endpoint in formatted error, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "invalid_api_key") {
		t.Errorf("expected error code 'invalid_api_key', got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Authentication required") {
		t.Errorf("expected provider message, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "renstra setup") {
		t.Errorf("expected actionable setup tip, got:\n%s", formatted)
	}

	html := markdownToHTML(formatted)
	if !strings.Contains(html, "<blockquote>") {
		t.Errorf("expected blockquote in rendered HTML, got:\n%s", html)
	}
	if !strings.Contains(html, "<b>Autentikasi Provider Gagal (HTTP 401)</b>") {
		t.Errorf("expected bold header in rendered HTML, got:\n%s", html)
	}
}

func TestFormatError_RateLimit429(t *testing.T) {
	rawErr := errors.New(`provider https://api.openai.com/v1: HTTP 429: {"error":{"message":"You exceeded your current quota.","type":"insufficient_quota","code":"insufficient_quota"}}`)

	formatted := FormatError(rawErr)
	if !strings.Contains(formatted, "HTTP 429") {
		t.Errorf("expected 'HTTP 429', got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "insufficient_quota") {
		t.Errorf("expected code 'insufficient_quota', got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "/model") {
		t.Errorf("expected /model recommendation, got:\n%s", formatted)
	}
}

func TestFormatError_NotFound404(t *testing.T) {
	rawErr := errors.New(`provider https://api.openai.com/v1: HTTP 404: {"error":{"message":"The model 'gpt-unknown' does not exist","code":"model_not_found"}}`)

	formatted := FormatError(rawErr)
	if !strings.Contains(formatted, "HTTP 404") {
		t.Errorf("expected 'HTTP 404', got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "model_not_found") {
		t.Errorf("expected code 'model_not_found', got:\n%s", formatted)
	}
}

func TestFormatError_ServerError502(t *testing.T) {
	rawErr := errors.New(`provider https://eay.ganigani.web.id/v1: HTTP 502: <html><head><title>502 Bad Gateway</title></head></html>`)

	formatted := FormatError(rawErr)
	if !strings.Contains(formatted, "HTTP 502") {
		t.Errorf("expected 'HTTP 502', got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Gangguan") {
		t.Errorf("expected 'Gangguan' description, got:\n%s", formatted)
	}
}

func TestFormatError_TimeoutAndNetwork(t *testing.T) {
	timeoutErr := fmt.Errorf("context deadline exceeded")
	if !strings.Contains(FormatError(timeoutErr), "Timeout") {
		t.Errorf("expected timeout notice, got: %s", FormatError(timeoutErr))
	}

	netErr := fmt.Errorf("dial tcp 127.0.0.1:8080: connect: connection refused")
	if !strings.Contains(FormatError(netErr), "Network Error") {
		t.Errorf("expected network notice, got: %s", FormatError(netErr))
	}
}
