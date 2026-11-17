package telegram

import (
	"html"
	"regexp"
	"strings"
)

var (
	mdCodeBlockRE  = regexp.MustCompile("(?s)```([a-zA-Z0-9_-]*)\\n?(.*?)```")
	mdInlineCodeRE = regexp.MustCompile("`([^`\n]+)`")
	mdLinkRE       = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	mdBoldRE       = regexp.MustCompile(`\*\*([^\n*]+?)\*\*`)
	mdItalicRE     = regexp.MustCompile(`\*([^\s*][^*\n]*?[^\s*]|[^\s*])\*`)
)

// markdownToHTML converts common Markdown (code blocks, inline code, links,
// bold, italic) into Telegram-safe HTML while escaping raw HTML characters and
// leaving snake_case identifiers untouched.
func markdownToHTML(s string) string {
	var slots []string
	stash := func(rendered string) string {
		idx := len(slots)
		slots = append(slots, rendered)
		return "\x00SLOT" + string(rune('A'+idx/26)) + string(rune('A'+idx%26)) + "\x00"
	}

	// 1. Stash fenced code blocks first so inner markdown is never touched.
	out := mdCodeBlockRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdCodeBlockRE.FindStringSubmatch(m)
		lang := strings.TrimSpace(sub[1])
		code := html.EscapeString(strings.TrimSuffix(sub[2], "\n"))
		if lang != "" {
			return stash("<pre><code class=\"language-" + html.EscapeString(lang) + "\">" + code + "</code></pre>")
		}
		return stash("<pre>" + code + "</pre>")
	})

	// 2. Stash inline code.
	out = mdInlineCodeRE.ReplaceAllStringFunc(out, func(m string) string {
		sub := mdInlineCodeRE.FindStringSubmatch(m)
		return stash("<code>" + html.EscapeString(sub[1]) + "</code>")
	})

	// 3. Stash links.
	out = mdLinkRE.ReplaceAllStringFunc(out, func(m string) string {
		sub := mdLinkRE.FindStringSubmatch(m)
		return stash("<a href=\"" + html.EscapeString(sub[2]) + "\">" + html.EscapeString(sub[1]) + "</a>")
	})

	// 4. Escape remaining plain text HTML entities.
	out = html.EscapeString(out)

	// 5. Bold & italic on escaped text.
	out = mdBoldRE.ReplaceAllString(out, "<b>$1</b>")
	out = mdItalicRE.ReplaceAllString(out, "<i>$1</i>")

	// 6. Restore stashed HTML blocks.
	for i, val := range slots {
		key := "\x00SLOT" + string(rune('A'+i/26)) + string(rune('A'+i%26)) + "\x00"
		out = strings.ReplaceAll(out, key, val)
	}
	return out
}
