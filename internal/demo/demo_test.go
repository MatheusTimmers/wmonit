package demo

import (
	"testing"

	"github.com/timmers/wmonit/internal/tasks"
)

func TestGitLabData(t *testing.T) {
	g := GitLab()
	if g.Username == "" || len(g.OpenMRs) == 0 || len(g.Merged) == 0 || len(g.ReviewPending) == 0 || len(g.Todos) == 0 {
		t.Fatalf("resumo do GitLab incompleto: %+v", g)
	}
	// Os títulos seguem o padrão "… #TAG [type]", de onde o app extrai a chave.
	if g.OpenMRs[0].JiraKey() == "" {
		t.Errorf("MR sem #TAG no título: %q", g.OpenMRs[0].Title)
	}
}

func TestJiraData(t *testing.T) {
	j := Jira()
	if len(j.Open) == 0 || len(j.Resolved) == 0 {
		t.Fatal("resumo do Jira incompleto")
	}
	for _, is := range j.Resolved {
		if is.Resolved == "" {
			t.Errorf("issue resolvida sem data: %s", is.Key)
		}
	}
}

func TestDemoTasksHasCritical(t *testing.T) {
	crit := false
	for _, tk := range demoTasks() {
		if tk.Priority == tasks.PriorityCritical && tk.Urgent() {
			crit = true
		}
	}
	if !crit {
		t.Error("demoTasks deveria ter uma tarefa crítica urgente")
	}
	if len(demoSessions()) == 0 {
		t.Error("demoSessions não deveria ser vazio")
	}
}
