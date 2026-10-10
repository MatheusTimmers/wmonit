package hub

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/session"
)

func TestRealHerdr(t *testing.T) {
	name := os.Getenv("WMONIT_HERDR_IT")
	if name == "" {
		t.Skip("defina WMONIT_HERDR_IT=<sessão de teste do herdr> para rodar contra um herdr real")
	}
	h := herdr.New("herdr")
	h.Session = name

	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	snap := Poll(h)
	if !snap.Online() {
		t.Fatalf("herdr offline: %v", snap.Err)
	}
	sess := session.Session{Key: "IT-1", Service: "it", Repo: dir, Worktree: dir, Kind: session.KindIssue, Status: session.StatusActive}
	got, err := Open(h, snap, sess, OpenOpts{
		Editor:   "echo editor-ok",
		TaskText: "contexto",
		Kickoff:  claude.Kickoff(),
		Setup:    []string{"touch setup-ok"},
		Tabs:     []ExtraTab{{Label: "dev", Command: "echo dev-ok"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sessão: %+v", got)
	defer func() { _ = h.CloseWorkspace(got.Workspace) }()

	tabs, err := h.Tabs(got.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, tb := range tabs {
		labels = append(labels, tb.Label)
	}
	if len(labels) != 4 || labels[0] != TabTerminal || labels[1] != TabClaude || labels[2] != TabEditor || labels[3] != "dev" {
		t.Errorf("abas = %v", labels)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "setup-ok")); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "setup-ok")); err != nil {
		t.Errorf("setup não rodou no terminal: %v", err)
	}

	snap = Poll(h)
	a, ok := snap.AgentIn(got.Workspace)
	if !ok || a.Name != got.Agent || a.Kind != "claude" {
		t.Fatalf("agent não detectado: %+v %v (agents=%+v)", a, ok, snap.Agents)
	}
	t.Logf("agent %s: %s, pendente=%q", a.Name, a.Status, got.PendingPrompt)
	if a.Status == herdr.StatusBlocked && got.PendingPrompt == "" {
		t.Error("com o Claude travado na partida, o pedido deveria ficar pendente")
	}
	if _, err := os.Stat(filepath.Join(dir, claude.TaskFile)); err != nil {
		t.Error(err)
	}
	screen, err := h.Read(a.Name, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("tela:\n%s", screen)

	if os.Getenv("WMONIT_HERDR_IT_PROMPT") != "" && got.PendingPrompt != "" {
		if a.Status == herdr.StatusBlocked {
			if err := h.SendKeys(a.Name, "down", "enter"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := h.Wait(a.Name, 60*time.Second, herdr.StatusIdle, herdr.StatusDone); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Second)
		sent, err := Deliver(h, Poll(h), &got)
		if err != nil || !sent || got.PendingPrompt != "" {
			t.Fatalf("Deliver real = %v %v pendente=%q", sent, err, got.PendingPrompt)
		}
		time.Sleep(2 * time.Second)
		cur, _ := Poll(h).Agent(a.Name)
		t.Logf("depois da entrega: %s, sessão do Claude %s", cur.Status, cur.SessionID())
		if cur.SessionID() != got.ClaudeSession {
			t.Errorf("o Claude deveria usar o id fixado: herdr=%q wmonit=%q", cur.SessionID(), got.ClaudeSession)
		}
		if _, err := h.Wait(a.Name, 120*time.Second, herdr.StatusIdle, herdr.StatusDone); err != nil {
			t.Fatal(err)
		}
		if claude.TranscriptPath(got.ClaudeSession) == "" {
			t.Fatal("transcript da conversa não encontrado")
		}
		if err := h.CloseWorkspace(got.Workspace); err != nil {
			t.Fatal(err)
		}
		again, err := Open(h, Poll(h), got, OpenOpts{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.CloseWorkspace(again.Workspace) }()
		if again.ClaudeSession != got.ClaudeSession {
			t.Fatalf("reabrir deveria manter a conversa: %q -> %q", got.ClaudeSession, again.ClaudeSession)
		}
		re, _ := Poll(h).Agent(again.Agent)
		t.Logf("reaberto: %s, conversa %q", re.Status, re.Title)
		if re.SessionID() != got.ClaudeSession {
			t.Errorf("deveria retomar a mesma conversa: herdr=%q wmonit=%q", re.SessionID(), got.ClaudeSession)
		}
	}

	cp := got
	cp.Workspace = "w999"
	if !Reconcile(&cp, snap) || cp.Workspace != got.Workspace {
		t.Errorf("reconcile pelo label falhou: %+v", cp)
	}
}
