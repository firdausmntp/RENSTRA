package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDetectBlockingSleepAndGuard(t *testing.T) {
	cases := []struct {
		cmd     string
		blocked bool
	}{
		{`sleep 180; pgrep -fa "threads run"`, true},
		{`sleep 200`, true},
		{`echo hi && sleep 2m && ls`, true},
		{`sleep 5 && echo ok`, false},
		{`sleep 30`, false},
		{`nohup sh -c "sleep 180 && echo done" > tmp/out.log 2>&1 &`, false},
		{`echo "sleep 180" && ls`, false},
	}
	for _, tc := range cases {
		// Note: quoted string echo "sleep 180" has quote before sleep, not separator.
		_, got := detectBlockingSleep(tc.cmd)
		if got != tc.blocked {
			t.Errorf("detectBlockingSleep(%q) = %v, want %v", tc.cmd, got, tc.blocked)
		}
	}

	term := NewTerminal(t.TempDir(), 5*time.Second)
	start := time.Now()
	out, err := term.Execute(context.Background(), json.RawMessage(`{"cmd":"sleep 180; echo done"}`))
	if err != nil {
		t.Fatalf("expected non-error refusal message, got err=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("blocking sleep guard took too long: %s", time.Since(start))
	}
	if !strings.Contains(out, "ditolak") || !strings.Contains(out, "cron") {
		t.Fatalf("expected actionable guidance in guard output, got: %s", out)
	}
}
