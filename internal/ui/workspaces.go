package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

const (
	wsPollEvery   = 3 * time.Second
	wsPollOffline = 10 * time.Second
	deliverAfter  = 2500 * time.Millisecond
	screenLines   = 60
)

type wsTickMsg struct{}

type wsSnapMsg struct{ snap hub.Snapshot }

type sessSavedMsg struct {
	sess session.Session
	info string
	err  error
}

type sessActionMsg struct {
	id     string
	remove bool
	status session.Status
	info   string
	err    error
}

type deliveredMsg struct {
	id   string
	sent bool
	err  error
}

type hubAskedMsg struct {
	at    time.Time
	ready bool
	err   error
}

type infoMsg struct {
	info string
	err  error
}

type pickPurpose int

const (
	pickService pickPurpose = iota
	pickLink
)

type pickState struct {
	purpose pickPurpose
	title   string
	labels  []string
	values  []string
	cursor  int
	target  string
}

type pendingSession struct {
	sess         session.Session
	createBranch bool
	guess        string
	services     []string
}

type rowKind int

const (
	rowHub rowKind = iota
	rowSession
	rowOther
)

type wsRow struct {
	kind  rowKind
	sess  session.Session
	other herdr.Workspace
}

func wsTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return wsTickMsg{} })
}

func (m Model) pollHerdr() tea.Cmd {
	h := m.hr
	return func() tea.Msg { return wsSnapMsg{snap: hub.Poll(h)} }
}

func (m Model) hubDir() string { return hub.HubDir(m.sess.Dir()) }

func (m Model) claudeArgs() []string {
	return hub.ClaudeArgs(m.cfg.Claude.Model, m.cfg.Claude.PermissionMode)
}

func (m Model) updateWorkspaces(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case wsTickMsg:
		return m, m.pollHerdr(), true

	case wsSnapMsg:
		cmds := m.applySnapshot(msg.snap)
		next := wsPollEvery
		if !msg.snap.Online() {
			next = wsPollOffline
		}
		return m, tea.Batch(append(cmds, wsTick(next))...), true

	case sessSavedMsg:
		if msg.err != nil {
			m.sui.info = errStyle.Render(msg.err.Error())
			return m, nil, true
		}
		if !m.sess.Update(msg.sess.ID, func(s *session.Session) { *s = msg.sess }) {
			m.sess.Add(msg.sess)
		}
		m.save(m.sess.Save())
		m.sui.info = msg.info
		if msg.sess.Err != "" {
			m.sui.info = errStyle.Render(msg.sess.Name() + ": " + msg.sess.Err)
		}
		return m, m.pollHerdr(), true

	case sessActionMsg:
		if msg.err != nil {
			m.sui.info = errStyle.Render(msg.err.Error())
			return m, nil, true
		}
		if msg.remove {
			m.sess.Delete(msg.id)
		} else if msg.status != "" {
			m.sess.Update(msg.id, func(s *session.Session) {
				s.Status = msg.status
				s.Workspace, s.Agent, s.PendingPrompt = "", "", ""
				now := time.Now()
				s.Finished = &now
			})
		}
		if n := len(m.wsRows()); m.cursor >= n && m.cursor > 0 {
			m.cursor = n - 1
		}
		m.save(m.sess.Save())
		m.sui.info = msg.info
		return m, m.pollHerdr(), true

	case deliveredMsg:
		delete(m.sui.delivering, msg.id)
		if msg.err != nil {
			m.sui.info = errStyle.Render("enviando o pedido: " + msg.err.Error())
			return m, nil, true
		}
		if msg.sent {
			var name string
			m.sess.Update(msg.id, func(s *session.Session) {
				s.PendingPrompt = ""
				name = s.Name()
			})
			m.save(m.sess.Save())
			m.sui.info = okStyle.Render("pedido enviado ao Claude de " + name)
		}
		return m, nil, true

	case hubAskedMsg:
		if msg.err != nil {
			m.sui.hubWaiting = false
			m.sui.info = errStyle.Render("pendências: " + msg.err.Error())
			if m.mode == modeDetail && m.detail.hub {
				m.detail.loading = false
				m.detail.body = errStyle.Render(msg.err.Error())
			}
			return m, nil, true
		}
		m.sui.hubAsked = msg.at
		m.sui.hubPending = !msg.ready
		if m.mode == modeDetail && m.detail.hub {
			m.detail.loading = false
			m.detail.body = m.hubBody()
		}
		if !msg.ready {
			m.sui.info = warnStyle.Render("subindo o agent de pendências — o pedido vai assim que ele estiver pronto (se ele pedir para confirmar a pasta: a na linha 🧭)")
		}
		return m, m.pollHerdr(), true

	case infoMsg:
		if msg.err != nil {
			m.sui.info = errStyle.Render(friendlyErr(msg.err))
		} else {
			m.sui.info = msg.info
		}
		return m, m.pollHerdr(), true
	}
	return m, nil, false
}

func friendlyErr(err error) string {
	switch {
	case herdr.IsCode(err, herdr.CodeAgentBlocked):
		return "o Claude está esperando você (permissão ou pergunta) — a abre o workspace"
	case herdr.IsCode(err, herdr.CodeServerNotRunning):
		return "o herdr não está rodando — abra o herdr e tente de novo"
	case herdr.IsCode(err, herdr.CodePromptStalled):
		return "o Claude não reagiu ao texto — confira no workspace (a)"
	}
	return err.Error()
}

