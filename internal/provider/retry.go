package provider

import (
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// Retryable reports whether an HTTP status deserves a retry. 429 and 5xx are
// transient; 4xx (bad request, auth, context overflow) are not.
func Retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// Backoff returns the wait before attempt n (1-based), honoring Retry-After
// when the server sent one, and otherwise using exponential backoff with
// jitter capped at 20s.
func Backoff(attempt int, retryAfter string) time.Duration {
	if retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs >= 0 && secs <= 120 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(retryAfter); err == nil {
			if d := time.Until(t); d > 0 && d <= 2*time.Minute {
				return d
			}
		}
	}
	if attempt < 1 {
		attempt = 1
	}
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	if base > 20*time.Second {
		base = 20 * time.Second
	}
	// Jitter avoids a thundering herd when several chats retry together.
	return base + time.Duration(rand.Int63n(int64(base/2+1)))
}

// MaxAttempts is how many times a single request is tried before giving up.
const MaxAttempts = 4
