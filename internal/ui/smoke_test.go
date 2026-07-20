package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/demo"
	"github.com/timmers/wmonit/internal/history"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
)

// TestSmokeRenderAllTabs monta o Model como no modo demo (fontes injetadas) e
// renderiza cada aba — cobre a fiação do composition root e a decomposição do
// Model sem depender de um terminal.
func TestSmokeRenderAllTabs(t *testing.T) {
	dir := t.TempDir()
	store, _ := tasks.LoadFrom(dir)
	hist, _ := history.LoadFrom(dir)
	sess, _ := session.LoadFrom(dir)

	m := New(config.Config{}, store, hist, sess, demo.GitLabSource{}, demo.JiraSource{}, "🧪 DEMO")
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(Model)
	m.fetch.gl = demo.GitLab()
	m.fetch.ji = demo.Jira()

	for tb := tabHoje; tb < numTabs; tb++ {
		m.tab = tb
		out := m.View()
		if strings.TrimSpace(out) == "" {
			t.Fatalf("aba %d: View vazio", tb)
		}
		if !strings.Contains(out, "DEMO") {
			t.Errorf("aba %d: selo DEMO ausente no rodapé", tb)
		}
	}

	perf := m.viewDesempenho()
	for _, want := range []string{"📈 Desempenho", "🎯 Metas", "⚖ Tendência", "📅 Ritmo semanal", "composição", "Agora"} {
		if !strings.Contains(perf, want) {
			t.Errorf("viewDesempenho sem seção %q", want)
		}
	}
}
