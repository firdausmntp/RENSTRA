package telegram

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	mdCodeBlockRE  = regexp.MustCompile("(?s)```([a-zA-Z0-9_-]*)\\n?(.*?)```")
	mdBlockquoteRE = regexp.MustCompile(`(?m)^(?:>[ \t]?.*(?:\r?\n|$))+`)
	mdInlineCodeRE = regexp.MustCompile("`([^`\n]+)`")
	mdLinkRE       = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	mdBulletRE     = regexp.MustCompile(`(?m)^([ \t]*)\*[ \t]+`)
	mdRuleRE       = regexp.MustCompile(`(?m)^(?:---+|===+|\*\*\*+)[ \t]*$`)
	mdHeadingRE    = regexp.MustCompile(`(?m)^#{1,6}[ \t]+(.+)$`)
	mdBoldStarRE   = regexp.MustCompile(`\*\*((?:[^\n*]|\*[^*])+?)\*\*`)
	mdBoldUnderRE  = regexp.MustCompile(`__([^\n_]+?)__`)
	mdStrikeRE     = regexp.MustCompile(`~~([^\n~]+?)~~`)
	mdItalicStarRE = regexp.MustCompile(`\*([^\s*<>][^*<>\n]*?[^\s*<>]|[^\s*<>])\*`)
	mdItalicUnder  = regexp.MustCompile(`(^|[\s(\["'.,!?])_([^\s_<>\n][^_<>\n]*?[^\s_<>\n]|[^\s_<>\n])_($|[\s)\]"'.,!?;:\-])`)
)

// markdownToHTML converts Markdown (code blocks, blockquotes, inline code,
// links, headings, lists, bold, italic, strikethrough) into valid Telegram HTML
// while escaping raw HTML characters and leaving snake_case untouched.
func markdownToHTML(s string) string {
	var slots []string
	stash := func(rendered string) string {
		idx := len(slots)
		slots = append(slots, rendered)
		return fmt.Sprintf("\x00SLOT%04d\x00", idx)
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

	// 2. Stash blockquotes, formatting inner inline markdown cleanly.
	out = mdBlockquoteRE.ReplaceAllStringFunc(out, func(m string) string {
		lines := strings.Split(m, "\n")
		var clean []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			line = strings.TrimPrefix(line, ">")
			line = strings.TrimPrefix(line, " ")
			clean = append(clean, formatInlineSegment(line, stash))
		}
		if len(clean) == 0 {
			return ""
		}
		return stash("<blockquote>" + strings.Join(clean, "\n") + "</blockquote>") + "\n"
	})

	// 3. Format remaining document body.
	out = formatBodySegment(out, stash)

	// 4. Restore stashed HTML blocks (inner blocks may reference earlier slots).
	for i := len(slots) - 1; i >= 0; i-- {
		key := fmt.Sprintf("\x00SLOT%04d\x00", i)
		out = strings.ReplaceAll(out, key, slots[i])
	}
	return out
}

func formatInlineSegment(s string, stash func(string) string) string {
	out := mdInlineCodeRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdInlineCodeRE.FindStringSubmatch(m)
		return stash("<code>" + html.EscapeString(sub[1]) + "</code>")
	})
	out = mdLinkRE.ReplaceAllStringFunc(out, func(m string) string {
		sub := mdLinkRE.FindStringSubmatch(m)
		return stash("<a href=\"" + html.EscapeString(sub[2]) + "\">" + html.EscapeString(sub[1]) + "</a>")
	})
	out = html.EscapeString(out)
	out = mdBoldStarRE.ReplaceAllString(out, "<b>$1</b>")
	out = mdBoldUnderRE.ReplaceAllString(out, "<b>$1</b>")
	out = mdStrikeRE.ReplaceAllString(out, "<s>$1</s>")
	out = mdItalicStarRE.ReplaceAllString(out, "<i>$1</i>")
	out = mdItalicUnder.ReplaceAllString(out, "$1<i>$2</i>$3")
	return out
}

func formatBodySegment(s string, stash func(string) string) string {
	out := mdInlineCodeRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdInlineCodeRE.FindStringSubmatch(m)
		return stash("<code>" + html.EscapeString(sub[1]) + "</code>")
	})
	out = mdLinkRE.ReplaceAllStringFunc(out, func(m string) string {
		sub := mdLinkRE.FindStringSubmatch(m)
		return stash("<a href=\"" + html.EscapeString(sub[2]) + "\">" + html.EscapeString(sub[1]) + "</a>")
	})

	out = html.EscapeString(out)
	out = mdRuleRE.ReplaceAllString(out, "────────")
	out = mdBulletRE.ReplaceAllString(out, "$1• ")

	// Convert headings first & stash them so **bold** inside headings never
	// creates invalid nested <b><b>...</b></b> tags in Telegram HTML.
	out = mdHeadingRE.ReplaceAllStringFunc(out, func(m string) string {
		sub := mdHeadingRE.FindStringSubmatch(m)
		inner := strings.TrimSpace(sub[1])
		inner = mdBoldStarRE.ReplaceAllString(inner, "$1")
		inner = mdBoldUnderRE.ReplaceAllString(inner, "$1")
		inner = mdItalicStarRE.ReplaceAllString(inner, "<i>$1</i>")
		inner = mdItalicUnder.ReplaceAllString(inner, "$1<i>$2</i>$3")
		return stash("<b>" + inner + "</b>")
	})

	out = mdBoldStarRE.ReplaceAllString(out, "<b>$1</b>")
	out = mdBoldUnderRE.ReplaceAllString(out, "<b>$1</b>")
	out = mdStrikeRE.ReplaceAllString(out, "<s>$1</s>")
	out = mdItalicStarRE.ReplaceAllString(out, "<i>$1</i>")
	out = mdItalicUnder.ReplaceAllString(out, "$1<i>$2</i>$3")
	return out
}
