package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
)

type fakeHerdr struct {
	workspaces []herdr.Workspace
	agents     []herdr.Agent
	startErr   error
	promptErr  error
	calls      []string
	prompts    map[string][]string
	nextTab    int
}

func (f *fakeHerdr) log(format string, args ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fakeHerdr) Workspaces() ([]herdr.Workspace, error) { return f.workspaces, nil }
func (f *fakeHerdr) Agents() ([]herdr.Agent, error)         { return f.agents, nil }
func (f *fakeHerdr) Panes(string) ([]herdr.Pane, error)     { return nil, nil }
func (f *fakeHerdr) Tabs(string) ([]herdr.Tab, error)       { return nil, nil }

func (f *fakeHerdr) CreateWorkspace(label, cwd string) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	id := fmt.Sprintf("w%d", len(f.workspaces)+1)
	ws := herdr.Workspace{ID: id, Label: label}
	f.workspaces = append(f.workspaces, ws)
	f.log("workspace %s %s %s", id, label, cwd)
	f.nextTab = 1
	return ws, herdr.Tab{ID: id + ":t1"}, herdr.Pane{ID: id + ":p1", Cwd: cwd}, nil
}

func (f *fakeHerdr) CreateTab(ws, cwd, label string) (herdr.Tab, herdr.Pane, error) {
	f.nextTab++
	f.log("tab %s %s", ws, label)
	return herdr.Tab{ID: fmt.Sprintf("%s:t%d", ws, f.nextTab), Label: label}, herdr.Pane{ID: fmt.Sprintf("%s:p%d", ws, f.nextTab)}, nil
}

func (f *fakeHerdr) RenameTab(tab, label string) error { f.log("rename %s %s", tab, label); return nil }
func (f *fakeHerdr) FocusWorkspace(ws string) error    { f.log("focus %s", ws); return nil }
func (f *fakeHerdr) CloseWorkspace(ws string) error    { f.log("close %s", ws); return nil }
func (f *fakeHerdr) Run(pane, cmd string) error        { f.log("run %s %s", pane, cmd); return nil }

func (f *fakeHerdr) StartAgent(name, kind, pane string, args ...string) error {
	f.log("start %s %s %s %s", name, kind, pane, strings.Join(args, " "))
	if f.startErr != nil {
		return f.startErr
	}
	f.agents = append(f.agents, herdr.Agent{Name: name, Kind: kind, PaneID: pane, WorkspaceID: strings.Split(pane, ":")[0], Status: herdr.StatusIdle})
	return nil
}

func (f *fakeHerdr) Prompt(agent, text string) error {
	if f.promptErr != nil {
		return f.promptErr
	}
	if f.prompts == nil {
		f.prompts = map[string][]string{}
	}
	f.prompts[agent] = append(f.prompts[agent], text)
	return nil
}

func (f *fakeHerdr) SendKeys(agent string, keys ...string) error { return nil }
func (f *fakeHerdr) Read(agent string, lines int) (string, error) {
	return "tela de " + agent, nil
}

