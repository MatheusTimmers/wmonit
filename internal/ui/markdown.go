package ui

import (
	"regexp"
	"strings"

	"charm.land/glamour/v2"
)

var mdRenderers = map[int]*glamour.TermRenderer{}

func renderMarkdown(md string, width int) string {
	md = tidyMarkdown(md)
	r, ok := mdRenderers[width]
	if !ok {
		var err error
		r, err = glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(width))
		if err != nil {
			return md
		}
		mdRenderers[width] = r
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.Trim(out, "\n")
}

var editorLink = regexp.MustCompile(`\[([^\]]+)\]\((?:vscode|file)://[^)]+\)`)

func tidyMarkdown(md string) string {
	lines := strings.Split(strings.TrimSpace(md), "\n")
	i := 0
	for i < len(lines) && (strings.HasPrefix(strings.TrimSpace(lines[i]), ">") || strings.TrimSpace(lines[i]) == "") {
		i++
	}
	if i == len(lines) {
		i = 0
	}
	return editorLink.ReplaceAllString(strings.Join(lines[i:], "\n"), "$1")
}
