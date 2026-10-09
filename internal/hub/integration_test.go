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
	got, err := Open(h, snap, sess, OpenOpts{Editor: "echo editor-ok", TaskText: "contexto", Kickoff: claude.Kickoff()})
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
	if len(labels) != 3 || labels[0] != TabTerminal || labels[1] != TabClaude || labels[2] != TabEditor {
		t.Errorf("abas = %v", labels)
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
		deadline := time.Now().Add(60 * time.Second)
		readyFor := 0
		for time.Now().Before(deadline) && readyFor < 2 {
			time.Sleep(1500 * time.Millisecond)
			if cur, ok := Poll(h).Agent(a.Name); ok && cur.Ready() {
				readyFor++
			} else {
				readyFor = 0
			}
		}
		sent, err := Deliver(h, Poll(h), &got)
		if err != nil || !sent || got.PendingPrompt != "" {
			t.Fatalf("Deliver real = %v %v pendente=%q", sent, err, got.PendingPrompt)
		}
		time.Sleep(2 * time.Second)
		cur, _ := Poll(h).Agent(a.Name)
		t.Logf("depois da entrega: %s", cur.Status)
	}

	cp := got
	cp.Workspace = "w999"
	if !Reconcile(&cp, snap) || cp.Workspace != got.Workspace {
		t.Errorf("reconcile pelo label falhou: %+v", cp)
	}
}