func (m *Model) applySnapshot(snap hub.Snapshot) []tea.Cmd {
	prevOnline := m.sui.snap.Online()
	m.sui.snap = snap
	if !snap.Online() {
		return nil
	}
	var cmds []tea.Cmd
	changed := false
	for i := range m.sess.Sessions {
		if hub.Reconcile(&m.sess.Sessions[i], snap) {
			changed = true
		}
	}
	if changed {
		m.save(m.sess.Save())
	}

	statuses := map[string]string{}
	for _, a := range snap.Agents {
		statuses[a.Name] = a.Status
		old, seen := m.sui.lastStatus[a.Name]
		if !prevOnline || !seen || old == a.Status {
			continue
		}
		label := m.agentOwner(a)
		if label == "" {
			continue
		}
		switch {
		case a.Status == herdr.StatusBlocked:
			cmds = append(cmds, notifyCmd("wmonit — "+label, "precisa de você (permissão ou pergunta)"))
		case old == herdr.StatusWorking && a.Status == herdr.StatusDone:
			cmds = append(cmds, notifyCmd("wmonit — "+label, "o Claude terminou"))
		}
	}
	m.sui.lastStatus = statuses

	ready := map[string]time.Time{}
	for _, a := range snap.Agents {
		if !a.Ready() {
			continue
		}
		since, ok := m.sui.readySince[a.Name]
		if !ok {
			since = snap.At
		}
		ready[a.Name] = since
	}
	m.sui.readySince = ready
	stable := func(name string) bool {
		since, ok := ready[name]
		return ok && snap.At.Sub(since) >= deliverAfter
	}

	for _, s := range m.sess.Sessions {
		if !s.Active() || s.PendingPrompt == "" || m.sui.delivering[s.ID] {
			continue
		}
		if stable(s.Agent) {
			m.sui.delivering[s.ID] = true
			cmds = append(cmds, m.deliverCmd(s))
		}
	}

	if m.sui.hubPending {
		if stable(hub.HubAgent) {
			m.sui.hubPending = false
			cmds = append(cmds, m.hubKickoffCmd())
		}
	}
	if m.sui.hubWaiting {
		if text, at, ok := hub.ReadReply(m.hubDir(), m.sui.hubAsked); ok {
			m.sui.hubWaiting = false
			m.sui.hubReply, m.sui.hubAt = text, at
			cmds = append(cmds, notifyCmd("wmonit — pendências", "a análise está pronta"))
			if m.mode == modeDetail && m.detail.hub {
				m.detail.loading = false
				m.detail.body = m.hubBody()
			}
		}
	}
	return cmds
}

func (m Model) agentOwner(a herdr.Agent) string {
	if a.Name == hub.HubAgent {
		return "pendências"
	}
	for _, s := range m.sess.Sessions {
		if s.Active() && s.Agent == a.Name {
			return s.Name()
		}
	}
	return ""
}

func (m Model) deliverCmd(s session.Session) tea.Cmd {
	h := m.hr
	return func() tea.Msg {
		sent, err := hub.Deliver(h, hub.Poll(h), &s)
		return deliveredMsg{id: s.ID, sent: sent, err: err}
	}
}

func (m Model) wsRows() []wsRow {
	rows := []wsRow{{kind: rowHub}}
	var done []wsRow
	for _, s := range m.sess.Sessions {
		if s.Active() {
			rows = append(rows, wsRow{kind: rowSession, sess: s})
		} else {
			done = append(done, wsRow{kind: rowSession, sess: s})
		}
	}
	for _, w := range hub.Others(m.sui.snap, m.sess.Sessions) {
		rows = append(rows, wsRow{kind: rowOther, other: w})
	}
	return append(rows, done...)
}

func (m Model) selectedRow() *wsRow {
	rows := m.wsRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return nil
	}
	return &rows[m.cursor]
}

func (m Model) newSessionFromItem(it *focusItem) (sess session.Session, guess string, createBranch bool) {
	if it.mr != nil {
		mr := *it.mr
		mode := session.ModeImplement
		if !m.ownsMR(mr) {
			mode = session.ModeReview
		}
		sess = session.Session{
			Key:    mr.ShortRef(),
			Title:  mr.ShortTitle(),
			URL:    mr.WebURL,
			MR:     mr.ShortRef(),
			Issue:  mr.JiraKey(),
			Branch: mr.SourceBranch,
			Kind:   session.KindMR,
			Mode:   mode,
		}
		return sess, mr.Project(), false
	}
	is := *it.issue
	sess = session.Session{
		Key:    is.Key,
		Title:  is.Summary,
		URL:    m.jiraURL(is.Key),
		Issue:  is.Key,
		Branch: m.cfg.Claude.BranchPrefix + is.Key,
		Kind:   session.KindIssue,
	}
	if m.fetch.gl != nil {
		for _, mr := range m.fetch.gl.OpenMRs {
			if mr.JiraKey() == is.Key {
				guess = mr.Project()
				break
			}
		}
	}
	return sess, guess, true
}

func (m Model) jiraURL(key string) string {
	if m.cfg.Jira.URL == "" {
		return ""
	}
	return strings.TrimRight(m.cfg.Jira.URL, "/") + "/browse/" + key
}

func (m Model) ownsMR(mr gitlab.MR) bool {
	for _, x := range m.myMRs() {
		if x.ProjectID == mr.ProjectID && x.IID == mr.IID {
			return true
		}
	}
	return false
}

func (m Model) allMRs() []gitlab.MR {
	if m.fetch.gl == nil {
		return nil
	}
	return append(append([]gitlab.MR{}, m.fetch.gl.OpenMRs...), m.fetch.gl.ReviewPending...)
}

