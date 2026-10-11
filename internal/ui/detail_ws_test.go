package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

func TestNextStep(t *testing.T) {
	active := session.Session{Status: session.StatusActive, Workspace: "w1"}
	idle := &herdr.Agent{Status: herdr.StatusIdle}
	cases := []struct {
		name string
		in   stepInput
		want string
	}{
		{"concluído", stepInput{sess: session.Session{Status: session.StatusCompleted}}, ""},
		{"fechado", stepInput{sess: session.Session{Status: session.StatusActive}}, "w reabre"},
		{"confiança", stepInput{sess: active, agent: idle, trust: true}, "t confirma"},
		{"travado", stepInput{sess: active, agent: &herdr.Agent{Status: herdr.StatusBlocked}}, "a abre no herdr"},
		{"limpar", stepInput{sess: active, agent: idle, staleReason: "MR x mergeado"}, "f conclui"},
		{"ci", stepInput{sess: active, agent: idle, mr: &gitlab.MRStatus{State: "opened", HeadPipeline: &gitlab.Pipeline{Status: "failed"}}}, "CI quebrado"},
		{"pronto", stepInput{sess: active, agent: idle, mr: &gitlab.MRStatus{State: "opened", DetailedMergeStatus: "mergeable", BlockingDiscussionsResolved: true}}, "pronto para merge"},
		{"sujo", stepInput{sess: active, agent: idle, git: &worktree.Info{Dirty: []string{" M a.go"}}}, "p pede o commit"},
		{"enviar", stepInput{sess: active, agent: idle, git: &worktree.Info{Commits: []string{"abc x"}}}, "s faz o push"},
		{"sem claude", stepInput{sess: active}, "p sobe um"},
		{"nada", stepInput{sess: active, agent: idle, git: &worktree.Info{Upstream: "origin/x"}}, ""},
	}
	for _, c := range cases {
		got := nextStep(c.in)
		if (c.want == "" && got != "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, esperava %q", c.name, got, c.want)
		}
	}
	if ago(time.Now().Add(-90*time.Minute)) != "há 1h" || ago(time.Now().Add(-72*time.Hour)) != "há 3 dias" {
		t.Error("ago")
	}
}

func TestSessionDetailShowsEverything(t *testing.T) {
	sessions := []session.Session{{
		ID: "ROT-512-x", Key: "ROT-512", Issue: "ROT-512", Title: "endpoint de histórico", Service: "backoffice",
		Kind: session.KindIssue, Label: "rot-512", Workspace: "w1", Agent: "rot-512", Status: session.StatusActive,
		UserNote: "cuidado com a paginação", Created: time.Now().Add(-26 * time.Hour), Model: "opus",
	}}
	m, _ := newWorkspaceModel(t, config.Config{}, sessions)
	mm, _ := m.Update(m.fetchMRStatus()())
	m = mm.(Model)
	for i, r := range m.wsRows() {
		if r.kind == rowSession {
			m.cursor = i
		}
	}
	m, cmd := press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = deliver(t, m, cmd)
	body := m.detail.body
	for _, want := range []string{
		"criado ontem", "modelo opus",
		"Próximo passo",
		"Jira ROT-512", "prioridade", "Descrição de demonstração", "Product Owner",
		"MR backoffice!412", "feature/ROT-512", "CI falhou (test-integration)", "Ana Revisora",
		"herdr", "rot-512",
		"Sua explicação", "cuidado com a paginação",
		"Tela do Claude",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detalhe sem %q", want)
		}
	}
	if t.Failed() {
		t.Log(body)
	}
}
