package ui

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/agents"
	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/demo"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/history"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
)

// fakeAgents grava o que a UI pede ao Claude Code e devolve o roster dado.
type fakeAgents struct {
	demo.AgentsSource
	live     []agents.Live
	launched *agents.LaunchOpts
	renamed  map[string]string
	removed  []string
	stopped  []string
}

func (f *fakeAgents) Stop(ref string) error   { f.stopped = append(f.stopped, ref); return nil }
func (f *fakeAgents) Remove(ref string) error { f.removed = append(f.removed, ref); return nil }

func (f *fakeAgents) Rename(sessionID, name string) error {
	if f.renamed == nil {
		f.renamed = map[string]string{}
	}
	f.renamed[sessionID] = name
	return nil
}

func (f *fakeAgents) List() ([]agents.Live, error) { return f.live, nil }
func (f *fakeAgents) Launch(o agents.LaunchOpts) (string, error) {
	f.launched = &o
	return "abc12345", nil
}
func (f *fakeAgents) Transcript(cwd, id string) (agents.Transcript, error) {
	return agents.Transcript{}, errors.New("sem transcript")
}
func (f *fakeAgents) AttachCmd(ref string) *exec.Cmd { return exec.Command("true") }

func newAgentsModel(t *testing.T, ag agentSource, sessions ...session.Session) Model {
	t.Helper()
	dir := t.TempDir()
	store, _ := tasks.LoadFrom(dir)
	hist, _ := history.LoadFrom(dir)
	sess, _ := session.LoadFrom(dir)
	sess.Sessions = sessions
	m := New(config.Config{}, store, hist, sess, demo.GitLabSource{}, demo.JiraSource{}, ag, "")
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	m = mm.(Model)
	m.tab = tabAgentes
	return m
}

// read roda a leitura do agent view como o tick faria.
func read(t *testing.T, m Model) Model {
	t.Helper()
	msg := m.fetchAgents()()
	mm, _ := m.Update(msg)
	return mm.(Model)
}

// A aba junta as sessões do wmonit com o roster, lista as outras sessões
// vivas à parte e aponta o arquivo editado por dois agents.
func TestAgentsTabDemo(t *testing.T) {
	dir := t.TempDir()
	demo.SeedData(dir)
	sess, _ := session.LoadFrom(dir)
	m := read(t, newAgentsModel(t, demo.AgentsSource{}, sess.Sessions...))

	rows := m.agentRows()
	if len(rows) != 3 {
		t.Fatalf("esperado 2 do wmonit + 1 de fora, veio %d", len(rows))
	}
	if rows[2].sess != nil || rows[2].live.Name != "investigação de leak" {
		t.Errorf("a sessão de fora devia vir por último: %+v", rows[2])
	}
	view, _ := m.viewAgentes()
	for _, want := range []string{
		"ROT-512", "● trabalhando", "ctx 38%", "$2.41", "1 subagent(s)",
		"hades!9470", "◆ aguarda você", "PEDIR AJUSTES",
		"Criados pelo wmonit (2)", "Outras sessões do agent view (1)", "investigação de leak",
		"backoffice:source/ordensu.pas editado por ROT-512, investigação de leak",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("aba Agentes sem %q:\n%s", want, view)
		}
	}
}

// Um agent do wmonit que para de trabalhar gera aviso; o primeiro estado
// lido só vira linha de base.
func TestAgentFinishNotifies(t *testing.T) {
	ag := &fakeAgents{live: []agents.Live{{PID: 1, ID: "abc12345", SessionID: "uuid-1", Kind: "background", Status: "busy", State: "working"}}}
	m := newAgentsModel(t, ag, session.Session{ID: "s1", Key: "ROT-1", AgentID: "abc12345", Status: session.StatusRunning})

	m = read(t, m)
	if !m.sui.busy["s1"] {
		t.Fatal("agent devia constar como trabalhando")
	}
	if m.sess.Sessions[0].ClaudeID != "uuid-1" {
		t.Errorf("ClaudeID devia ser aprendido do roster, veio %q", m.sess.Sessions[0].ClaudeID)
	}

	ag.live[0].Status, ag.live[0].State = "idle", "done"
	msg := m.fetchAgents()()
	if cmd := m.applyAgents(msg.(agentsMsg)); cmd == nil {
		t.Error("busy → idle devia gerar notificação")
	}
	if m.sui.busy["s1"] {
		t.Error("agent devia constar como parado")
	}
}

