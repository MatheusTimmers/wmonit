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

	got := s2.Find(s2.Sessions[0].ID)
	if got == nil {
		t.Fatal("Find não achou a sessão")
	}
	got.Status = StatusCompleted
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
	t.Setenv("XDG_DATA_HOME", dir)
	want := filepath.Join(dir, "wmonit", "logs")
	if LogDir() != want {
		t.Fatalf("LogDir = %s, esperado %s", LogDir(), want)
	}
	_ = os.MkdirAll(LogDir(), 0o755)
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
