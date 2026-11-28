package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/dashboard"
	"renstra/internal/memory"
	"renstra/internal/store"
)

func doctorCmd(out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	home := config.Home()
	st, _ := store.Open(filepath.Join(home, "state.db"))
	if st != nil {
		defer st.Close()
	}
	mem, _ := memory.Open(filepath.Join(home, "memory"))
	dashAddr, dashEnabled := cfg.DashboardListenAddr(0, "")
	if !dashEnabled {
		dashAddr = ""
	}
	report := buildDoctorReport(context.Background(), cfg, nil, st, mem, nil, home, dashAddr)
	_, err = fmt.Fprintln(out, report)
	return err
}

func (h *telegramHandler) handleDoctorCmd(ctx context.Context, chatID int64) (string, error) {
	cr := h.runnerForChat(ctx, chatID)
	return buildDoctorReport(ctx, h.cfg, cr, h.store, h.runner.Memory, h.logHub, config.Home(), h.dashAddr), nil
}

func buildDoctorReport(
	ctx context.Context,
	cfg *config.Config,
	cr *agent.Runner,
	st *store.Store,
	mem *memory.Store,
	logHub *dashboard.LogHub,
	home, dashAddr string,
) string {
	var b strings.Builder
	var warns []string

	fmt.Fprintf(&b, "🩺 **RENSTRA Doctor** (`%s`)\n\n", version)

	// 1. Timeout & Long-Horizon config
	maxIters := 80
	turnTO := 30 * time.Minute
	modelName := "(belum diset)"
	ctxWin := 128000
	if cfg != nil {
		if cfg.Agent.MaxIterations > 0 {
			maxIters = cfg.Agent.MaxIterations
		}
		if cfg.Agent.MaxTurnTimeout > 0 {
			turnTO = time.Duration(cfg.Agent.MaxTurnTimeout)
		}
		if cfg.DefaultModel != "" {
			modelName = cfg.DefaultModel
		}
	}
	if cr != nil {
		if cr.MaxIters > 0 {
			maxIters = cr.MaxIters
		}
		if cr.TurnTimeout > 0 {
			turnTO = cr.TurnTimeout
		}
		if m := cr.GetModel(); m != "" {
			modelName = m
		}
		if cr.ContextWindow > 0 {
			ctxWin = cr.ContextWindow
		}
	}
	b.WriteString("⚙️ **Konfigurasi Agent**\n")
	fmt.Fprintf(&b, "• Model: `%s` (ctx `%d`)\n", modelName, ctxWin)
	fmt.Fprintf(&b, "• Max Iterations: `%d` | Turn Timeout: `%s`\n", maxIters, turnTO)
	if turnTO <= 15*time.Minute {
		warns = append(warns, fmt.Sprintf("`max_turn_timeout` masih `%s` — naikkan ke `30m`/`60m` (`/config turn_timeout 30m` atau edit `config.yaml`) agar tugas panjang tidak mudah timeout.", turnTO))
	}
	if maxIters < 60 {
		warns = append(warns, fmt.Sprintf("`max_iterations` masih `%d` — disarankan minimal `80` untuk tugas long-horizon.", maxIters))
	}

	// 2. Provider & Fallback health
	if cfg != nil {
		provCount := len(cfg.Providers)
		missingKeys := 0
		for _, p := range cfg.Providers {
			if strings.TrimSpace(p.APIKey) == "" && strings.TrimSpace(p.APIKeyEnv) != "" && os.Getenv(p.APIKeyEnv) == "" {
				if !strings.EqualFold(p.Kind, "ollama") {
					missingKeys++
				}
			}
		}
		fmt.Fprintf(&b, "• Providers: `%d` akun | Fallbacks: `%d`", provCount, len(cfg.Fallbacks))
		if cfg.SmartFallbackEnabled() {
			b.WriteString(" (Smart Fallback ON)")
		}
		b.WriteString("\n")
		if missingKeys > 0 {
			warns = append(warns, fmt.Sprintf("%d provider tidak memiliki API key di environment/`.env`.", missingKeys))
		}
	}
	if cr != nil {
		statuses := cr.FallbackStatus()
		unhealthy := 0
		for _, stItem := range statuses {
			if stItem.CooldownLeft > 0 {
				unhealthy++
			}
		}
		if unhealthy > 0 {
			warns = append(warns, fmt.Sprintf("%d target model sedang cooldown circuit-breaker (cek `/fallback`).", unhealthy))
		}
	}

	// 3. Memory & Rules
	if mem != nil {
		snap := mem.Snapshot()
		mLim, uLim, sLim := mem.AllLimits()
		mLen := utf8.RuneCountInString(snap.Memory)
		uLen := utf8.RuneCountInString(snap.User)
		sLen := utf8.RuneCountInString(snap.Soul)
		b.WriteString("\n🧠 **Memori & Aturan**\n")
		fmt.Fprintf(&b, "• `SOUL.md`: %d/%d char | `MEMORY.md`: %d/%d | `USER.md`: %d/%d\n", sLen, sLim, mLen, mLim, uLen, uLim)
		if mLim > 0 && float64(mLen)/float64(mLim) >= 0.85 {
			warns = append(warns, "`MEMORY.md` sudah >85% penuh — pindahkan fakta teknis panjang ke Smart Memory (`memory action=remember`).")
		}
		if uLim > 0 && float64(uLen)/float64(uLim) >= 0.85 {
			warns = append(warns, "`USER.md` sudah >85% penuh — rapikan atau naikkan batas via `/memory limit`.")
		}
	}
	if st != nil {
		if facts, err := st.ListMemoryFacts(ctx, "", 100); err == nil {
			fmt.Fprintf(&b, "• Smart Memory Facts: `%d` entri\n", len(facts))
		}
		dbPath := filepath.Join(home, "state.db")
		if info, err := os.Stat(dbPath); err == nil {
			fmt.Fprintf(&b, "• SQLite DB: `%.2f MB`\n", float64(info.Size())/(1024*1024))
		}
	}

	// 4. Dashboard & LogHub
	b.WriteString("\n📊 **Layanan & Log**\n")
	if dashAddr != "" {
		authState := "token ON"
		if cfg == nil || strings.TrimSpace(cfg.Dashboard.Token) == "" {
			authState = "tanpa token"
			if !strings.HasPrefix(dashAddr, "127.0.0.1:") && !strings.HasPrefix(dashAddr, "localhost:") {
				warns = append(warns, fmt.Sprintf("Dashboard ter-bind ke `%s` tanpa `dashboard.token`! Set bearer token segera.", dashAddr))
			}
		}
		fmt.Fprintf(&b, "• Dashboard: `http://%s` (%s)\n", dashAddr, authState)
	} else {
		b.WriteString("• Dashboard: nonaktif\n")
	}
	if logHub != nil {
		errs, wrns, total := logHub.Counts()
		fmt.Fprintf(&b, "• Log Buffer: `%d` entri (`ERROR`: %d, `WARN`: %d)\n", total, errs, wrns)
	}

	if len(warns) == 0 {
		b.WriteString("\n✅ **Semua pemeriksaan sehat — tidak ada masalah kritis terdeteksi.**")
	} else {
		b.WriteString("\n⚠️ **Rekomendasi Perbaikan:**\n")
		for i, w := range warns {
			fmt.Fprintf(&b, "%d. %s\n", i+1, w)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