func (m Model) startSession(it *focusItem) (tea.Model, tea.Cmd) {
	sess, guess, create := m.newSessionFromItem(it)
	if sess.Branch == "" {
		m.sui.info = errStyle.Render("MR sem branch de origem — atualize (r) e tente de novo")
		return m, nil
	}
	if m.sess.HasActiveFor(sess.Key) {
		m.sui.info = warnStyle.Render("já existe workspace ativo para " + sess.Key)
		m.tab = tabSessoes
		return m, nil
	}
	return m.pickServiceFor(&pendingSession{sess: sess, createBranch: create, guess: guess})
}

func (m Model) startFreeWorkspace() (tea.Model, tea.Cmd) {
	return m.pickServiceFor(&pendingSession{sess: session.Session{Kind: session.KindFree}})
}

func (m Model) pickServiceFor(p *pendingSession) (tea.Model, tea.Cmd) {
	services, err := worktree.DetectServices(m.cfg.Claude.SourcesDir)
	if err != nil {
		m.sui.info = errStyle.Render("lendo " + m.cfg.Claude.SourcesDir + ": " + err.Error())
		return m, nil
	}
	if len(services) == 0 {
		m.sui.info = errStyle.Render("nenhum repositório git em " + m.cfg.Claude.SourcesDir)
		return m, nil
	}
	p.services = services
	cursor := 0
	for i, s := range services {
		if strings.EqualFold(s, p.guess) {
			cursor = i
		}
	}
	title := "🛠 Em qual repositório criar o workspace?"
	if p.sess.Key != "" {
		title = "🛠 Em qual repositório trabalhar " + p.sess.Key + "?"
	}
	m.sui.pending = p
	m.sui.pick = pickState{purpose: pickService, title: title, labels: services, values: services, cursor: cursor}
	m.tab = tabSessoes
	m.filter = ""
	m.sui.info = ""
	m.mode = modePicking
	m.vp.GotoTop()
	return m, nil
}

func (m Model) describe() (tea.Model, tea.Cmd) {
	m.mode = modeDescribing
	m.sui.descInput.Reset()
	m.sui.descInput.Focus()
	m.vp.GotoTop()
	return m, textarea.Blink
}

func (m Model) updateDescribe(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.sui.pending = nil
		m.sui.target = ""
		m.sui.descInput.Blur()
		m.sui.info = dim.Render("cancelado")
		return m, nil
	case "ctrl+d", "ctrl+s":
		return m.submitText(true)
	case "ctrl+n":
		if m.sui.pending != nil {
			return m.submitText(false)
		}
	case "ctrl+r":
		if p := m.sui.pending; p != nil && p.sess.Kind == session.KindMR {
			if p.sess.IsReview() {
				p.sess.Mode = session.ModeImplement
			} else {
				p.sess.Mode = session.ModeReview
			}
		}
		return m, nil
	case "ctrl+e":
		return m, editNoteCmd(m.cfg, m.sui.descInput.Value())
	}
	var cmd tea.Cmd
	m.sui.descInput, cmd = m.sui.descInput.Update(msg)
	return m, cmd
}

func (m Model) submitText(send bool) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.sui.descInput.Value())
	m.mode = modeNormal
	m.sui.descInput.Blur()
	if p := m.sui.pending; p != nil {
		m.sui.pending = nil
		p.sess.UserNote = text
		return m.launchCreate(*p, send)
	}
	target := m.sui.target
	m.sui.target = ""
	if text == "" {
		m.sui.info = dim.Render("nada enviado")
		return m, nil
	}
	return m, m.sendPromptCmd(target, text)
}

func (m Model) viewDescribe() string {
	var b strings.Builder
	if m.sui.pending == nil {
		b.WriteString(section.Render("💬 Mensagem para "+m.targetName(m.sui.target)) + "\n\n")
		b.WriteString(dim.Render("O texto vai direto para o Claude do workspace, como se você tivesse digitado lá.") + "\n\n")
		b.WriteString(m.sui.descInput.View() + "\n\n")
		b.WriteString(dim.Render("ctrl+d envia · ctrl+e edita no editor · esc cancela"))
		return b.String()
	}
	p := m.sui.pending
	if p.sess.Kind == session.KindFree {
		b.WriteString(section.Render("🆕 Novo workspace em "+p.sess.Service) + "\n\n")
		b.WriteString(dim.Render("Descreva o que fazer. A primeira linha vira o título; vazio = só abre o workspace.") + "\n\n")
	} else {
		head, mode := "📝 Novo workspace — ", okStyle.Render("desenvolvimento")
		expl := "Explique a tarefa; a descrição e os comentários da issue/MR entram junto no contexto (" + claude.TaskFile + ")."
		if p.sess.IsReview() {
			head, mode = "🔎 Nova revisão — ", warnStyle.Render("revisão (só revisa o MR e reporta)")
			expl = "Diga o que olhar com atenção na revisão (opcional); a descrição e os comentários do MR entram no contexto."
		}
		b.WriteString(section.Render(head+p.sess.Key) + " " + p.sess.Title + "\n\n")
		b.WriteString(dim.Render("repositório: ") + p.sess.Service + dim.Render(" · modo: ") + mode)
		if p.sess.Kind == session.KindMR {
			b.WriteString(dim.Render("  (ctrl+r troca)"))
		}
		b.WriteString("\n" + dim.Render(expl) + "\n\n")
	}
	b.WriteString(m.sui.descInput.View() + "\n\n")
	b.WriteString(dim.Render("ctrl+d cria e manda a tarefa ao Claude · ctrl+n só cria · ctrl+e edita no editor · esc cancela"))
	return b.String()
}

