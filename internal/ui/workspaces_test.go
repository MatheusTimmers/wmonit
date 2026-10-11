package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/demo"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/history"
	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
)

func newWorkspaceModel(t *testing.T, cfg config.Config, sessions []session.Session) (Model, *demo.Herdr) {
	t.Helper()
	dir := t.TempDir()
	store, _ := tasks.LoadFrom(dir)
	hist, _ := history.LoadFrom(dir)
	sess, _ := session.LoadFrom(dir)
	sess.Sessions = sessions
	h := demo.NewHerdr()
	m := New(cfg, store, hist, sess, demo.GitLabSource{}, demo.JiraSource{}, h, "")
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 50})
	m = mm.(Model)
	m.fetch.gl = demo.GitLab()
	m.fetch.ji = demo.Jira()
	m.tab = tabSessoes
	return poll(t, m), h
}

func poll(t *testing.T, m Model) Model {
	t.Helper()
	mm, _ := m.Update(m.pollHerdr()())
	return mm.(Model)
}

func press(t *testing.T, m Model, key tea.KeyPressMsg) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(key)
	return mm.(Model), cmd
}

func settle(m Model, h hub.Herdr) (Model, []tea.Cmd) {
	t0 := time.Now()
	first := hub.Poll(h)
	first.At = t0
	m.applySnapshot(first)
	second := hub.Poll(h)
	second.At = t0.Add(3 * time.Second)
	cmds := m.applySnapshot(second)
	return m, cmds
}

func runes(s string) tea.KeyPressMsg {
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

func deliver(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("esperava um comando")
	}
	mm, _ := m.Update(cmd())
	return mm.(Model)
}

func demoSessions() []session.Session {
	return []session.Session{
		{ID: "a", Key: "ROT-512", Title: "histórico", Issue: "ROT-512", Service: "backoffice", Kind: session.KindIssue, Label: "rot-512", Workspace: "w1", Status: session.StatusActive},
		{ID: "b", Key: "hades!9470", Title: "relatório", MR: "hades!9470", Service: "hades", Kind: session.KindMR, Mode: session.ModeReview, Label: "hades-9470", Workspace: "w2", Status: session.StatusActive},
		{ID: "c", Key: "ROT-1", Title: "antiga", Service: "hades", Kind: session.KindIssue, Label: "rot-1", Status: session.StatusCompleted},
	}
}

func TestWorkspaceRowsAndView(t *testing.T) {
	m, _ := newWorkspaceModel(t, config.Config{}, demoSessions())
	rows := m.wsRows()
	kinds := []rowKind{rowHub, rowSession, rowSession, rowOther, rowSession}
	if len(rows) != len(kinds) {
		t.Fatalf("rows = %d, esperado %d", len(rows), len(kinds))
	}
	for i, k := range kinds {
		if rows[i].kind != k {
			t.Errorf("row %d kind = %v, esperado %v", i, rows[i].kind, k)
		}
	}
	if rows[3].other.Label != "bode_os" || rows[4].sess.ID != "c" {
		t.Errorf("ordem inesperada: %+v", rows)
	}
	if m.sess.Sessions[0].Agent != "rot-512" || m.sess.Sessions[1].Agent != "hades-9470" {
		t.Errorf("reconcile não preencheu os agents: %+v", m.sess.Sessions)
	}
	view, _ := m.viewSessoes()
	for _, want := range []string{"Pendências", "trabalhando", "precisa de você", "bode_os", "Concluídos", "🔗 ROT-512", "🔀 hades!9470"} {
		if !strings.Contains(view, want) {
			t.Errorf("view sem %q:\n%s", want, view)
		}
	}
	if out := m.render(); !strings.Contains(out, "Workspaces") {
		t.Error("aba sem o nome Workspaces")
	}
}

func TestNotifiesWhenAgentBlocks(t *testing.T) {
	m, h := newWorkspaceModel(t, config.Config{}, demoSessions())
	m = poll(t, m)
	snap := hub.Poll(h)
	for i := range snap.Agents {
		if snap.Agents[i].Name == "rot-512" {
			snap.Agents[i].Status = herdr.StatusBlocked
		}
	}
	if cmds := m.applySnapshot(snap); len(cmds) != 2 {
		t.Fatalf("esperava a notificação e a checagem da tela, veio %d", len(cmds))
	}
	if cmds := m.applySnapshot(snap); len(cmds) != 0 {
		t.Fatalf("sem mudança não notifica de novo: %d", len(cmds))
	}
}

