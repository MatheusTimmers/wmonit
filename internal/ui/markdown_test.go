package ui

import (
	"strings"
	"testing"
)

const replyWithFeedback = "> **Corrected:** \"Check the client test.\"\n\nI read `ClientTestU.pas` end to end.\n\n## Real defects\n\n**1.** stalls ([ClientTestU.pas:961](vscode://file/C:/Fontes/x/ClientTestU.pas:961))\n- reads a 2-byte length\n"

// O bloco de feedback do inglês sai do topo e o link do editor vira só o
// arquivo:linha.
func TestTidyMarkdown(t *testing.T) {
	got := tidyMarkdown(replyWithFeedback)
	if strings.Contains(got, "Corrected") || strings.Contains(got, "vscode://") {
		t.Errorf("tidyMarkdown deixou ruído:\n%s", got)
	}
	if !strings.HasPrefix(got, "I read") || !strings.Contains(got, "stalls (ClientTestU.pas:961)") {
		t.Errorf("tidyMarkdown = %q", got)
	}
	// Renderizado: a marcação de negrito e a crase somem (o tema mantém o "##"
	// dos títulos como enfeite).
	if out := renderMarkdown(replyWithFeedback, 80); !strings.Contains(out, "Real") || strings.Contains(out, "**") || strings.Contains(out, "`") {
		t.Errorf("markdown não renderizado:\n%s", out)
	}
}

// O resumo de uma linha pula a citação e a marcação.
func TestCleanReply(t *testing.T) {
	if got := cleanReply(replyWithFeedback); got != "I read ClientTestU.pas end to end. Real defects" {
		t.Errorf("cleanReply = %q", got)
	}
}