func (m Model) updatePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pk := &m.sui.pick
	switch msg.String() {
	case "esc", "q":
		m.mode = modeNormal
		m.sui.pending = nil
		return m, nil
	case "j", "down":
		if pk.cursor < len(pk.values)-1 {
			pk.cursor++
		}
		return m, nil
	case "k", "up":
		if pk.cursor > 0 {
			pk.cursor--
		}
		return m, nil
	case "enter":
		if len(pk.values) == 0 {
			m.mode = modeNormal
			return m, nil
		}
		value := pk.values[pk.cursor]
		m.mode = modeNormal
		switch pk.purpose {
		case pickService:
			if m.sui.pending == nil {
				return m, nil
			}
			m.sui.pending.sess.Service = value
			return m.describe()
		case pickLink:
			return m.applyLink(pk.target, value)
		}
	}
	return m, nil
}

func (m Model) viewPick() string {
	var b strings.Builder
	pk := m.sui.pick
	b.WriteString(section.Render(pk.title) + "\n\n")
	if len(pk.labels) == 0 {
		b.WriteString(dim.Render("(nada para escolher — atualize com r)") + "\n")
	}
	for i, l := range pk.labels {
		cursor := "  "
		if i == pk.cursor {
			cursor = cursorStyle.Render("▌ ")
		}
		b.WriteString(cursor + l + "\n")
	}
	return b.String()
}

func (m Model) launchCreate(p pendingSession, send bool) (tea.Model, tea.Cmd) {
	sess := p.sess
	sess.Repo = filepath.Join(m.cfg.Claude.SourcesDir, sess.Service)
	sess.Status = session.StatusActive
	sess.Created = time.Now()
	base := sess.Key
	if sess.Kind == session.KindFree {
		base = sess.Service
		sess.ID = session.NewID(sess.Service)
		sess.Worktree = sess.Repo
		sess.Title = firstLine(sess.UserNote)
		if sess.Title == "" {
			sess.Title = sess.Service
		}
	} else {
		sess.ID = session.NewID(sess.Key)
		sess.Worktree = filepath.Join(m.cfg.Claude.WorktreesDir, sess.ID)
	}
	snap := m.sui.snap
	sess.Label = herdr.UniqueName(herdr.AgentName(base), func(l string) bool {
		_, inHerdr := snap.WorkspaceByLabel(l)
		return inHerdr || m.sess.LabelTaken(l)
	})

	h, gl, ji := m.hr, m.glSrc, m.jiSrc
	template := m.cfg.Claude.Templates[sess.Service]
	mrs := m.allMRs()
	opts := hub.OpenOpts{ClaudeArgs: m.claudeArgs(), Editor: m.cfg.Herdr.Editor}
	createBranch := p.createBranch
	m.sui.info = dim.Render("criando o workspace " + sess.Label + " em " + sess.Service + "…")
	return m, func() tea.Msg {
		snap := hub.Poll(h)
		if !snap.Online() {
			return sessSavedMsg{err: fmt.Errorf("%s", friendlyErr(snap.Err))}
		}
		if sess.OwnsWorktree() {
			if err := worktree.Add(sess.Repo, sess.Worktree, sess.Branch, createBranch); err != nil {
				return sessSavedMsg{err: err}
			}
		}
		if sess.Kind == session.KindFree {
			if sess.UserNote != "" {
				opts.TaskText = claude.FreePrompt(sess.Service, sess.UserNote)
				if send {
					opts.Kickoff = claude.Kickoff()
				}
			}
		} else {
			ctx := hub.BuildContext(gl, ji, template, sess, mrs)
			opts.TaskText = claude.ContextOnly(ctx)
			if send {
				opts.TaskText = hub.TaskText(ctx, sess)
				opts.Kickoff = claude.Kickoff()
			}
		}
		opened, err := hub.Open(h, snap, sess, opts)
		if err != nil {
			opened.Err = err.Error()
		}
		info := okStyle.Render("workspace " + opened.Label + " criado — a abre no herdr")
		if opened.PendingPrompt != "" {
			info = warnStyle.Render("workspace " + opened.Label + " criado — confirme a pasta no Claude (a); a tarefa vai assim que ele ficar livre")
		}
		return sessSavedMsg{sess: opened, info: info}
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(s, 80)
}

func (m Model) reopenCmd(s session.Session) tea.Cmd {
	h := m.hr
	opts := hub.OpenOpts{ClaudeArgs: m.claudeArgs(), Editor: m.cfg.Herdr.Editor}
	return func() tea.Msg {
		dir := s.Worktree
		if dir == "" {
			dir = s.Repo
		}
		if _, err := os.Stat(dir); err != nil {
			return sessSavedMsg{err: fmt.Errorf("pasta %s não existe mais — conclua com f", dir)}
		}
		snap := hub.Poll(h)
		if !snap.Online() {
			return sessSavedMsg{err: fmt.Errorf("%s", friendlyErr(snap.Err))}
		}
		s.Err = ""
		opened, err := hub.Open(h, snap, s, opts)
		if err != nil {
			return sessSavedMsg{err: err}
		}
		return sessSavedMsg{sess: opened, info: okStyle.Render("workspace " + opened.Label + " reaberto")}
	}
}

func (m Model) adoptCmd(w herdr.Workspace) tea.Cmd {
	h := m.hr
	return func() tea.Msg {
		panes, err := h.Panes(w.ID)
		if err != nil {
			return sessSavedMsg{err: err}
		}
		dir := ""
		for _, p := range panes {
			if p.Cwd != "" {
				dir = p.Cwd
				break
			}
		}
		if dir == "" {
			return sessSavedMsg{err: fmt.Errorf("não achei a pasta do workspace %s", w.Label)}
		}
		repo := dir
		if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
			repo = filepath.FromSlash(strings.TrimSpace(string(out)))
		}
		branch := ""
		if out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output(); err == nil {
			branch = strings.TrimSpace(string(out))
		}
		sess := session.Session{
			ID:        session.NewID(w.Label),
			Title:     w.Label,
			Service:   filepath.Base(repo),
			Repo:      repo,
			Worktree:  repo,
			Branch:    branch,
			Kind:      session.KindFree,
			Label:     w.Label,
			Workspace: w.ID,
			Status:    session.StatusActive,
			Created:   time.Now(),
		}
		return sessSavedMsg{sess: sess, info: okStyle.Render("workspace " + w.Label + " adotado — L liga a uma issue/MR")}
	}
}

