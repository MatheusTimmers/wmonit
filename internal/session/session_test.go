package session

import (
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

// O worktree só é da sessão (e removível) quando o wmonit o criou; o agent
// livre trabalha no próprio repositório.
func TestOwnsWorktree(t *testing.T) {
	cases := []struct {
		sess Session
		want bool
	}{
		{Session{Kind: KindIssue, Repo: "c:/Fontes/hades", Worktree: "c:/Fontes/.worktrees/ROT-1"}, true},
		{Session{Kind: KindFree, Repo: "c:/Fontes/hades", Worktree: "c:/Fontes/hades"}, false},
		{Session{Kind: KindIssue, Repo: "c:/Fontes/hades", Worktree: "c:/Fontes/hades"}, false},
		{Session{Kind: KindMR}, false},
	}
	for _, c := range cases {
		if got := c.sess.OwnsWorktree(); got != c.want {
			t.Errorf("OwnsWorktree(%+v) = %v, esperado %v", c.sess, got, c.want)
		}
	}
}
