package agent

import (
	"strings"
	"sync"
	"time"

	"renstra/internal/config"
	"renstra/internal/provider"
)

type chatLock struct {
	mu   sync.Mutex
	refs int
}

type chatLockRegistry struct {
	mu    sync.Mutex
	locks map[int64]*chatLock
}

func newChatLockRegistry() *chatLockRegistry {
	return &chatLockRegistry{locks: map[int64]*chatLock{}}
}

func (reg *chatLockRegistry) acquire(chatID int64) func() {
	reg.mu.Lock()
	l := reg.locks[chatID]
	if l == nil {
		l = &chatLock{}
		reg.locks[chatID] = l
	}
	l.refs++
	reg.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		reg.mu.Lock()
		l.refs--
		if l.refs <= 0 && reg.locks[chatID] == l {
			delete(reg.locks, chatID)
		}
		reg.mu.Unlock()
	}
}

func (r *Runner) lockRegistry() *chatLockRegistry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locks == nil {
		r.locks = newChatLockRegistry()
	}
	return r.locks
}

// Clone returns a shallow copy of Runner with isolated mutable model state
// while sharing stores, tools, fallbacks, logger, and chat locks.
func (r *Runner) Clone() *Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locks == nil {
		r.locks = newChatLockRegistry()
	}
	return &Runner{
		Provider:           r.Provider,
		Tools:              r.Tools,
		Store:              r.Store,
		Model:              r.Model,
		Cwd:                r.Cwd,
		MaxIters:           r.MaxIters,
		MaxTokens:          r.MaxTokens,
		Memory:             r.Memory,
		Skills:             r.Skills,
		Approve:            r.Approve,
		Out:                r.Out,
		PriceIn:            r.PriceIn,
		PriceOut:           r.PriceOut,
		ContextWindow:      r.ContextWindow,
		AuxModel:           r.AuxModel,
		AuxProvider:        r.AuxProvider,
		AuxPriceIn:         r.AuxPriceIn,
		AuxPriceOut:        r.AuxPriceOut,
		AutoCompact:        r.AutoCompact,
		DisableAutoCompact: r.DisableAutoCompact,
		Threshold:          r.Threshold,
		Vision:             r.Vision,
		ToolsDisabled:      r.ToolsDisabled,
		Depth:              r.Depth,
		TurnTimeout:        r.TurnTimeout,
		Limits:             r.Limits,
		PromptPrefix:       r.PromptPrefix,
		ApprovalCfg:        r.ApprovalCfg,
		Fallbacks:          r.Fallbacks,
		SubagentCall:       r.SubagentCall,
		Logger:             r.Logger,
		locks:              r.locks,
	}
}

// SetModel updates the active model (and provider capabilities if prov is non-nil)
// safely across concurrent turns and clears the cached system prompt.
func (r *Runner) SetModel(prov provider.Provider, model string, maxTokens, contextWin int, priceIn, priceOut float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Model = model
	if prov != nil {
		r.Provider = prov
		r.MaxTokens = maxTokens
		r.ContextWindow = contextWin
		r.PriceIn = priceIn
		r.PriceOut = priceOut
	}
	r.cachedPrompt = ""
}

// SetCapabilities updates vision and prompted-tools flags safely.
func (r *Runner) SetCapabilities(vision, toolsDisabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Vision = vision
	r.ToolsDisabled = toolsDisabled
}

// GetModel returns the active model name safely.
func (r *Runner) GetModel() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Model
}

// SetApprovalMode updates the runner's approval mode safely at runtime.
func (r *Runner) SetApprovalMode(mode string) {
	r.mu.Lock()
	r.ApprovalCfg.Mode = mode
	r.mu.Unlock()
}

// GetApprovalMode returns the runner's current approval mode ("prompt", "auto", or "deny").
func (r *Runner) GetApprovalMode() string {
	r.mu.Lock()
	mode := r.ApprovalCfg.Mode
	r.mu.Unlock()
	if strings.TrimSpace(mode) == "" {
		return "prompt"
	}
	return mode
}

// SetAutoCompact updates auto-compaction and threshold safely at runtime.
func (r *Runner) SetAutoCompact(enabled bool, threshold float64) {
	r.mu.Lock()
	r.AutoCompact = enabled
	r.DisableAutoCompact = !enabled
	if threshold > 0 {
		r.Threshold = threshold
	}
	r.mu.Unlock()
}

// SetMaxIters updates MaxIters safely at runtime.
func (r *Runner) SetMaxIters(n int) {
	r.mu.Lock()
	r.MaxIters = n
	r.mu.Unlock()
}

// SetTurnTimeout updates TurnTimeout safely at runtime.
func (r *Runner) SetTurnTimeout(d time.Duration) {
	r.mu.Lock()
	r.TurnTimeout = d
	r.mu.Unlock()
}

// SetLimits updates daily cost/token limits safely at runtime.
func (r *Runner) SetLimits(l config.Limits) {
	r.mu.Lock()
	r.Limits = l
	r.mu.Unlock()
}

// SetPromptPrefix updates PromptPrefix safely and clears cached prompt.
func (r *Runner) SetPromptPrefix(prefix string) {
	r.mu.Lock()
	r.PromptPrefix = prefix
	r.cachedPrompt = ""
	r.mu.Unlock()
}

func (r *Runner) primaryTarget() FallbackTarget {
	r.mu.Lock()
	defer r.mu.Unlock()
	return FallbackTarget{
		Provider:      r.Provider,
		Model:         r.Model,
		MaxTokens:     r.MaxTokens,
		PriceIn:       r.PriceIn,
		PriceOut:      r.PriceOut,
		ContextWindow: r.ContextWindow,
		Vision:        r.Vision,
		ToolsDisabled: r.ToolsDisabled,
	}
}

// lockChat serializes turns per chat.
func (r *Runner) lockChat(chatID int64) func() {
	return r.lockRegistry().acquire(chatID)
}
