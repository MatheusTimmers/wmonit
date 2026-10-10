package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/timmers/wmonit/internal/config"
)

func TestHelpPopup(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("WMONIT_CONFIG", cfgPath)
	m, _ := newWorkspaceModel(t, config.Config{GitLab: config.GitLab{URL: "https://git", Token: "segredo"}}, nil)

	m, _ = press(t, m, runes("?"))
	if !m.help.open || m.help.tab != helpKeys {
		t.Fatal("? deveria abrir o popup nos atalhos")
	}
	out := m.View().Content
	if !strings.Contains(out, "Atalhos") || !strings.Contains(out, "entregar: push") || !strings.Contains(out, "[6] Workspaces") {
		t.Fatalf("popup deveria ficar por cima da lista:\n%s", out)
	}

	m, _ = press(t, m, runes("j"))
	if m.tab != tabSessoes || m.cursor != 0 {
		t.Fatal("com o popup aberto as teclas não vão para a tela de baixo")
	}

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	out = m.View().Content
	if m.help.tab != helpConfig || !strings.Contains(out, "[gitlab]") || !strings.Contains(out, "definido") || strings.Contains(out, "segredo") {
		t.Fatalf("aba config deveria mostrar a config sem expor o token:\n%s", out)
	}

	if err := os.WriteFile(cfgPath, []byte("[herdr]\nnotify = \"both\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mm, _ := m.Update(configEditedMsg{})
	m = mm.(Model)
	if m.cfg.Herdr.Notify != "both" || !strings.Contains(m.help.vp.GetContent(), "both") {
		t.Fatalf("config deveria recarregar: %+v", m.cfg.Herdr)
	}

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.help.open {
		t.Fatal("esc fecha")
	}

	m.mode = modeDescribing
	m, _ = press(t, m, runes("?"))
	if m.help.open {
		t.Fatal("? digitado numa mensagem não abre o popup")
	}
}
