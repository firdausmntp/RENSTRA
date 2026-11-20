package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatCLIError_UserReported401(t *testing.T) {
	rawErr := errors.New(`provider https://eay.ganigani.web.id/v1: HTTP 401: {"error":{"code":"invalid_api_key","message":"Authentication required. Provide an API key via Authorization: Bearer <key> header or ?key=<key> query parameter.","type":"authentication_error"}}`)

	out := FormatCLIError(rawErr)
	if !strings.Contains(out, "HTTP 401") {
		t.Errorf("expected HTTP 401 in output:\n%s", out)
	}
	if !strings.Contains(out, "https://eay.ganigani.web.id/v1") {
		t.Errorf("expected endpoint in output:\n%s", out)
	}
	if !strings.Contains(out, "invalid_api_key") {
		t.Errorf("expected error code invalid_api_key in output:\n%s", out)
	}
	if !strings.Contains(out, "Authentication required") {
		t.Errorf("expected provider message in output:\n%s", out)
	}
	if !strings.Contains(out, "renstra setup") {
		t.Errorf("expected renstra setup in tips:\n%s", out)
	}
}

func TestFormatCLIError_RateLimit429(t *testing.T) {
	rawErr := errors.New(`provider https://api.openai.com/v1: HTTP 429: {"error":{"message":"Quota exceeded","code":"insufficient_quota"}}`)
	out := FormatCLIError(rawErr)
	if !strings.Contains(out, "HTTP 429") {
		t.Errorf("expected HTTP 429 in output:\n%s", out)
	}
	if !strings.Contains(out, "insufficient_quota") {
		t.Errorf("expected insufficient_quota in output:\n%s", out)
	}
}

func TestFormatCLIError_EndpointTrailingColon(t *testing.T) {
	rawErr := errors.New(`provider https://api.example.com/v1: HTTP 401: {"error":{"message":"bad key","code":"invalid_api_key"}}`)
	out := FormatCLIError(rawErr)
	if strings.Contains(out, "v1:") {
		t.Errorf("endpoint tidak boleh menyertakan titik dua ekor:\n%s", out)
	}
	if !strings.Contains(out, "https://api.example.com/v1") {
		t.Errorf("endpoint hilang:\n%s", out)
	}
}
