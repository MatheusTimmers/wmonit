package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	s.Add(Session{ID: NewID("ABC-123"), Key: "ABC-123", Title: "teste", Status: StatusPending})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Sessions) != 1 || s2.Sessions[0].Key != "ABC-123" {
		t.Fatalf("round trip falhou: %+v", s2.Sessions)
	}
	if !s2.HasActiveFor("ABC-123") {
		t.Fatal("HasActiveFor deveria achar sessão pendente")
	}

	if _, ok := s2.Get(s2.Sessions[0].ID); !ok {
		t.Fatal("Get não achou a sessão")
	}
	if !s2.Update(s2.Sessions[0].ID, func(x *Session) { x.Status = StatusCompleted }) {
		t.Fatal("Update não achou a sessão")
	}
	if s2.HasActiveFor("ABC-123") {
		t.Fatal("sessão completed não é ativa")
	}

	s2.DeleteAt(0)
	if len(s2.Sessions) != 0 {
		t.Fatal("DeleteAt não removeu")
	}
}

func TestNewID(t *testing.T) {
	id := NewID("hades!9470")
	if strings.Contains(id, "!") {
		t.Fatalf("id deveria ser slug: %s", id)
	}
	if !strings.HasPrefix(id, "hades-9470-") {
		t.Fatalf("id inesperado: %s", id)
	}
}

func TestLogDir(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "logs")
	if s.LogDir() != want {
		t.Fatalf("LogDir = %s, esperado %s", s.LogDir(), want)
	}
	_ = os.MkdirAll(s.LogDir(), 0o755)
}

func TestPlan(t *testing.T) {
	cases := []struct {
		name    string
		sess    Session
		wantOK  bool
		wantAct Action
	}{
		{"review pendente", Session{Mode: ModeReview, Status: StatusPending},
			true, Action{Phase: PhaseReview}},
		{"review falhou retoma", Session{Mode: ModeReview, Status: StatusFailed, Phase: PhaseReview,
			ClaudeIDs: map[string]string{PhaseReview: "rev-id"}},
			true, Action{Phase: PhaseReview, ResumeID: "rev-id"}},
		{"review concluída não roda", Session{Mode: ModeReview, Status: StatusDone},
			false, Action{}},

		{"implement pendente → plan", Session{Status: StatusPending},
			true, Action{Phase: PhasePlan}},
		{"waiting no plan → dev", Session{Status: StatusWaiting, Phase: PhasePlan},
			true, Action{Phase: PhaseDev}},
		{"waiting no dev → review", Session{Status: StatusWaiting, Phase: PhaseDev},
			true, Action{Phase: PhaseReview}},
		{"falhou no plan retoma sem fix", Session{Status: StatusFailed, Phase: PhasePlan,
			ClaudeIDs: map[string]string{PhasePlan: "plan-id"}},
			true, Action{Phase: PhasePlan, ResumeID: "plan-id"}},
		{"falhou no dev sem review → resume", Session{Status: StatusFailed, Phase: PhaseDev,
			ClaudeIDs: map[string]string{PhaseDev: "dev-id"}},
			true, Action{Phase: PhaseDev, ResumeID: "dev-id"}},
		{"falhou no dev após review → fix", Session{Status: StatusFailed, Phase: PhaseDev,
			ClaudeIDs: map[string]string{PhaseDev: "dev-id"}, Results: map[string]string{PhaseReview: "AJUSTAR"}},
			true, Action{Phase: PhaseDev, ResumeID: "dev-id", UseFix: true}},
		{"pronta → ciclo de correção no dev", Session{Status: StatusDone, Phase: PhaseReview,
			ClaudeIDs: map[string]string{PhaseDev: "dev-id"}, Results: map[string]string{PhaseReview: "AJUSTAR"}},
			true, Action{Phase: PhaseDev, ResumeID: "dev-id", UseFix: true}},
		{"pronta usa ClaudeID legado", Session{Status: StatusDone, Phase: PhaseReview,
			ClaudeID: "legado", Results: map[string]string{PhaseReview: "AJUSTAR"}},
			true, Action{Phase: PhaseDev, ResumeID: "legado", UseFix: true}},
		{"pronta sem conversa não roda", Session{Status: StatusDone, Phase: PhaseReview},
			false, Action{}},
		{"rodando não roda", Session{Status: StatusRunning, Phase: PhaseDev},
			false, Action{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			act, ok := c.sess.Plan()
			if ok != c.wantOK {
				t.Fatalf("ok = %v, esperado %v", ok, c.wantOK)
			}
			if ok && act != c.wantAct {
				t.Errorf("Action = %+v, esperado %+v", act, c.wantAct)
			}
		})
	}
}

func TestPhaseHelpers(t *testing.T) {
	s := Session{Key: "ABC-123"}
	if !s.IsIssue() {
		t.Errorf("chave Jira sem Kind deveria ser issue (legado)")
	}
	s.Kind = KindMR
	if s.IsIssue() {
		t.Errorf("Kind=mr deveria vencer o formato da chave")
	}
	s.SetClaudeID(PhaseDev, "id-dev")
	s.SetClaudeID(PhaseReview, "id-review")
	if s.ClaudeIDs[PhaseDev] != "id-dev" || s.ClaudeID != "id-review" {
		t.Errorf("SetClaudeID: ids = %v, último = %q", s.ClaudeIDs, s.ClaudeID)
	}
	s.SetResult(PhaseReview, "APROVADO")
	if s.Results[PhaseReview] != "APROVADO" {
		t.Errorf("SetResult não gravou")
	}
	if NextPhase(PhasePlan) != PhaseDev || NextPhase(PhaseDev) != PhaseReview || NextPhase(PhaseReview) != "" {
		t.Errorf("NextPhase fora de ordem")
	}
}
