package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	s.Add(Session{ID: NewID("ABC-123"), Key: "ABC-123", Title: "teste", Label: "ABC-123", Workspace: "w3", Status: StatusActive})
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
	if !s2.HasActiveFor("ABC-123") || s2.HasActiveFor("") {
		t.Fatal("HasActiveFor deveria achar só a chave ativa")
	}
	if got, ok := s2.ByWorkspace("w3"); !ok || got.Key != "ABC-123" {
		t.Fatal("ByWorkspace não achou a sessão")
	}
	if !s2.LabelTaken("abc-123") {
		t.Fatal("LabelTaken deveria ignorar maiúsculas")
	}
	id := s2.Sessions[0].ID
	if !s2.Update(id, func(x *Session) { x.Status = StatusCompleted }) {
		t.Fatal("Update não achou a sessão")
	}
	if s2.HasActiveFor("ABC-123") || s2.LabelTaken("ABC-123") {
		t.Fatal("sessão concluída não é ativa")
	}
	s2.Delete(id)
	if len(s2.Sessions) != 0 {
		t.Fatal("Delete não removeu")
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

func TestLegacyPipelineSessionStaysActive(t *testing.T) {
	var s Session
	raw := `{"id":"x","key":"ABC-1","status":"waiting","phase":"plan","claude_ids":{"plan":"id"}}`
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if !s.Active() {
		t.Fatal("sessão do pipeline antigo deveria continuar ativa")
	}
	if s.IssueKey() != "ABC-1" || s.MRRef() != "" {
		t.Fatalf("links inesperados: issue=%q mr=%q", s.IssueKey(), s.MRRef())
	}
}

func TestLinks(t *testing.T) {
	s := Session{Key: "hades!42", Kind: KindMR, Issue: "ROT-9"}
	if s.IsIssue() {
		t.Error("Kind=mr deveria vencer o formato da chave")
	}
	if s.MRRef() != "hades!42" || s.IssueKey() != "ROT-9" {
		t.Errorf("links = %q %q", s.MRRef(), s.IssueKey())
	}
	free := Session{Kind: KindFree, Service: "wmonit", Repo: "/src/wmonit", Worktree: "/src/wmonit"}
	if free.OwnsWorktree() || free.Name() != "wmonit" {
		t.Errorf("sessão livre: owns=%v name=%q", free.OwnsWorktree(), free.Name())
	}
	if !IsJiraKey("ABC-123") || IsJiraKey("hades!42") {
		t.Error("IsJiraKey")
	}
}