// Agent já encerrado (sem pid no --all) não conta como vivo: a linha cai
// para o estado do store e não aparece entre as outras sessões.
func TestExitedAgentIsNotLive(t *testing.T) {
	ag := &fakeAgents{live: []agents.Live{
		{ID: "abc12345", SessionID: "uuid-1", Kind: "background", State: "done"},
		{ID: "zzz", SessionID: "uuid-2", Kind: "background", State: "done"},
	}}
	m := read(t, newAgentsModel(t, ag, session.Session{ID: "s1", Key: "ROT-1", AgentID: "abc12345", Status: session.StatusRunning}))
	rows := m.agentRows()
	if len(rows) != 1 || rows[0].live != nil {
		t.Fatalf("esperado só a sessão do wmonit, sem agent vivo: %+v", rows)
	}
	if got := liveLabel(rows[0]); !strings.Contains(got, "encerrado") {
		t.Errorf("label = %q, esperado 'encerrado'", got)
	}
}

// O lançamento vai para o worktree da sessão, com nome legível e o prompt
// de desenvolvimento montado do contexto.
func TestLaunchAgent(t *testing.T) {
	ag := &fakeAgents{}
	s := session.Session{ID: "s1", Key: "ROT-512", Title: "endpoint de histórico", Kind: session.KindIssue,
		Service: "backoffice", Worktree: `c:\Fontes\.worktrees\s1`, UserNote: "paginar por conta", Status: session.StatusPending}
	m := newAgentsModel(t, ag, s)
	m.cfg.Claude.Model, m.cfg.Claude.PermissionMode = "opus", "acceptEdits"

	msg := m.launchAgentCmd(s)()
	mm, _ := m.Update(msg)
	m = mm.(Model)

	if ag.launched == nil {
		t.Fatal("Launch não foi chamado")
	}
	o := *ag.launched
	if o.Dir != s.Worktree || o.Name != "ROT-512 endpoint de histórico" || o.Model != "opus" || o.PermissionMode != "acceptEdits" {
		t.Errorf("LaunchOpts = %+v", o)
	}
	for _, want := range []string{"ROT-512", "paginar por conta", "NÃO faça push"} {
		if !strings.Contains(o.Prompt, want) {
			t.Errorf("prompt sem %q", want)
		}
	}
	got, _ := m.sess.Get("s1")
	if got.Status != session.StatusRunning || got.AgentID != "abc12345" {
		t.Errorf("sessão depois do lançamento = %+v", got)
	}
}

