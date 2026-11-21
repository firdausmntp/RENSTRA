package agent

import (
	"sort"
	"strings"
	"sync"
	"time"

	"renstra/internal/provider"
)

// FallbackErrKind classifies why a provider target failed so smart fallback
// can pick an appropriate cooldown and choose compatible backup models.
type FallbackErrKind string

const (
	errKindTransient FallbackErrKind = "transient"
	errKindAuth      FallbackErrKind = "auth"
	errKindContext   FallbackErrKind = "context"
	errKindOther     FallbackErrKind = "other"
)

// FallbackTargetStatus describes the current circuit-breaker health of a target.
type FallbackTargetStatus struct {
	Provider      string
	Model         string
	Failures      int
	CooldownLeft  time.Duration
	LastError     string
	LastKind      FallbackErrKind
	ContextWindow int
	Vision        bool
}

type targetHealth struct {
	failures      int
	cooldownUntil time.Time
	lastError     string
	lastKind      FallbackErrKind
}

type fallbackHealth struct {
	mu    sync.Mutex
	items map[string]*targetHealth
	nowFn func() time.Time
}

func newFallbackHealth() *fallbackHealth {
	return &fallbackHealth{
		items: map[string]*targetHealth{},
		nowFn: time.Now,
	}
}

func (h *fallbackHealth) now() time.Time {
	if h != nil && h.nowFn != nil {
		return h.nowFn()
	}
	return time.Now()
}

func targetKey(t FallbackTarget) string {
	pname := "unknown"
	if t.Provider != nil {
		pname = t.Provider.Name()
	}
	return pname + ":" + t.Model
}

func classifyFallbackErr(err error) FallbackErrKind {
	if err == nil {
		return errKindOther
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "context length"),
		strings.Contains(s, "context_length"),
		strings.Contains(s, "maximum context"),
		strings.Contains(s, "too many tokens"),
		strings.Contains(s, "prompt is too long"),
		strings.Contains(s, "input is too long"),
		strings.Contains(s, "exceeds the context"):
		return errKindContext
	case strings.Contains(s, "http 401"),
		strings.Contains(s, "http 402"),
		strings.Contains(s, "http 403"),
		strings.Contains(s, "http 404"),
		strings.Contains(s, "invalid api key"),
		strings.Contains(s, "unauthorized"),
		strings.Contains(s, "insufficient"),
		strings.Contains(s, "quota"),
		strings.Contains(s, "billing"):
		return errKindAuth
	case strings.Contains(s, "http 429"),
		strings.Contains(s, "http 500"),
		strings.Contains(s, "http 502"),
		strings.Contains(s, "http 503"),
		strings.Contains(s, "http 504"),
		strings.Contains(s, "429"),
		strings.Contains(s, "503"),
		strings.Contains(s, "rate limit"),
		strings.Contains(s, "service unavailable"),
		strings.Contains(s, "timeout"),
		strings.Contains(s, "deadline exceeded"),
		strings.Contains(s, "connection refused"),
		strings.Contains(s, "connection reset"),
		strings.Contains(s, "unexpected end of json"),
		strings.Contains(s, "eof"),
		strings.Contains(s, "respons kosong"):
		return errKindTransient
	default:
		return errKindOther
	}
}

func (h *fallbackHealth) recordSuccess(t FallbackTarget) {
	if h == nil {
		return
	}
	key := targetKey(t)
	h.mu.Lock()
	delete(h.items, key)
	h.mu.Unlock()
}

func (h *fallbackHealth) recordFailure(t FallbackTarget, err error) FallbackErrKind {
	kind := classifyFallbackErr(err)
	if h == nil || kind == errKindContext {
		return kind
	}
	key := targetKey(t)
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.items[key]
	if st == nil {
		st = &targetHealth{}
		h.items[key] = st
	}
	st.failures++
	st.lastKind = kind
	if err != nil {
		msg := err.Error()
		if len(msg) > 160 {
			msg = msg[:157] + "..."
		}
		st.lastError = msg
	}

	var cooldown time.Duration
	switch kind {
	case errKindAuth:
		cooldown = 5 * time.Minute
	case errKindTransient:
		shift := st.failures - 1
		if shift > 3 {
			shift = 3
		}
		cooldown = 30 * time.Second * time.Duration(1<<uint(shift))
		if cooldown > 5*time.Minute {
			cooldown = 5 * time.Minute
		}
	default:
		cooldown = 15 * time.Second
	}
	st.cooldownUntil = now.Add(cooldown)
	return kind
}