func TestOpenBuildsDevLayout(t *testing.T) {
	dir := t.TempDir()
	f := &fakeHerdr{workspaces: []herdr.Workspace{{ID: "w9", Label: "abc-123"}}}
	sess := session.Session{Key: "ABC-123", Repo: dir, Worktree: dir, Kind: session.KindIssue, Status: session.StatusActive}
	got, err := Open(f, Poll(f), sess, OpenOpts{
		ClaudeArgs: []string{"--model", "opus"},
		Editor:     "nvim .",
		TaskText:   "contexto",
		Kickoff:    claude.Kickoff(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "abc-123-2" {
		t.Errorf("label deveria fugir do workspace existente: %q", got.Label)
	}
	want := []string{
		"workspace w2 abc-123-2 " + dir,
		"rename w2:t1 terminal",
		"tab w2 claude",
		"tab w2 nvim",
		"run w2:p3 nvim .",
		"start abc-123-2 claude w2:p2 --model opus",
	}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("chamadas:\n%s\nesperado:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	if got.Workspace != "w2" || got.Agent != "abc-123-2" || got.PendingPrompt != claude.Kickoff() {
		t.Errorf("sessão = %+v", got)
	}
	if len(f.prompts) != 0 {
		t.Errorf("Open não deveria mandar o pedido na hora: %v", f.prompts)
	}
	if data, err := os.ReadFile(filepath.Join(dir, claude.TaskFile)); err != nil || string(data) != "contexto" {
		t.Errorf("arquivo da tarefa = %q, %v", data, err)
	}
}

func TestOpenKeepsPromptUntilAgentReady(t *testing.T) {
	dir := t.TempDir()
	f := &fakeHerdr{startErr: &herdr.Error{Code: herdr.CodeAgentNotReady, Message: "blocked"}}
	got, err := Open(f, Poll(f), session.Session{Service: "wmonit", Repo: dir, Kind: session.KindFree}, OpenOpts{Kickoff: "faça x"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "wmonit" || got.PendingPrompt != "faça x" || got.Err != "" {
		t.Fatalf("sessão = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, claude.TaskFile)); err == nil {
		t.Error("sem TaskText não deveria gravar arquivo")
	}

	f.agents = []herdr.Agent{{Name: "wmonit", WorkspaceID: got.Workspace, Status: herdr.StatusBlocked}}
	if sent, _ := Deliver(f, Poll(f), &got); sent {
		t.Fatal("não deveria mandar com o agent travado")
	}
	f.agents[0].Status = herdr.StatusIdle
	f.promptErr = &herdr.Error{Code: herdr.CodePromptStalled, Message: "stalled"}
	if sent, err := Deliver(f, Poll(f), &got); sent || err != nil || got.PendingPrompt == "" {
		t.Fatalf("stalled deveria manter o pedido pendente: %v %v %+v", sent, err, got)
	}
	f.promptErr = nil
	if sent, err := Deliver(f, Poll(f), &got); !sent || err != nil || got.PendingPrompt != "" {
		t.Fatalf("Deliver = %v %v %+v", sent, err, got)
	}
	if p := f.prompts["wmonit"]; len(p) != 1 || p[0] != "faça x" {
		t.Errorf("prompts = %v", f.prompts)
	}
}

func TestReconcile(t *testing.T) {
	snap := Snapshot{
		At:         time.Now(),
		Workspaces: []herdr.Workspace{{ID: "w5", Label: "abc-123"}},
		Agents:     []herdr.Agent{{Name: "shell", Kind: "codex", WorkspaceID: "w5"}, {Name: "abc", Kind: "claude", WorkspaceID: "w5"}},
	}
	s := session.Session{Label: "abc-123", Workspace: "w1", Status: session.StatusActive}
	if !Reconcile(&s, snap) || s.Workspace != "w5" || s.Agent != "abc" {
		t.Fatalf("reconcile por label = %+v", s)
	}
	if Reconcile(&s, snap) {
		t.Fatal("segunda reconciliação não deveria mudar nada")
	}
	snap.Workspaces = nil
	if !Reconcile(&s, snap) || s.Workspace != "" || s.Agent != "" {
		t.Fatalf("workspace fechado = %+v", s)
	}
	off := session.Session{Label: "x", Workspace: "w1", Status: session.StatusActive}
	if Reconcile(&off, Snapshot{Err: fmt.Errorf("offline"), At: time.Now()}) || off.Workspace != "w1" {
		t.Fatal("offline não deveria apagar o workspace")
	}
}

func TestEnsureHub(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pendencias")
	f := &fakeHerdr{}
	ready, err := EnsureHub(f, Poll(f), dir, nil)
	if err != nil || ready {
		t.Fatalf("hub recém-criado não está pronto ainda: %v %v", ready, err)
	}
	if f.calls[0] != "workspace w1 wmonit "+dir || !strings.Contains(f.calls[2], "start pendencias claude w1:p1 --permission-mode acceptEdits") {
		t.Errorf("chamadas = %v", f.calls)
	}
	f.calls = nil
	if ready, err := EnsureHub(f, Poll(f), dir, nil); err != nil || !ready || len(f.calls) != 0 {
		t.Errorf("hub existente não deveria recriar: %v %v %v", ready, err, f.calls)
	}
}

func TestDigest(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.Local)
	mr := gitlab.MR{IID: 42, Title: "ROT-9 nova rota", References: struct {
		Full string `json:"full"`
	}{Full: "grupo/hades!42"}}
	other := gitlab.MR{IID: 7, Title: "outra coisa", References: struct {
		Full string `json:"full"`
	}{Full: "grupo/front!7"}}
	in := DigestInput{
		Now: now,
		Items: []Item{{
			Session:    session.Session{Key: "ROT-9", Label: "rot-9", Workspace: "w2", Service: "hades", Kind: session.KindIssue, UserNote: "cuidado com o cache", PendingPrompt: "x"},
			Agent:      &herdr.Agent{Name: "rot-9", Status: herdr.StatusBlocked},
			Screen:     "Do you want to proceed?",
			JiraStatus: "Em Andamento",
			MR:         &mr,
		}},
		Others: []herdr.Workspace{{Label: "bode_os", AgentStatus: herdr.StatusIdle}},
		Jira: &jira.Summary{Open: []jira.Issue{
			{Key: "ROT-9", Status: "Em Andamento"},
			{Key: "ROT-10", Status: "Aberto", Summary: "atrasada", Due: "2026-10-01"},
		}},
		GitLab: &gitlab.Summary{ReviewPending: []gitlab.MR{other}},
		Tasks:  []tasks.Task{{Text: "ligar pro cliente", Due: "2026-10-09"}, {Text: "feita", Done: true}},
	}
	d := Digest(in)
	for _, want := range []string{
		claude.ReplyFile, "### rot-9", "Jira: ROT-9 — Em Andamento", "MR: hades!42", "travado esperando por mim",
		"pedido esperando", "> cuidado com o cache", "Do you want to proceed?", "bode_os",
		"ROT-10 [Aberto] atrasada · prazo 2026-10-01 (ATRASADA)", "front!7 aguardando a minha revisão", "ligar pro cliente",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("digest sem %q", want)
		}
	}
	for _, unwanted := range []string{"ROT-9 [Em Andamento]", "feita"} {
		if strings.Contains(d, unwanted) {
			t.Errorf("digest não deveria ter %q", unwanted)
		}
	}
}

func TestReplyAndOthers(t *testing.T) {
	dir := t.TempDir()
	before := time.Now().Add(-time.Minute)
	if _, _, ok := ReadReply(dir, before); ok {
		t.Fatal("sem arquivo não há resposta")
	}
	if err := os.WriteFile(filepath.Join(dir, claude.ReplyFile), []byte("# ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, _, ok := ReadReply(dir, before); !ok || text != "# ok" {
		t.Fatalf("ReadReply = %q %v", text, ok)
	}
	if _, _, ok := ReadReply(dir, time.Now().Add(time.Minute)); ok {
		t.Fatal("resposta velha não conta")
	}

	snap := Snapshot{Workspaces: []herdr.Workspace{{ID: "w1", Label: "wmonit"}, {ID: "w2", Label: "abc"}, {ID: "w3", Label: "bode_os"}}}
	others := Others(snap, []session.Session{{Label: "abc", Status: session.StatusActive}})
	if len(others) != 1 || others[0].Label != "bode_os" {
		t.Errorf("Others = %+v", others)
	}
}
