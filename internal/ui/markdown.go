package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
)

// mdRenderers guarda um renderer por largura: montar o glamour custa (lê o
// tema e compila os estilos) e a largura só muda num resize.
var mdRenderers = map[int]*glamour.TermRenderer{}

// renderMarkdown desenha o markdown das respostas do agent (títulos, listas,
// tabelas, código) na largura dada. Tema escuro fixo: o auto consulta o
// terminal, o que trava com a TUI no ar. Em erro devolve o texto cru.
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

// Links do editor ([ClientTestU.pas:961](vscode://file/C:/...)) servem no
// terminal do Claude; aqui o caminho inteiro só polui — fica o texto.
var editorLink = regexp.MustCompile(`\[([^\]]+)\]\((?:vscode|file)://[^)]+\)`)

// tidyMarkdown tira do topo o bloco de feedback do inglês que o CLAUDE.md
// pede em toda resposta ("> **In English:** …", "> ✓ Correct.") e encurta os
// links do editor.
func tidyMarkdown(md string) string {
	lines := strings.Split(strings.TrimSpace(md), "\n")
	i := 0
	for i < len(lines) && (strings.HasPrefix(strings.TrimSpace(lines[i]), ">") || strings.TrimSpace(lines[i]) == "") {
		i++
	}
	if i == len(lines) {
		i = 0 // a resposta inteira é citação: mantém
	}
	return editorLink.ReplaceAllString(strings.Join(lines[i:], "\n"), "$1")
}