func (m Model) openLinkPicker(s session.Session) (tea.Model, tea.Cmd) {
	var labels, values []string
	if m.fetch.ji != nil {
		for _, is := range m.fetch.ji.Open {
			labels = append(labels, warnStyle.Render(is.Key)+" "+truncate(is.Summary, 70)+dim.Render(" · "+is.Status))
			values = append(values, "issue:"+is.Key)
		}
	}
	seen := map[string]bool{}
	for _, mr := range m.allMRs() {
		ref := mr.ShortRef()
		if seen[ref] {
			continue
		}
		seen[ref] = true
		labels = append(labels, okStyle.Render(ref)+" "+truncate(mr.ShortTitle(), 70))
		values = append(values, "mr:"+ref)
	}
	m.sui.pick = pickState{purpose: pickLink, title: "🔗 Ligar " + s.Name() + " a uma issue ou MR", labels: labels, values: values, target: s.ID}
	m.mode = modePicking
	m.vp.GotoTop()
	return m, nil
}

func (m Model) applyLink(id, value string) (tea.Model, tea.Cmd) {
	kind, ref, _ := strings.Cut(value, ":")
	var name string
	m.sess.Update(id, func(s *session.Session) {
		switch kind {
		case "issue":
			s.Issue = ref
			if s.Kind == session.KindFree || s.Key == "" {
				s.Key, s.URL = ref, m.jiraURL(ref)
				if m.fetch.ji != nil {
					for _, is := range m.fetch.ji.Open {
						if is.Key == ref {
							s.Title = is.Summary
						}
					}
				}
			}
		case "mr":
			s.MR = ref
			for _, mr := range m.allMRs() {
				if mr.ShortRef() != ref {
					continue
				}
				if s.Issue == "" {
					s.Issue = mr.JiraKey()
				}
				if s.Kind == session.KindFree || s.Key == "" {
					s.Key, s.URL, s.Title = ref, mr.WebURL, mr.ShortTitle()
				}
			}
		}
		name = s.Name()
	})
	m.save(m.sess.Save())
	m.sui.info = okStyle.Render(name + " ligado a " + ref)
	return m, nil
}

func (m Model) targetName(target string) string {
	switch {
	case target == hub.HubAgent:
		return "o agent de pendências"
	case strings.HasPrefix(target, "ws:"):
		return strings.TrimPrefix(target, "ws:")
	}
	if s, ok := m.sess.Get(target); ok {
		return s.Name()
	}
	return target
}

func (m Model) sendPromptCmd(target, text string) tea.Cmd {
	h := m.hr
	args := m.claudeArgs()
	hubDir := m.hubDir()
	sess, isSession := m.sess.Get(target)
	return func() tea.Msg {
		snap := hub.Poll(h)
		if !snap.Online() {
			return infoMsg{err: snap.Err}
		}
		var agent string
		switch {
		case target == hub.HubAgent:
			ready, err := hub.EnsureHub(h, snap, hubDir, args)
			if err != nil {
				return infoMsg{err: err}
			}
			if !ready {
				return infoMsg{err: fmt.Errorf("o agent de pendências acabou de subir — tente de novo em alguns segundos (ou confirme a pasta com a na linha 🧭)")}
			}
			agent = hub.HubAgent
		case strings.HasPrefix(target, "ws:"):
			a, ok := snap.AgentIn(strings.TrimPrefix(target, "ws:"))
			if !ok {
				return infoMsg{err: fmt.Errorf("nenhum agent rodando nesse workspace")}
			}
			agent = a.Name
		case isSession:
			name, err := hub.EnsureAgent(h, snap, sess, args)
			if err != nil {
				return infoMsg{err: err}
			}
			agent = name
		default:
			return infoMsg{err: fmt.Errorf("workspace não encontrado")}
		}
		if err := h.Prompt(agent, text); err != nil {
			return infoMsg{err: err}
		}
		return infoMsg{info: okStyle.Render("mensagem enviada para " + agent)}
	}
}

func (m Model) rowAgent(r wsRow) (herdr.Agent, bool) {
	snap := m.sui.snap
	switch r.kind {
	case rowHub:
		return snap.Agent(hub.HubAgent)
	case rowOther:
		return snap.AgentIn(r.other.ID)
	}
	if r.sess.Agent != "" {
		if a, ok := snap.Agent(r.sess.Agent); ok {
			return a, true
		}
	}
	if r.sess.Workspace != "" {
		return snap.AgentIn(r.sess.Workspace)
	}
	return herdr.Agent{}, false
}