func (h *fallbackHealth) cooldownUntil(t FallbackTarget, now time.Time) (time.Time, bool) {
	if h == nil {
		return time.Time{}, false
	}
	key := targetKey(t)
	h.mu.Lock()
	st := h.items[key]
	h.mu.Unlock()
	if st == nil || !st.cooldownUntil.After(now) {
		return time.Time{}, false
	}
	return st.cooldownUntil, true
}

func (h *fallbackHealth) reset() {
	if h == nil {
		return
	}
	h.mu.Lock()
	clear(h.items)
	h.mu.Unlock()
}

func (h *fallbackHealth) statusFor(t FallbackTarget) FallbackTargetStatus {
	pname := "unknown"
	if t.Provider != nil {
		pname = t.Provider.Name()
	}
	out := FallbackTargetStatus{
		Provider:      pname,
		Model:         t.Model,
		ContextWindow: t.ContextWindow,
		Vision:        t.Vision,
	}
	if h == nil {
		return out
	}
	now := h.now()
	h.mu.Lock()
	st := h.items[targetKey(t)]
	if st != nil {
		out.Failures = st.failures
		out.LastError = st.lastError
		out.LastKind = st.lastKind
		if st.cooldownUntil.After(now) {
			out.CooldownLeft = st.cooldownUntil.Sub(now)
		}
	}
	h.mu.Unlock()
	return out
}

func hasImageAttachments(history []provider.Message) bool {
	for _, m := range history {
		if len(m.Attachments) > 0 {
			return true
		}
	}
	return false
}

// rankSmartCandidates deduplicates primary + fallbacks and orders them by:
// 1) Not in circuit-breaker cooldown (or earliest expiry if all cooling down)
// 2) Vision capability match when request includes images
// 3) Context window sufficiency for estimated request tokens
// 4) Original configured order (primary before fallbacks).
func rankSmartCandidates(
	primary FallbackTarget,
	fallbacks []FallbackTarget,
	health *fallbackHealth,
	needVision bool,
	estTokens int,
) []FallbackTarget {
	raw := make([]FallbackTarget, 0, 1+len(fallbacks))
	raw = append(raw, primary)
	raw = append(raw, fallbacks...)

	seen := map[string]bool{}
	type scored struct {
		target        FallbackTarget
		origIdx       int
		inCooldown    bool
		cooldownUntil time.Time
		visionMiss    bool
		ctxTooSmall   bool
	}
	var list []scored
	now := health.now()
	for idx, t := range raw {
		if t.Provider == nil || t.Model == "" {
			continue
		}
		k := targetKey(t)
		if seen[k] {
			continue
		}
		seen[k] = true
		until, cooling := health.cooldownUntil(t, now)
		ctxTooSmall := estTokens > 0 && t.ContextWindow > 0 && estTokens >= int(float64(t.ContextWindow)*0.95)
		list = append(list, scored{
			target:        t,
			origIdx:       idx,
			inCooldown:    cooling,
			cooldownUntil: until,
			visionMiss:    needVision && !t.Vision,
			ctxTooSmall:   ctxTooSmall,
		})
	}

	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.inCooldown != b.inCooldown {
			return !a.inCooldown
		}
		if a.inCooldown && !a.cooldownUntil.Equal(b.cooldownUntil) {
			return a.cooldownUntil.Before(b.cooldownUntil)
		}
		if a.visionMiss != b.visionMiss {
			return !a.visionMiss
		}
		if a.ctxTooSmall != b.ctxTooSmall {
			return !a.ctxTooSmall
		}
		return a.origIdx < b.origIdx
	})

	out := make([]FallbackTarget, len(list))
	for i, s := range list {
		out[i] = s.target
	}
	return out
}
