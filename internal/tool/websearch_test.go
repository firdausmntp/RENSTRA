package tool_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"renstra/internal/tool"
)

const ddgMockHTML = `<!DOCTYPE html>
<html>
<body>
<div class="result results_links">
  <h2 class="result__title">
    <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org%2F&rut=1">The <b>Go</b> Programming Language</a>
  </h2>
  <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org%2F&rut=1">Go is an open source programming language that makes it easy to build simple, reliable, and efficient software.</a>
</div>
<div class="result results_links">
  <h2 class="result__title">
    <a class="result__a" href="https://github.com/golang/go">GitHub - golang/go</a>
  </h2>
  <a class="result__snippet" href="https://github.com/golang/go">The Go programming language source code.</a>
</div>
</body>
</html>`

func TestWebSearchParsesHits(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q != "golang" {
			t.Fatalf("query salah: %q", q)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(ddgMockHTML))
	}))
	defer ts.Close()

	ws := &tool.WebSearch{
		BaseURL: ts.URL,
		Client:  ts.Client(),
	}

	out, err := ws.Execute(context.Background(), json.RawMessage(`{"query":"golang","limit":2}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.HasPrefix(out, "[untrusted]") || !strings.HasSuffix(out, "[/untrusted]") {
		t.Fatalf("output harus dibungkus [untrusted]: %q", out)
	}
	if !strings.Contains(out, "The Go Programming Language") {
		t.Fatalf("tidak menemukan judul pertama: %q", out)
	}
	// Direct unwrapped link from uddg
	if !strings.Contains(out, "https://golang.org/") {
		t.Fatalf("link harus di-unwrapper ke target asli: %q", out)
	}
	if !strings.Contains(out, "GitHub - golang/go") {
		t.Fatalf("tidak menemukan judul kedua: %q", out)
	}
	if !strings.Contains(out, "efficient software") {
		t.Fatalf("snippet hilang: %q", out)
	}
}