// O agent livre roda direto no repositório, sem worktree próprio.
func TestFreeAgentUsesRepo(t *testing.T) {
	m := newAgentsModel(t, &fakeAgents{})
	m.cfg.Claude.SourcesDir = `c:\Fontes`
	sess := session.Session{Kind: session.KindFree, Service: "hades", UserNote: "investigar o leak de memória\ndetalhes…"}
	_, cmd := m.launchCreate(sess, false)
	created := cmd().(sessCreatedMsg)
	if created.err != nil {
		t.Fatal(created.err)
	}
	s := created.sess
	if s.Worktree != s.Repo || s.OwnsWorktree() || s.Key != "hades" || s.Title != "investigar o leak de memória" {
		t.Errorf("agent livre = %+v", s)
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
	is := jira.Issue{Key: "ROT-9", Summary: "x"}
	if s, _, create := m.newSessionFromItem(&focusItem{issue: &is}); s.Kind != session.KindIssue || !create || s.Branch != "ROT-9" {
		t.Errorf("issue: %+v create=%v", s, create)
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

// O haiku nomeia o agent: a linha mostra o nome e o que falta, a leitura
// fica no store e o nome vai para a sessão (com o ticket na frente).
func TestAnalyzeNamesAgents(t *testing.T) {
	ag := &fakeAgents{live: []agents.Live{
		{PID: 1, ID: "abc12345", SessionID: "uuid-1", Kind: "background", Status: "idle", State: "done"},
		{PID: 2, SessionID: "uuid-2", Cwd: `c:\Fontes\wdc`, Kind: "interactive", Name: "email reading task", Status: "idle"},
	}}
	m := read(t, newAgentsModel(t, ag, session.Session{ID: "s1", Key: "ROT-1", AgentID: "abc12345", Status: session.StatusRunning}))

	for _, r := range m.agentRows() {
		mm, _ := m.Update(m.analyze(r)())
		m = mm.(Model)
	}
	if ag.renamed["uuid-1"] != "ROT-1 · Histórico paginado de ordens" || ag.renamed["uuid-2"] != "Histórico paginado de ordens" {
		t.Errorf("renames = %v", ag.renamed)
	}
	got, _ := m.sess.Get("s1")
	if got.AIMissing != "registrar a rota e revisar o diff" || got.AIAt == nil {
		t.Errorf("leitura não foi para o store: %+v", got)
	}
	view, _ := m.viewAgentes()
	if !strings.Contains(view, "🧠 Histórico paginado de ordens") || !strings.Contains(view, "registrar a rota") {
		t.Errorf("aba sem a leitura do haiku:\n%s", view)
	}
	if rows := m.agentRows(); rows[1].label() != "Histórico paginado de ordens" {
		t.Errorf("sessão de fora deveria usar o nome do haiku, veio %q", rows[1].label())
	}
	if len(m.sui.analyzing) != 0 {
		t.Errorf("análises em voo deveriam ter zerado: %v", m.sui.analyzing)
	}
}

// A conversa abre numa aba nova do Windows Terminal, na pasta do agent; o
// ';' do título não pode virar separador de comando do wt.
func TestExternalTerminal(t *testing.T) {
	cfg := config.Config{}
	cfg.Editor.Terminal = "wt"
	inner := exec.Command("claude", "attach", "abc12345")
	inner.Dir = `c:\Fontes\.worktrees\s1`
	ext := externalTerminal(cfg, "ROT-1; x", inner)
	want := []string{"wt", "-w", "0", "new-tab", "--title", "ROT-1, x", "-d", `c:\Fontes\.worktrees\s1`, "claude", "attach", "abc12345"}
	if strings.Join(ext.Args, "|") != strings.Join(want, "|") {
		t.Errorf("args = %q", ext.Args)
	}
	cfg.Editor.Terminal = ""
	if externalTerminal(cfg, "x", inner) != nil {
		t.Error("sem terminal configurado deveria devolver nil")
	}
}

func press(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return mm.(Model), cmd
}

// Nas sessões de fora do wmonit as teclas também respondem: d pede
// confirmação e só então tira a sessão do agent view; v abre o diff da pasta;
// o e s avisam em vez de não fazer nada.
func TestOutsideSessionKeys(t *testing.T) {
	ag := &fakeAgents{live: []agents.Live{{PID: 1, ID: "3f1b9a62", SessionID: "uuid-3", Cwd: t.TempDir(), Kind: "background", Name: "API Key Gateway Branch Review", Status: "idle", State: "done"}}}
	m := read(t, newAgentsModel(t, ag))

	m, cmd := press(t, m, "d")
	if cmd != nil || !strings.Contains(m.sui.sessInfo, "d de novo") {
		t.Fatalf("primeiro d deveria só pedir confirmação: %q", m.sui.sessInfo)
	}
	m, cmd = press(t, m, "d")
	if cmd == nil {
		t.Fatal("segundo d deveria remover")
	}
	mm, _ := m.Update(cmd())
	m = mm.(Model)
	if len(ag.removed) != 1 || ag.removed[0] != "3f1b9a62" || len(ag.stopped) != 1 {
		t.Fatalf("stop/rm = %v / %v", ag.stopped, ag.removed)
	}

	// Outra tecla no meio cancela a confirmação.
	m, _ = press(t, m, "f")
	m, _ = press(t, m, "j")
	m, cmd = press(t, m, "f")
	if cmd != nil {
		t.Error("confirmação deveria ter sido cancelada pela tecla do meio")
	}

	m, _ = press(t, m, "o")
	if !strings.Contains(m.sui.sessInfo, "não tem issue/MR") {
		t.Errorf("o sem link deveria avisar: %q", m.sui.sessInfo)
	}
	m, _ = press(t, m, "s")
	if !strings.Contains(m.sui.sessInfo, "só vale para agents criados pelo wmonit") {
		t.Errorf("s fora do wmonit deveria avisar: %q", m.sui.sessInfo)
	}
	m, cmd = press(t, m, "v")
	if m.mode != modeDetail || cmd == nil {
		t.Errorf("v deveria abrir o diff da pasta (mode=%v)", m.mode)
	}
}