func (m Model) rowWorkspace(r wsRow) string {
	switch r.kind {
	case rowHub:
		if w, ok := m.sui.snap.WorkspaceByLabel(hub.HubLabel); ok {
			return w.ID
		}
		return ""
	case rowOther:
		return r.other.ID
	}
	return r.sess.Workspace
}

func (m Model) rowTarget(r wsRow) string {
	switch r.kind {
	case rowHub:
		return hub.HubAgent
	case rowOther:
		return "ws:" + r.other.ID
	}
	return r.sess.ID
}

func (m Model) focusCmd(workspaceID, name string) tea.Cmd {
	h := m.hr
	return func() tea.Msg {
		if err := h.FocusWorkspace(workspaceID); err != nil {
			return infoMsg{err: err}
		}
		return infoMsg{info: dim.Render(name + " em foco no herdr")}
	}
}

func (m Model) interruptCmd(agent string) tea.Cmd {
	h := m.hr
	return func() tea.Msg {
		if err := h.SendKeys(agent, "esc"); err != nil {
			return infoMsg{err: err}
		}
		return infoMsg{info: warnStyle.Render("esc enviado para " + agent)}
	}
}

func (m Model) askHub() (tea.Model, tea.Cmd) {
	h := m.hr
	args := m.claudeArgs()
	dir := m.hubDir()
	sessions := append([]session.Session{}, m.sess.Sessions...)
	ji, gl := m.fetch.ji, m.fetch.gl
	tasksCopy := append(m.store.Tasks[:0:0], m.store.Tasks...)
	m.sui.hubWaiting = true
	m.sui.info = dim.Render("montando o resumo de pendências…")
	m.mode = modeDetail
	m.detail = detailState{loading: true, title: "🧭 pendências", hub: true}
	m.vp.GotoTop()
	return m, func() tea.Msg {
		snap := hub.Poll(h)
		if !snap.Online() {
			return hubAskedMsg{err: fmt.Errorf("%s", friendlyErr(snap.Err))}
		}
		digest := hub.Digest(hub.DigestInput{
			Now:    time.Now(),
			Items:  hub.Collect(h, snap, sessions, ji, gl, screenLines),
			Others: hub.Others(snap, sessions),
			Jira:   ji,
			GitLab: gl,
			Tasks:  tasksCopy,
		})
		if err := hub.WriteDigest(dir, digest); err != nil {
			return hubAskedMsg{err: err}
		}
		at := time.Now()
		ready, err := hub.EnsureHub(h, snap, dir, args)
		if err != nil {
			return hubAskedMsg{err: err}
		}
		if ready {
			if err := h.Prompt(hub.HubAgent, claude.DigestKickoff()); err != nil {
				return hubAskedMsg{err: err}
			}
		}
		return hubAskedMsg{at: at, ready: ready}
	}
}

func (m Model) hubKickoffCmd() tea.Cmd {
	h := m.hr
	return func() tea.Msg {
		if err := h.Prompt(hub.HubAgent, claude.DigestKickoff()); err != nil {
			return hubAskedMsg{err: err}
		}
		return infoMsg{info: dim.Render("pedido enviado ao agent de pendências")}
	}
}

func (m Model) hubBody() string {
	if strings.TrimSpace(m.sui.hubReply) == "" {
		if m.sui.hubWaiting {
			return dim.Render("o agent está analisando as pendências… (a resposta aparece aqui sozinha; a na linha 🧭 acompanha no herdr)")
		}
		return dim.Render("nenhuma análise ainda — i pede uma")
	}
	head := dim.Render("análise de " + m.sui.hubAt.Format("02/01 15:04"))
	if m.sui.hubWaiting {
		head += warnStyle.Render(" · atualizando…")
	}
	return head + "\n\n" + renderMarkdown(m.sui.hubReply, max(40, m.vp.Width-2))
}

