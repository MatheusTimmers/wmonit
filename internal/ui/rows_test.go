package ui

import (
	"strings"
	"testing"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
)

func TestMatchMR(t *testing.T) {
	mr := gitlab.MR{Title: "corrige login #ABC-12 [bug]"}
	for _, q := range []string{"login", "abc-12", "bug"} {
		if !matchMR(mr, q) {
			t.Errorf("matchMR deveria casar com %q", q)
		}
	}
	if matchMR(mr, "deploy") {
		t.Errorf("matchMR não deveria casar com 'deploy'")
	}
}

func TestGitlabRowsFilter(t *testing.T) {
	m := Model{
		tab:    tabGitLab,
		filter: "login",
		fetch: fetchState{gl: &gitlab.Summary{
			Username: "tim",
			OpenMRs: []gitlab.MR{
				{IID: 1, ProjectID: 9, Title: "corrige login #ABC-1"},
				{IID: 2, ProjectID: 9, Title: "ajusta cache #ABC-2"},
			},
		}},
	}
	if got := m.focusCount(); got != 1 {
		t.Fatalf("focusCount com filtro = %d, esperado 1", got)
	}
	it := m.selectedItem()
	if it == nil || it.mr == nil || it.mr.IID != 1 {
		t.Errorf("selectedItem = %+v, esperado o MR de login (IID 1)", it)
	}
}

func TestJiraRowsFilterByStatus(t *testing.T) {
	m := Model{
		tab:    tabJira,
		cfg:    config.Config{},
		filter: "deploy",
		fetch: fetchState{ji: &jira.Summary{
			Open: []jira.Issue{
				{Key: "ABC-1", Summary: "tarefa A", Status: "Em Deploy", Category: "indeterminate"},
				{Key: "ABC-2", Summary: "tarefa B", Status: "Em Andamento", Category: "indeterminate"},
			},
		}},
	}
	rows := m.jiraRows()
	if !strings.Contains(rows[0].text, "deploy") {
		t.Errorf("cabeçalho do filtro inesperado: %q", rows[0].text)
	}
	if got := m.focusCount(); got != 1 {
		t.Errorf("focusCount com filtro de status = %d, esperado 1", got)
	}
}
