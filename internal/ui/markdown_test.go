package ui

import (
	"strings"
	"testing"
)

const replyWithFeedback = "> **Corrected:** \"Check the client test.\"\n\nI read `ClientTestU.pas` end to end.\n\n## Real defects\n\n**1.** stalls ([ClientTestU.pas:961](vscode://file/C:/Fontes/x/ClientTestU.pas:961))\n- reads a 2-byte length\n"

func TestTidyMarkdown(t *testing.T) {
	got := tidyMarkdown(replyWithFeedback)
	if strings.Contains(got, "Corrected") || strings.Contains(got, "vscode://") {
		t.Errorf("tidyMarkdown deixou ruído:\n%s", got)
	}
	if !strings.HasPrefix(got, "I read") || !strings.Contains(got, "stalls (ClientTestU.pas:961)") {
		t.Errorf("tidyMarkdown = %q", got)
	}
	if out := renderMarkdown(replyWithFeedback, 80); !strings.Contains(out, "Real") || strings.Contains(out, "**") || strings.Contains(out, "`") {
		t.Errorf("markdown não renderizado:\n%s", out)
	}
}