func (m Model) openRowDetail(r wsRow) (tea.Model, tea.Cmd) {
	m.mode = modeDetail
	m.vp.GotoTop()
	if r.kind == rowHub {
		m.detail = detailState{title: "🧭 pendências", hub: true}
		m.detail.body = m.hubBody()
		return m, nil
	}
	m.detail = detailState{loading: true}
	h := m.hr
	agent, hasAgent := m.rowAgent(r)
	wrap := m.wrapText
	if r.kind == rowOther {
		m.detail.title = "workspace " + r.other.Label
		return m, func() tea.Msg {
			body := section.Render(r.other.Label) + dim.Render(" · sem tarefa ligada (u adota)") + "\n\n"
			if hasAgent {
				if screen, err := h.Read(agent.Name, screenLines); err == nil {
					body += section.Render("🖥 Tela do Claude") + "\n" + screen
				}
			}
			return detailMsg{body: body}
		}
	}
	s := r.sess
	m.detail.title = s.Name()
	m.detail.url = s.URL
	links := m.linksText(s)
	state := m.stateLabel(r)
	return m, func() tea.Msg {
		var b strings.Builder
		b.WriteString(state + "  " + warnStyle.Render(s.Name()) + " " + s.Title + "\n")
		meta := []string{s.Service}
		if s.Branch != "" {
			meta = append(meta, s.Branch)
		}
		if s.IsReview() {
			meta = append(meta, "revisão de MR")
		}
		b.WriteString(dim.Render(strings.Join(meta, " · ")) + "\n")
		if links != "" {
			b.WriteString(links + "\n")
		}
		dir := s.Worktree
		if dir == "" {
			dir = s.Repo
		}
		b.WriteString(dim.Render(dir) + "\n\n")
		if s.Err != "" {
			b.WriteString(errStyle.Render(s.Err) + "\n\n")
		}
		if s.PendingPrompt != "" {
			b.WriteString(warnStyle.Render("⏳ pedido aguardando o Claude ficar livre") + "\n\n")
		}
		if info, err := worktree.Inspect(dir); err == nil {
			line := fmt.Sprintf("🌿 %s", info.Branch)
			if info.Base != "" {
				line += fmt.Sprintf(" · %d commit(s) desde %s", len(info.Commits), info.Base)
			}
			line += fmt.Sprintf(" · %d arquivo(s) não commitado(s)", len(info.Dirty))
			b.WriteString(section.Render(line) + "\n")
			for _, c := range info.Commits {
				b.WriteString("  " + c + "\n")
			}
			b.WriteString("\n")
		}
		if n := strings.TrimSpace(s.UserNote); n != "" {
			b.WriteString(section.Render("📝 Sua explicação") + "\n" + wrap(n) + "\n\n")
		}
		if hasAgent {
			if screen, err := h.Read(agent.Name, screenLines); err == nil {
				b.WriteString(section.Render("🖥 Tela do Claude ("+agent.Name+")") + "\n" + screen + "\n")
			}
		}
		return detailMsg{body: b.String()}
	}
}

func (m Model) openDiff(s session.Session) (tea.Model, tea.Cmd) {
	m.mode = modeDetail
	m.detail = detailState{loading: true, title: "diff " + s.Name(), url: s.URL}
	m.vp.GotoTop()
	dir := s.Worktree
	if dir == "" {
		dir = s.Repo
	}
	return m, func() tea.Msg {
		d, err := worktree.Diff(dir)
		if err != nil {
			return detailMsg{err: err}
		}
		if strings.TrimSpace(d) == "" {
			d = dim.Render("(sem mudanças)")
		}
		return detailMsg{body: d}
	}
}

func (m Model) finishCmd(s session.Session, force, remove bool) tea.Cmd {
	h := m.hr
	return func() tea.Msg {
		if s.Active() && s.OwnsWorktree() && !force {
			if dirty, err := worktree.HasChanges(s.Worktree); err == nil && dirty {
				return sessActionMsg{id: s.ID, err: fmt.Errorf("%s tem mudanças não commitadas — use %s para descartar", s.Worktree, map[bool]string{true: "D", false: "F"}[remove])}
			}
		}
		if s.Active() && s.Workspace != "" {
			if err := h.CloseWorkspace(s.Workspace); err != nil && !herdr.IsCode(err, herdr.CodeServerNotRunning) {
				return sessActionMsg{id: s.ID, err: err}
			}
		}
		if s.Active() && s.OwnsWorktree() {
			if _, err := os.Stat(s.Worktree); err == nil {
				if err := worktree.Remove(s.Repo, s.Worktree, force); err != nil {
					return sessActionMsg{id: s.ID, err: err}
				}
			}
		}
		if remove {
			return sessActionMsg{id: s.ID, remove: true, info: dim.Render(s.Name() + " removido")}
		}
		return sessActionMsg{id: s.ID, status: session.StatusCompleted, info: okStyle.Render(s.Name() + " concluído — a branch continua no repositório")}
	}
}

func (m Model) sessionKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.wsRows()
	r := m.selectedRow()
	switch msg.String() {
	case "j", "down":
		if m.cursor < len(rows)-1 {
			m.cursor++
		}
		m.scrollToSession()
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		m.scrollToSession()
		return m, nil
	case "n":
		return m.startFreeWorkspace()
	case "i":
		return m.askHub()
	}
	if r == nil {
		return m, nil
	}
	active := r.kind != rowSession || r.sess.Active()
	switch msg.String() {
	case "enter":
		return m.openRowDetail(*r)
	case "a":
		if ws := m.rowWorkspace(*r); ws != "" {
			return m, m.focusCmd(ws, m.rowName(*r))
		}
		switch {
		case r.kind == rowHub:
			m.sui.info = dim.Render("o workspace de pendências nasce no primeiro i")
		case active:
			m.sui.info = warnStyle.Render("workspace fechado — w abre de novo")
		}
	case "p":
		if !active {
			return m, nil
		}
		m.sui.target = m.rowTarget(*r)
		return m.describe()
	case "x":
		if a, ok := m.rowAgent(*r); ok {
			return m, m.interruptCmd(a.Name)
		}
	case "w":
		if r.kind == rowSession && r.sess.Active() && r.sess.Workspace == "" {
			m.sui.info = dim.Render("abrindo " + r.sess.Name() + "…")
			return m, m.reopenCmd(r.sess)
		}
	case "u":
		if r.kind == rowOther {
			return m, m.adoptCmd(r.other)
		}
	case "L":
		if r.kind == rowSession && r.sess.Active() {
			return m.openLinkPicker(r.sess)
		}
	case "o":
		if r.kind == rowSession {
			if u := m.sessionURL(r.sess); u != "" {
				return m, openURLCmd(u)
			}
		}
	case "v":
		if r.kind == rowSession && r.sess.Active() {
			return m.openDiff(r.sess)
		}
	case "f", "F":
		if r.kind == rowSession && r.sess.Active() {
			return m, m.finishCmd(r.sess, msg.String() == "F", false)
		}
	case "d", "D":
		if r.kind == rowSession {
			return m, m.finishCmd(r.sess, msg.String() == "D", true)
		}
	}
	return m, nil
}