func TestPendingPromptIsDelivered(t *testing.T) {
	sessions := demoSessions()
	sessions[1].PendingPrompt = "leia a tarefa"
	m, h := newWorkspaceModel(t, config.Config{}, sessions)
	if m.sess.Sessions[1].PendingPrompt == "" {
		t.Fatal("agent travado não deveria receber o pedido")
	}
	h2 := &readyHerdr{Herdr: h}
	m.hr = h2
	cut := hub.Poll(h2)
	cut.At = time.Now()
	if cmds := m.applySnapshot(cut); len(cmds) != 0 {
		t.Fatalf("recém-pronto ainda não recebe o pedido: %d", len(cmds))
	}
	m, cmds := settle(m, h2)
	if len(cmds) != 1 {
		t.Fatalf("esperava o comando de entrega, veio %d", len(cmds))
	}
	m = deliver(t, m, cmds[0])
	if m.sess.Sessions[1].PendingPrompt != "" || h2.prompted != "leia a tarefa" {
		t.Fatalf("pedido não entregue: %+v %q", m.sess.Sessions[1], h2.prompted)
	}
}

type readyHerdr struct {
	*demo.Herdr
	prompted string
}

func (r *readyHerdr) State() (herdr.State, error) {
	st, err := r.Herdr.State()
	for i := range st.Agents {
		st.Agents[i].Status = herdr.StatusIdle
	}
	return st, err
}

func (r *readyHerdr) Prompt(agent, text string) error {
	r.prompted = text
	return r.Herdr.Prompt(agent, text)
}

func gitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func TestCreateFreeWorkspace(t *testing.T) {
	sources := t.TempDir()
	repo := filepath.Join(sources, "svc")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, repo)
	cfg := config.Config{Claude: config.Claude{SourcesDir: sources}, Herdr: config.Herdr{Editor: "nvim ."}}
	m, h := newWorkspaceModel(t, cfg, nil)

	m, _ = press(t, m, runes("n"))
	f := m.sui.form
	if m.mode != modeForm || f == nil || f.repo != "svc" {
		t.Fatalf("esperava o formulário de criação: mode=%v form=%+v", m.mode, f)
	}
	f.note = "ajustar o rodapé\ncom o novo selo"
	mm, cmd := m.submitForm(f)
	m = deliver(t, mm.(Model), cmd)

	if len(m.sess.Sessions) != 1 {
		t.Fatalf("sessão não criada: %s", m.sui.info)
	}
	s := m.sess.Sessions[0]
	if s.Label != "svc" || s.Workspace == "" || s.Agent != "svc" || s.Title != "ajustar o rodapé" || s.Worktree != repo || s.PendingPrompt != claude.Kickoff() {
		t.Fatalf("sessão = %+v", s)
	}
	m, cmds := settle(m, h)
	if len(cmds) != 1 {
		t.Fatalf("esperava a entrega da tarefa, veio %d comandos", len(cmds))
	}
	m = deliver(t, m, cmds[0])
	if m.sess.Sessions[0].PendingPrompt != "" {
		t.Fatalf("tarefa não entregue: %s", m.sui.info)
	}
	data, err := os.ReadFile(filepath.Join(repo, claude.TaskFile))
	if err != nil || !strings.Contains(string(data), "com o novo selo") {
		t.Fatalf("arquivo da tarefa = %q, %v", data, err)
	}
	cmd2 := exec.Command("git", "status", "--porcelain")
	cmd2.Dir = repo
	if out, _ := cmd2.Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("o arquivo da tarefa deveria ficar fora do git: %s", out)
	}
	if st, _ := h.State(); st.Workspaces[len(st.Workspaces)-1].Label != "svc" {
		t.Errorf("workspace não criado no herdr: %+v", st.Workspaces)
	}
}

func TestAdoptAndLink(t *testing.T) {
	m, _ := newWorkspaceModel(t, config.Config{Jira: config.Jira{URL: "https://jira"}}, nil)
	rows := m.wsRows()
	if len(rows) < 4 || rows[3].other.Label != "bode_os" {
		t.Fatalf("rows = %+v", rows)
	}
	m.cursor = 3
	m, cmd := press(t, m, runes("u"))
	m = deliver(t, m, cmd)
	m = poll(t, m)
	if len(m.sess.Sessions) != 1 || m.sess.Sessions[0].Label != "bode_os" || m.sess.Sessions[0].Workspace != "w3" {
		t.Fatalf("adoção = %+v (%s)", m.sess.Sessions, m.sui.info)
	}

	for i, r := range m.wsRows() {
		if r.kind == rowSession {
			m.cursor = i
		}
	}
	m, _ = press(t, m, runes("L"))
	if m.mode != modePicking || m.sui.pick.purpose != pickLink || len(m.sui.pick.values) == 0 {
		t.Fatalf("picker de link não abriu: %+v", m.sui.pick)
	}
	issue := ""
	for i, v := range m.sui.pick.values {
		if strings.HasPrefix(v, "issue:") {
			m.sui.pick.cursor = i
			issue = strings.TrimPrefix(v, "issue:")
			break
		}
	}
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	s := m.sess.Sessions[0]
	if s.Issue != issue || s.Key != issue || s.URL != "https://jira/browse/"+issue {
		t.Fatalf("link = %+v", s)
	}
}

