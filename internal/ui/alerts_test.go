package ui

import (
	"strings"
	"testing"

	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
)

func TestGitlabAlertsBaseline(t *testing.T) {
	m := Model{seenTodos: map[int]bool{}, gl: &gitlab.Summary{
		Todos: []gitlab.Todo{{ID: 1, ActionName: "review_requested"}},
	}}
	// Primeira leitura só fixa a base — não alerta o que já existia.
	if got := m.newGitlabAlerts(); len(got) != 0 {
		t.Fatalf("baseline não deveria alertar, veio %v", got)
	}
	// Chega um todo novo.
	m.gl.Todos = append(m.gl.Todos, gitlab.Todo{ID: 2, ActionName: "build_failed"})
	got := m.newGitlabAlerts()
	if len(got) != 1 || !strings.Contains(got[0], "Build falhou") {
		t.Fatalf("esperado 1 alerta de build, veio %v", got)
	}
	// Sem novidades, nada.
	if got := m.newGitlabAlerts(); len(got) != 0 {
		t.Fatalf("sem novidades deveria ser vazio, veio %v", got)
	}
}

func TestJiraAlerts(t *testing.T) {
	m := Model{issueStatus: map[string]string{}, ji: &jira.Summary{
		Open: []jira.Issue{{Key: "ABC-1", Summary: "tarefa", Status: "Em Andamento"}},
	}}
	if got := m.newJiraAlerts(); len(got) != 0 {
		t.Fatalf("baseline não deveria alertar, veio %v", got)
	}
	// ABC-1 muda de status e ABC-2 chega nova.
	m.ji.Open = []jira.Issue{
		{Key: "ABC-1", Summary: "tarefa", Status: "Revisão 1"},
		{Key: "ABC-2", Summary: "nova", Status: "Em Andamento"},
	}
	if got := m.newJiraAlerts(); len(got) != 2 {
		t.Fatalf("esperado 2 alertas (status + nova), veio %v", got)
	}
	if got := m.newJiraAlerts(); len(got) != 0 {
		t.Fatalf("sem mudanças deveria ser vazio, veio %v", got)
	}
}
