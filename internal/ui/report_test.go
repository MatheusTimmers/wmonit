package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/tasks"
)

func TestViewReport(t *testing.T) {
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1)

	merged := now
	old := now.AddDate(0, 0, -3)

	m := Model{
		gl: &gitlab.Summary{
			OpenMRs: []gitlab.MR{
				{IID: 3, Title: "aberto hoje #ABC-3", CreatedAt: now},
				{IID: 4, Title: "aberto antes #ABC-4", CreatedAt: old},
			},
			Merged: []gitlab.MR{
				{IID: 1, Title: "feature nova #ABC-1 [feature]", CreatedAt: old, MergedAt: &merged},
				{IID: 2, Title: "antigo #ABC-2", CreatedAt: old, MergedAt: &old},
			},
		},
		ji: &jira.Summary{Resolved: []jira.Issue{
			{Key: "ABC-1", Summary: "resolvida hoje", Resolved: today},
			{Key: "ABC-9", Summary: "resolvida antes", Resolved: yesterday.Format("2006-01-02")},
		}},
		store: &tasks.Store{Tasks: []tasks.Task{
			{Text: "tarefa de hoje", Done: true, DoneAt: &now},
			{Text: "tarefa de ontem", Done: true, DoneAt: &yesterday},
			{Text: "tarefa pendente", Done: false},
		}},
	}

	out := m.viewReport()

	for _, want := range []string{"feature nova", "aberto hoje", "resolvida hoje", "tarefa de hoje"} {
		if !strings.Contains(out, want) {
			t.Errorf("relatório não contém %q\n%s", want, out)
		}
	}
	for _, notWant := range []string{"antigo", "aberto antes", "resolvida antes", "tarefa de ontem", "tarefa pendente"} {
		if strings.Contains(out, notWant) {
			t.Errorf("relatório não deveria conter %q\n%s", notWant, out)
		}
	}

	if s := m.reportSummary(); s != "1 MRs abertos · 1 mergeados · 1 issues · 1 tarefas" {
		t.Errorf("reportSummary = %q", s)
	}
}