func TestAskHubWritesDigestAndReadsReply(t *testing.T) {
	m, h := newWorkspaceModel(t, config.Config{}, demoSessions())
	m, cmd := press(t, m, runes("i"))
	if m.mode != modeDetail || !m.detail.hub || !m.sui.hubWaiting {
		t.Fatalf("i deveria abrir o painel de pendências: mode=%v", m.mode)
	}
	m = deliver(t, m, cmd)
	if !m.sui.hubPending {
		t.Fatalf("hub recém-criado deveria ficar com o pedido pendente: %s", m.sui.info)
	}
	if req, ok := hub.LoadRequest(m.hubDir()); !ok || !req.Pending {
		t.Fatalf("o pedido pendente deveria ficar gravado: %+v", req)
	}
	m, cmds := settle(m, h)
	if len(cmds) != 1 || !m.sui.hubKicking {
		t.Fatalf("esperava o envio ao hub: %d cmds, kicking=%v", len(cmds), m.sui.hubKicking)
	}
	m = deliver(t, m, cmds[0])
	if m.sui.hubPending || m.sui.hubKicking {
		t.Fatal("depois do envio o pedido não está mais pendente")
	}
	if req, _ := hub.LoadRequest(m.hubDir()); req.Pending {
		t.Fatal("o arquivo do pedido deveria registrar o envio")
	}
	digest, err := os.ReadFile(filepath.Join(m.hubDir(), claude.DigestFile))
	if err != nil || !strings.Contains(string(digest), "### hades-9470") || !strings.Contains(string(digest), "Do you want to make this edit") {
		t.Fatalf("digest = %s, %v", digest, err)
	}
	if _, ok := hub.Poll(h).Agent(hub.HubAgent); !ok {
		t.Fatal("agent de pendências não foi criado")
	}

	reply := filepath.Join(m.hubDir(), claude.ReplyFile)
	if err := os.WriteFile(reply, []byte("## Travado\n\n- hades-9470 espera você"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(reply, later, later); err != nil {
		t.Fatal(err)
	}
	m = poll(t, m)
	if m.sui.hubWaiting || !strings.Contains(m.sui.hubReply, "espera você") || !strings.Contains(m.detail.body, "Travado") {
		t.Fatalf("resposta não carregada: waiting=%v body=%q", m.sui.hubWaiting, m.detail.body)
	}
}

func TestNewSessionFromItemMode(t *testing.T) {
	m, _ := newWorkspaceModel(t, config.Config{}, nil)
	mine := m.myMRs()
	if len(mine) == 0 || m.fetch.gl == nil || len(m.fetch.gl.ReviewPending) == 0 {
		t.Skip("demo sem MRs suficientes")
	}
	if s, _, create := m.newSessionFromItem(&focusItem{mr: &mine[0]}); s.Mode != session.ModeImplement || create || s.MR == "" {
		t.Errorf("meu MR deveria ser desenvolvimento: %+v", s)
	}
	other := m.fetch.gl.ReviewPending[0]
	if s, _, _ := m.newSessionFromItem(&focusItem{mr: &other}); s.Mode != session.ModeReview {
		t.Errorf("MR de outra pessoa deveria ser revisão: %+v", s)
	}
}

func TestCreateIssueWorkspaceOnlyContext(t *testing.T) {
	sources := t.TempDir()
	m0, _ := newWorkspaceModel(t, config.Config{}, nil)
	if m0.fetch.ji == nil || len(m0.fetch.ji.Open) == 0 {
		t.Skip("demo sem issues")
	}
	issue := m0.fetch.ji.Open[0]
	repo := filepath.Join(sources, "backoffice")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, repo)
	cfg := config.Config{Claude: config.Claude{
		SourcesDir:   sources,
		WorktreesDir: filepath.Join(sources, ".worktrees"),
		BranchPrefix: "feature/",
	}}
	m, h := newWorkspaceModel(t, cfg, nil)

	m.tab = tabJira
	mm, _ := m.startSession(&focusItem{issue: &issue})
	m = mm.(Model)
	f := m.sui.form
	if m.mode != modeForm || m.tab != tabSessoes || f == nil || f.branch != "feature/"+issue.Key {
		t.Fatalf("esperava o formulário na aba Workspaces: mode=%v tab=%v form=%+v", m.mode, m.tab, f)
	}
	f.note = "só abrir por enquanto"
	f.action = actionCreate
	mm, cmd := m.submitForm(f)
	m = deliver(t, mm.(Model), cmd)

	if len(m.sess.Sessions) != 1 {
		t.Fatalf("sessão não criada: %s", m.sui.info)
	}
	s := m.sess.Sessions[0]
	if s.Issue != issue.Key || s.Branch != "feature/"+issue.Key || !strings.HasPrefix(s.Worktree, cfg.Claude.WorktreesDir) || s.PendingPrompt != "" {
		t.Fatalf("sessão = %+v", s)
	}
	out, err := exec.Command("git", "-C", s.Worktree, "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(out)) != "feature/"+issue.Key {
		t.Fatalf("worktree sem a branch: %q %v", out, err)
	}
	data, err := os.ReadFile(filepath.Join(s.Worktree, claude.TaskFile))
	if err != nil || !strings.Contains(string(data), "espere minhas instruções") || !strings.Contains(string(data), "só abrir por enquanto") {
		t.Fatalf("contexto = %q, %v", data, err)
	}
	for _, a := range hub.Poll(h).Agents {
		if a.Name == s.Agent && a.Status != herdr.StatusIdle {
			t.Errorf("ctrl+n não deveria mandar prompt; agent %s está %s", a.Name, a.Status)
		}
	}

	m = poll(t, m)
	for i, r := range m.wsRows() {
		if r.kind == rowSession {
			m.cursor = i
		}
	}
	m, cmd = press(t, m, runes("f"))
	m = deliver(t, m, cmd)
	if m.sess.Sessions[0].Active() {
		t.Fatalf("f deveria concluir: %s", m.sui.info)
	}
	if _, err := os.Stat(s.Worktree); !os.IsNotExist(err) {
		t.Errorf("worktree deveria ter sido removido: %v", err)
	}
	if _, ok := hub.Poll(h).WorkspaceByLabel(s.Label); ok {
		t.Error("workspace deveria ter sido fechado")
	}
}

func TestPickerScrollsAndFilters(t *testing.T) {
	m, _ := newWorkspaceModel(t, config.Config{}, nil)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = mm.(Model)
	var labels, values []string
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("repo-%02d", i)
		labels = append(labels, name)
		values = append(values, "issue:"+name)
	}
	m.sui.pick = pickState{purpose: pickLink, title: "escolha", labels: labels, plain: labels, values: values}
	m.mode = modePicking
	m.tab = tabSessoes

	visible := func(name string) bool { return strings.Contains(m.render(), name) }
	if !visible("repo-00") || visible("repo-40") {
		t.Fatal("começo da lista deveria estar na tela, o fim não")
	}
	for i := 0; i < 40; i++ {
		m, _ = press(t, m, runes("j"))
	}
	if !visible("repo-40") || visible("repo-00") {
		t.Fatalf("a tela deveria acompanhar o cursor (offset %d)", m.vp.YOffset())
	}
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.sui.pick.cursor != 59 || !visible("repo-59") {
		t.Fatalf("end: cursor %d", m.sui.pick.cursor)
	}
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.sui.pick.cursor >= 59 || !visible(fmt.Sprintf("repo-%02d", m.sui.pick.cursor)) {
		t.Fatalf("pgup: cursor %d", m.sui.pick.cursor)
	}
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	if m.sui.pick.cursor != 0 || !visible("repo-00") {
		t.Fatalf("home: cursor %d", m.sui.pick.cursor)
	}

	m, _ = press(t, m, runes("/"))
	for _, r := range "rp42" {
		m, _ = press(t, m, runes(string(r)))
	}
	if vis := m.sui.pick.visible(); len(vis) == 0 || m.sui.pick.plain[vis[0]] != "repo-42" {
		t.Fatalf("filtro fuzzy: %v", vis)
	}
	if visible("repo-00") || !visible("repo-42") {
		t.Fatal("a tela deveria mostrar só o que casa com o filtro")
	}
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.sui.pick.filtering || m.sui.pick.query != "rp42" {
		t.Fatalf("enter deveria fixar o filtro: %+v", m.sui.pick)
	}
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeNormal {
		t.Fatalf("enter deveria escolher, mode = %v", m.mode)
	}
}