func (m Model) sessionURL(s session.Session) string {
	if s.URL != "" {
		return s.URL
	}
	if ref := s.MRRef(); ref != "" {
		for _, mr := range m.allMRs() {
			if mr.ShortRef() == ref {
				return mr.WebURL
			}
		}
	}
	if k := s.IssueKey(); k != "" {
		return m.jiraURL(k)
	}
	return ""
}

func (m Model) rowName(r wsRow) string {
	switch r.kind {
	case rowHub:
		return "pendências"
	case rowOther:
		return r.other.Label
	}
	return r.sess.Name()
}

func agentStateLabel(status string) string {
	switch status {
	case herdr.StatusWorking:
		return okStyle.Render("▶ trabalhando")
	case herdr.StatusBlocked:
		return alertStyle.Render(" ⏸ precisa de você ")
	case herdr.StatusDone:
		return okStyle.Render("✔ terminou")
	case herdr.StatusIdle:
		return dim.Render("○ parado")
	}
	return dim.Render("? desconhecido")
}

func (m Model) stateLabel(r wsRow) string {
	if r.kind == rowSession && !r.sess.Active() {
		return dim.Render("✓ concluído")
	}
	if !m.sui.snap.Online() {
		return dim.Render("· herdr offline")
	}
	if a, ok := m.rowAgent(r); ok {
		return agentStateLabel(a.Status)
	}
	if m.rowWorkspace(r) == "" {
		if r.kind == rowHub {
			return dim.Render("· ainda não criado")
		}
		return warnStyle.Render("∅ workspace fechado")
	}
	return dim.Render("· sem Claude")
}

func (m Model) linksText(s session.Session) string {
	var parts []string
	if k := s.IssueKey(); k != "" {
		status := ""
		if m.fetch.ji != nil {
			for _, is := range m.fetch.ji.Open {
				if is.Key == k {
					status = " " + is.Status
				}
			}
		}
		parts = append(parts, "🔗 "+k+status)
	}
	if ref := s.MRRef(); ref != "" {
		mr := ref
		if m.fetch.gl != nil {
			for _, x := range m.fetch.gl.ReviewPending {
				if x.ShortRef() == ref {
					mr += " (aguarda sua revisão)"
				}
			}
		}
		parts = append(parts, "🔀 "+mr)
	}
	if len(parts) == 0 {
		return ""
	}
	return dim.Render(strings.Join(parts, " · "))
}

func (m Model) viewSessoes() (string, int) {
	var b strings.Builder
	sel, line := 0, 0
	write := func(s string) {
		b.WriteString(s)
		line += strings.Count(s, "\n")
	}
	snap := m.sui.snap
	switch {
	case snap.Err != nil:
		write(errStyle.Render("herdr: "+friendlyErr(snap.Err)) + "\n\n")
	case snap.At.IsZero():
		write(dim.Render("conectando ao herdr…") + "\n\n")
	}
	if m.sui.info != "" {
		write(m.sui.info + "\n\n")
	}
	rows := m.wsRows()
	section2 := ""
	for i, r := range rows {
		head := ""
		switch {
		case r.kind == rowOther:
			head = "Outros workspaces do herdr"
		case r.kind == rowSession && !r.sess.Active():
			head = "Concluídos"
		case r.kind == rowSession:
			head = "Tarefas"
		}
		if head != "" && head != section2 {
			write("\n" + section.Render(head) + "\n")
			section2 = head
		}
		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("▌ ")
			sel = line
		}
		state := m.stateLabel(r)
		switch r.kind {
		case rowHub:
			write(cursor + "🧭 " + section.Render("Pendências") + "  " + state + "\n")
			hint := "i analisa tudo e diz o que destravar e o que fazer a seguir"
			switch {
			case m.sui.hubWaiting:
				hint = m.spin.View() + " analisando…"
			case m.sui.hubReply != "":
				hint = "última análise " + m.sui.hubAt.Format("02/01 15:04") + " · enter lê · i atualiza"
			}
			write("    " + dim.Render(hint) + "\n")
		case rowOther:
			write(cursor + state + "  " + warnStyle.Render(r.other.Label) + dim.Render("  (u adota como tarefa)") + "\n")
		case rowSession:
			s := r.sess
			title := s.Title
			if title == s.Name() {
				title = ""
			}
			write(cursor + state + "  " + warnStyle.Render(s.Name()) + " " + title + "\n")
			meta := []string{s.Service}
			if s.Branch != "" && s.Kind != session.KindFree {
				meta = append(meta, s.Branch)
			}
			if s.IsReview() {
				meta = append(meta, "revisão")
			}
			if s.Finished != nil && !s.Active() {
				meta = append(meta, "concluído "+s.Finished.Format("02/01 15:04"))
			}
			lineMeta := dim.Render("    " + strings.Join(meta, " · "))
			if links := m.linksText(s); links != "" {
				lineMeta += dim.Render(" · ") + links
			}
			write(lineMeta + "\n")
			if s.PendingPrompt != "" && s.Active() {
				write("    " + warnStyle.Render("⏳ tarefa esperando o Claude ficar livre (confirme a pasta com a)") + "\n")
			}
			if s.Err != "" && s.Active() {
				write("    " + errStyle.Render(truncate(s.Err, 160)) + "\n")
			}
		}
	}
	return b.String(), sel
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
