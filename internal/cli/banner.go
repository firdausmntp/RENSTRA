package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"renstra/internal/agent"
)

// PrintBanner displays a styled welcome box for the interactive terminal session.
func PrintBanner(out io.Writer, r *agent.Runner, opts RunOptions) {
	out = plainIfNotTTY(out)
	ver := opts.Version
	if ver == "" {
		ver = "v1.2.8"
	}
	prov := opts.Provider
	if prov == "" {
		prov = "default"
	}
	model := r.GetModel()
	if model == "" {
		model = "default"
	}
	ctxWin := r.ContextWindow
	if ctxWin <= 0 {
		ctxWin = 32000
	}

	skillsCount := 0
	if r.Skills != nil {
		idx := strings.TrimSpace(r.Skills.Index())
		if idx != "" {
			skillsCount = len(strings.Split(idx, "\n"))
		}
	}

	const inner = 65
	var b strings.Builder
	b.WriteString(cCyan + "╭" + strings.Repeat("─", inner) + "╮" + cReset + "\n")
	bannerLine(&b, inner,
		bannerSeg{"RENSTRA Interactive Terminal", cWhite},
		bannerSeg{"  " + ver, cYellow},
	)
	bannerLine(&b, inner,
		bannerSeg{"Model    : ", cDim},
		bannerSeg{padRight(truncateStr(model, 24), 24), cGreen},
		bannerSeg{"Provider: ", cDim},
		bannerSeg{truncateStr(prov, 16), cWhite},
	)
	bannerLine(&b, inner,
		bannerSeg{"Context  : ", cDim},
		bannerSeg{padRight(fmt.Sprintf("%d tokens", ctxWin), 24), ""},
		bannerSeg{"Skills  : ", cDim},
		bannerSeg{fmt.Sprintf("%d terdaftar", skillsCount), ""},
	)
	if r.Cwd != "" {
		bannerLine(&b, inner,
			bannerSeg{"Workspace: ", cDim},
			bannerSeg{truncateStr(r.Cwd, 52), ""},
		)
	}
	bannerLine(&b, inner)
	bannerLine(&b, inner,
		bannerSeg{"Ketik pesanmu atau gunakan ", ""},
		bannerSeg{"/help", cYellow},
		bannerSeg{" untuk bantuan. Ctrl+C: batal.", ""},
	)
	b.WriteString(cCyan + "╰" + strings.Repeat("─", inner) + "╯" + cReset + "\n")
	fmt.Fprint(out, b.String())
	if opts.UpdateInfo != "" {
		fmt.Fprintf(out, "%s💡 %s%s\n\n", cYellow, opts.UpdateInfo, cReset)
	} else {
		fmt.Fprintln(out)
	}
}

type bannerSeg struct{ text, color string }

func bannerLine(b *strings.Builder, inner int, segs ...bannerSeg) {
	visible := 0
	b.WriteString(cCyan + "│" + cReset + " ")
	for _, s := range segs {
		visible += utf8.RuneCountInString(s.text)
		if s.color != "" {
			b.WriteString(s.color + s.text + cReset)
		} else {
			b.WriteString(s.text)
		}
	}
	pad := inner - 2 - visible
	if pad < 0 {
		pad = 0
	}
	b.WriteString(strings.Repeat(" ", pad) + " " + cCyan + "│" + cReset + "\n")
}

func truncateStr(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}
