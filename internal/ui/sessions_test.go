package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/session"
)

func TestPhaseLabelMode(t *testing.T) {
	if got := phaseLabel(session.PhaseReview, session.ModeReview); got != "revisão" {
		t.Errorf("modo revisão = %q, esperado 'revisão'", got)
	}
	if got := phaseLabel(session.PhaseReview, session.ModeImplement); got != "review 3/3" {
		t.Errorf("modo dev = %q, esperado 'review 3/3'", got)
	}
	if got := phaseLabel(session.PhasePlan, ""); got != "plano 1/3" {
		t.Errorf("plano = %q, esperado 'plano 1/3'", got)
	}
}

// O MR de outra pessoa vira sessão de revisão; o seu próprio, de
// desenvolvimento.
func TestNewSessionFromItemMode(t *testing.T) {
	mine := gitlab.MR{IID: 10, ProjectID: 1, Title: "meu MR", SourceBranch: "feat/x"}
	mine.References.Full = "grp/svc!10"
	other := gitlab.MR{IID: 20, ProjectID: 2, Title: "MR do colega", SourceBranch: "feat/y"}
	other.References.Full = "grp/svc!20"
	m := Model{fetch: fetchState{gl: &gitlab.Summary{OpenMRs: []gitlab.MR{mine}, ReviewPending: []gitlab.MR{other}}}}

	if s, _, create := m.newSessionFromItem(&focusItem{mr: &mine}); s.Mode != session.ModeImplement || create {
		t.Errorf("meu MR: mode=%q create=%v, esperado implement/false", s.Mode, create)
	}
	if s, _, _ := m.newSessionFromItem(&focusItem{mr: &other}); s.Mode != session.ModeReview {
		t.Errorf("MR do colega: mode=%q, esperado review", s.Mode)
	}
}

// A fila de review lista os MRs aguardando você, com o tempo parado, e
// todos continuam selecionáveis para 'c'.
func TestGitlabRowsReviewQueue(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	a := gitlab.MR{IID: 1, ProjectID: 1, Title: "a revisar", UpdatedAt: old}
	b := gitlab.MR{IID: 2, ProjectID: 1, Title: "outro", UpdatedAt: old}
	m := Model{tab: tabGitLab, fetch: fetchState{gl: &gitlab.Summary{Username: "tim", ReviewPending: []gitlab.MR{a, b}}}}

	var text strings.Builder
	for _, r := range m.gitlabRows() {
		text.WriteString(r.text + "\n")
	}
	for _, want := range []string{"Aguardando seu review", "parado há 2d"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("rows da fila de review sem %q", want)
		}
	}
	if got := m.focusCount(); got != 2 {
		t.Errorf("focusCount = %d, esperado 2 (todos selecionáveis)", got)
	}
}
