package ui

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/repocfg"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

const (
	wsPollEvery   = 3 * time.Second
	wsPollEvents  = 20 * time.Second
	wsPollOffline = 10 * time.Second
	deliverAfter  = 2500 * time.Millisecond
	screenLines   = 60
)

type wsTickMsg struct{}

type wsSnapMsg struct {
	snap hub.Snapshot
	tick bool
}

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
	pickLink pickPurpose = iota
)

type pickState struct {
	purpose   pickPurpose
	title     string
	labels    []string
	plain     []string
	values    []string
	cursor    int
	target    string
	query     string
	filtering bool
}

type pendingSession struct {
	sess         session.Session
	createBranch bool
	guess        string
	services     []string
	model        string
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

func (m Model) pollHerdr() tea.Cmd { return m.poll(false) }

func (m Model) poll(tick bool) tea.Cmd {
	h := m.hr
	return func() tea.Msg { return wsSnapMsg{snap: hub.Poll(h), tick: tick} }
}

func (m Model) hubDir() string { return hub.HubDir(m.sess.Dir()) }

func (m Model) claudeArgs() []string {
	return hub.ClaudeArgs(m.cfg.Claude.Model, m.cfg.Claude.PermissionMode)
}

func (m Model) updateWorkspaces(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case wsTickMsg:
		return m, m.poll(true), true

	case wsSnapMsg:
		cmds := m.applySnapshot(msg.snap)
		if msg.tick {
			cmds = append(cmds, wsTick(m.pollInterval(msg.snap)))
		}
		cmds = append(cmds, m.publishMeta(), m.refreshUsage())
		return m, tea.Batch(cmds...), true

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
		m.sui.hubKicking = false
		if msg.err != nil {
			m.sui.hubWaiting, m.sui.hubPending = false, false
			m.sui.info = errStyle.Render("pendências: " + msg.err.Error())
			if m.mode == modeDetail && m.detail.hub {
				m.detail.loading = false
				m.detail.body = errStyle.Render(msg.err.Error())
			}
			return m, nil, true
		}
		m.sui.hubAsked = msg.at
		m.sui.hubPending = !msg.ready
		m.saveHubRequest()
		if m.mode == modeDetail && m.detail.hub {
			m.detail.loading = false
			m.detail.body = m.hubBody()
		}
		return m, m.pollHerdr(), true

	case hubKickedMsg:
		m.sui.hubKicking = false
		if msg.at.Equal(m.sui.hubAsked) {
			m.sui.hubPending = false
			m.saveHubRequest()
		}
		m.sui.info = dim.Render("pedido enviado ao agent de pendências")
		return m, m.pollHerdr(), true

	case trustProbeMsg:
		next, cmd := m.updateTrust(msg)
		return next, cmd, true

	case shippedMsg:
		next, cmd := m.updateShip(msg)
		return next, cmd, true

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
		if cmd := m.probeBlocked(a, old, seen); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if !prevOnline || !seen || old == a.Status {
			continue
		}
		label := m.agentOwner(a)
		if label == "" {
			continue
		}
		m.live.usageAt = time.Time{}
		switch {
		case a.Status == herdr.StatusBlocked:
			cmds = append(cmds, m.notify("wmonit — "+label, "precisa de você (permissão ou pergunta)", "request"))
		case old == herdr.StatusWorking && a.Status == herdr.StatusDone:
			cmds = append(cmds, m.notify("wmonit — "+label, "o Claude terminou", "done"))
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

	if m.sui.hubPending && !m.sui.hubKicking {
		if stable(hub.HubAgent) {
			m.sui.hubKicking = true
			cmds = append(cmds, m.hubKickoffCmd())
		}
	}
	if cmd := m.checkHubReply(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if m.mode == modeDetail && m.detail.hub && !m.detail.loading {
		m.detail.body = m.hubBody()
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
	return m.openCreateForm(p)
}

func (m Model) describe() (tea.Model, tea.Cmd) {
	m.mode = modeDescribing
	m.sui.descInput.Reset()
	m.sui.descInput.Focus()
	m.vp.GotoTop()
	return m, textarea.Blink
}

func (m Model) updateDescribe(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.sui.target = ""
		m.sui.descInput.Blur()
		m.sui.info = dim.Render("cancelado")
		return m, nil
	case "ctrl+d", "ctrl+s":
		return m.submitText()
	case "ctrl+e":
		return m, editNoteCmd(m.cfg, m.sui.descInput.Value())
	}
	var cmd tea.Cmd
	m.sui.descInput, cmd = m.sui.descInput.Update(msg)
	return m, cmd
}

func (m Model) submitText() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.sui.descInput.Value())
	m.mode = modeNormal
	m.sui.descInput.Blur()
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
	b.WriteString(section.Render("💬 Mensagem para "+m.targetName(m.sui.target)) + "\n\n")
	b.WriteString(dim.Render("O texto vai direto para o Claude do workspace, como se você tivesse digitado lá.") + "\n\n")
	b.WriteString(m.sui.descInput.View() + "\n\n")
	b.WriteString(dim.Render("ctrl+d envia · ctrl+e edita no editor · esc cancela"))
	return b.String()
}

func (pk pickState) visible() []int {
	if pk.query == "" {
		idx := make([]int, len(pk.values))
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	var idx []int
	for _, mt := range fuzzy.Find(pk.query, pk.plain) {
		idx = append(idx, mt.Index)
	}
	return idx
}

func (m Model) updatePick(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pk := &m.sui.pick
	if pk.filtering {
		switch msg.String() {
		case "esc":
			pk.filtering, pk.query, pk.cursor = false, "", 0
		case "enter":
			pk.filtering = false
		case "backspace":
			if r := []rune(pk.query); len(r) > 0 {
				pk.query = string(r[:len(r)-1])
			}
			pk.cursor = 0
		case "down", "ctrl+j", "ctrl+n":
			return m.movePick(1)
		case "up", "ctrl+k", "ctrl+p":
			return m.movePick(-1)
		default:
			if msg.Text != "" {
				pk.query += msg.Text
				pk.cursor = 0
			}
		}
		m.scrollToPick()
		return m, nil
	}
	n := len(pk.visible())
	switch msg.String() {
	case "esc", "q":
		m.mode = modeNormal
		return m, nil
	case "/":
		pk.filtering = true
		return m, nil
	case "j", "down":
		return m.movePick(1)
	case "k", "up":
		return m.movePick(-1)
	case "pgdown", "ctrl+d":
		return m.movePick(max(1, m.vp.Height()-pickHeader))
	case "pgup", "ctrl+u":
		return m.movePick(-max(1, m.vp.Height()-pickHeader))
	case "home", "g":
		return m.movePick(-n)
	case "end", "G":
		return m.movePick(n)
	case "enter":
		vis := pk.visible()
		if len(vis) == 0 {
			m.mode = modeNormal
			return m, nil
		}
		value := pk.values[vis[min(pk.cursor, len(vis)-1)]]
		m.mode = modeNormal
		if pk.purpose == pickLink {
			return m.applyLink(pk.target, value)
		}
	}
	return m, nil
}

const pickHeader = 2

func (m Model) movePick(delta int) (tea.Model, tea.Cmd) {
	pk := &m.sui.pick
	pk.cursor = min(max(pk.cursor+delta, 0), max(len(pk.visible())-1, 0))
	m.scrollToPick()
	return m, nil
}

func (m *Model) scrollToPick() {
	line := pickHeader + m.sui.pick.cursor
	if line == pickHeader {
		line = 0
	}
	m.scrollTo(m.viewPick(), line)
}

func (m Model) viewPick() string {
	var b strings.Builder
	pk := m.sui.pick
	head := section.Render(pk.title)
	switch {
	case pk.filtering:
		head += "  " + okStyle.Render("/"+pk.query+"▏")
	case pk.query != "":
		head += "  " + dim.Render("filtro: "+pk.query)
	}
	b.WriteString(head + "\n\n")
	vis := pk.visible()
	if len(vis) == 0 {
		b.WriteString(dim.Render("(nada para escolher — atualize com r ou mude o filtro)") + "\n")
	}
	for i, idx := range vis {
		cursor := "  "
		if i == pk.cursor {
			cursor = cursorStyle.Render("▌ ")
		}
		b.WriteString(cursor + pk.labels[idx] + "\n")
	}
	return b.String()
}

func (m Model) launchCreate(p pendingSession, send bool) (tea.Model, tea.Cmd) {
	sess := p.sess
	sess.Repo = filepath.Join(m.cfg.Claude.SourcesDir, sess.Service)
	sess.Status = session.StatusActive
	sess.Created = time.Now()
	sess.Model = p.model
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
		return inHerdr || hub.ReservedLabel(l) || m.sess.LabelTaken(l)
	})

	h, gl, ji := m.hr, m.glSrc, m.jiSrc
	mover, _ := m.jiSrc.(hub.IssueMover)
	cfg := m.cfg
	mrs := m.allMRs()
	createBranch := p.createBranch
	m.sui.info = dim.Render("criando o workspace " + sess.Label + " em " + sess.Service + "…")
	return m, func() tea.Msg {
		snap := hub.Poll(h)
		if !snap.Online() {
			return sessSavedMsg{err: fmt.Errorf("%s", friendlyErr(snap.Err))}
		}
		rc, rcErr := repocfg.Load(sess.Repo)
		if sess.OwnsWorktree() {
			if err := worktree.Add(sess.Repo, sess.Worktree, sess.Branch, createBranch); err != nil {
				return sessSavedMsg{err: err}
			}
		}
		opts := openOpts(cfg, rc, sess)
		if sess.Kind == session.KindFree {
			if sess.UserNote != "" {
				opts.TaskText = claude.FreePrompt(sess.Service, sess.UserNote)
				if t := templateFor(cfg, rc, sess.Service); t != "" {
					opts.TaskText += "\n\nInstruções específicas deste serviço:\n" + t + "\n"
				}
				if send {
					opts.Kickoff = claude.Kickoff()
				}
			}
		} else {
			ctx := hub.BuildContext(gl, ji, templateFor(cfg, rc, sess.Service), sess, mrs)
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
		var notes []string
		if rcErr != nil {
			notes = append(notes, rcErr.Error())
		}
		if key := opened.IssueKey(); key != "" && opened.Kind == session.KindIssue && cfg.Jira.StartTransition != "" && mover != nil {
			if to, err := mover.TransitionTo(context.Background(), key, cfg.Jira.StartTransition); err != nil {
				notes = append(notes, err.Error())
			} else {
				notes = append(notes, key+" → "+to)
			}
		}
		info := okStyle.Render("workspace " + opened.Label + " criado — a abre no herdr")
		if opened.PendingPrompt != "" {
			info = warnStyle.Render("workspace " + opened.Label + " criado — confirme a pasta no Claude (a); a tarefa vai assim que ele ficar livre")
		}
		if len(notes) > 0 {
			info += dim.Render(" · " + strings.Join(notes, " · "))
		}
		return sessSavedMsg{sess: opened, info: info}
	}
}

func templateFor(cfg config.Config, rc repocfg.Config, service string) string {
	var parts []string
	for _, t := range []string{cfg.Claude.Template, cfg.Claude.Templates[service], rc.Template} {
		if t = strings.TrimSpace(t); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

func openOpts(cfg config.Config, rc repocfg.Config, s session.Session) hub.OpenOpts {
	model := s.Model
	if model == "" {
		model = rc.Model
	}
	if model == "" {
		model = cfg.Claude.Model
	}
	perm := rc.PermissionMode
	if perm == "" {
		perm = cfg.Claude.PermissionMode
	}
	editor := cfg.Herdr.Editor
	if rc.Editor != "" {
		editor = rc.Editor
	}
	o := hub.OpenOpts{ClaudeArgs: hub.ClaudeArgs(model, perm), Editor: editor, Setup: rc.Setup}
	for _, t := range rc.Tabs {
		o.Tabs = append(o.Tabs, hub.ExtraTab{Label: t.Label, Command: t.Command})
	}
	return o
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
	cfg := m.cfg
	return func() tea.Msg {
		rc, _ := repocfg.Load(s.Repo)
		opts := openOpts(cfg, rc, s)
		opts.Setup = nil
		opts.Kickoff = s.PendingPrompt
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
	var labels, plain, values []string
	if m.fetch.ji != nil {
		for _, is := range m.fetch.ji.Open {
			labels = append(labels, warnStyle.Render(is.Key)+" "+truncate(is.Summary, 70)+dim.Render(" · "+is.Status))
			plain = append(plain, is.Key+" "+is.Summary+" "+is.Status)
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
		plain = append(plain, ref+" "+mr.Title)
		values = append(values, "mr:"+ref)
	}
	m.sui.pick = pickState{purpose: pickLink, title: "🔗 Ligar " + s.Name() + " a uma issue ou MR", labels: labels, plain: plain, values: values, target: s.ID}
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
	cfg := m.cfg
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
			rc, _ := repocfg.Load(sess.Repo)
			name, started, err := hub.EnsureAgent(h, snap, &sess, openOpts(cfg, rc, sess).ClaudeArgs)
			if err != nil {
				return infoMsg{err: err}
			}
			if started {
				sess.Agent, sess.PendingPrompt = name, text
				return sessSavedMsg{sess: sess, info: warnStyle.Render("subindo um Claude novo em " + sess.Name() + " — a mensagem vai assim que ele estiver pronto")}
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
	statuses := maps.Clone(m.live.mrStatus)
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
			Dir:    dir,
			Now:    time.Now(),
			Items:  hub.Collect(h, snap, sessions, ji, gl, statuses, screenLines),
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
			if err := h.Prompt(hub.HubAgent, claude.DigestKickoff(dir)); err != nil {
				if !herdr.IsCode(err, herdr.CodePromptStalled) {
					return hubAskedMsg{err: err}
				}
				ready = false
			}
		}
		return hubAskedMsg{at: at, ready: ready}
	}
}

func (m Model) hubKickoffCmd() tea.Cmd {
	h := m.hr
	at := m.sui.hubAsked
	dir := m.hubDir()
	return func() tea.Msg {
		if err := h.Prompt(hub.HubAgent, claude.DigestKickoff(dir)); err != nil {
			if herdr.IsCode(err, herdr.CodePromptStalled) {
				return hubAskedMsg{at: at, ready: false}
			}
			return hubAskedMsg{err: err}
		}
		return hubKickedMsg{at: at}
	}
}

func (m Model) hubBody() string {
	if strings.TrimSpace(m.sui.hubReply) == "" {
		if m.sui.hubWaiting {
			return m.hubState()
		}
		return dim.Render("nenhuma análise ainda — i pede uma")
	}
	head := dim.Render("análise de " + m.sui.hubAt.Format("02/01 15:04"))
	if m.sui.hubWaiting {
		head += "  " + m.hubState()
	}
	return head + "\n\n" + renderMarkdown(m.sui.hubReply, max(40, m.vp.Width()-2))
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
	m.detail.title = r.sess.Name()
	m.detail.url = m.sessionURL(r.sess)
	return m, m.sessionDetailCmd(r)
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
	forceKey := "F"
	if remove {
		forceKey = "D"
	}
	return func() tea.Msg {
		if s.Active() && s.OwnsWorktree() && !force {
			if dirty, err := worktree.HasChanges(s.Worktree); err == nil && dirty {
				return sessActionMsg{id: s.ID, err: fmt.Errorf("%s tem mudanças não commitadas — use %s para descartar", s.Worktree, forceKey)}
			}
		}
		if s.Active() && s.Label != "" {
			if ws, ok := hub.Poll(h).WorkspaceByLabel(s.Label); ok {
				if err := h.CloseWorkspace(ws.ID); err != nil {
					return sessActionMsg{id: s.ID, err: err}
				}
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

func (m Model) sessionKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rows := m.wsRows()
	r := m.selectedRow()
	if msg.String() != "C" {
		m.sui.confirmClean = false
	}
	switch msg.String() {
	case "C":
		return m.cleanStale()
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
	case "t":
		if a, ok := m.rowAgent(*r); ok {
			if keys, asks := m.sui.trust[a.Name]; asks {
				delete(m.sui.trust, a.Name)
				return m, m.confirmTrustCmd(a.Name, keys)
			}
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
	case "s":
		if r.kind == rowSession && r.sess.Active() {
			m.sui.info = dim.Render("entregando " + r.sess.Name() + " (push, MR, Jira)…")
			return m, m.shipCmd(r.sess)
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
	if stale := m.staleItems(); len(stale) > 0 && !m.sui.confirmClean {
		write(warnStyle.Render(fmt.Sprintf("🧹 %d workspace(s) com MR mergeado ou issue resolvida — C conclui", len(stale))) + "\n\n")
	}
	rows := m.wsRows()
	lastHead := ""
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
		if head != "" && head != lastHead {
			write("\n" + section.Render(head) + "\n")
			lastHead = head
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
			if s.Active() {
				if extra := m.sessionExtra(s); extra != "" {
					write("    " + extra + "\n")
				}
			}
			if _, asks := m.sui.trust[s.Agent]; asks && s.Active() {
				write("    " + alertStyle.Render(" 🔐 o Claude pede para confiar na pasta ") + dim.Render("  t confirma daqui") + "\n")
			} else if s.PendingPrompt != "" && s.Active() {
				write("    " + warnStyle.Render("⏳ tarefa esperando o Claude ficar livre") + "\n")
			}
			if s.Err != "" && s.Active() {
				write("    " + errStyle.Render(truncate(s.Err, 160)) + "\n")
			}
		}
	}
	return b.String(), sel
}

func (m Model) sessionExtra(s session.Session) string {
	var parts []string
	_, ref := m.sessionMR(s)
	if st := m.live.mrStatus[ref]; st != nil {
		switch p := st.Problems(); {
		case len(p) > 0:
			style := warnStyle
			if st.CI() == "failed" || st.HasConflicts {
				style = errStyle
			}
			parts = append(parts, style.Render("🔀 "+strings.Join(p, " · ")))
		case st.Ready():
			parts = append(parts, okStyle.Render("🔀 pronto para merge"))
		}
	}
	if u, ok := m.live.usage[s.ID]; ok {
		parts = append(parts, dim.Render("💬 "+m.usageText(u.usage)))
	}
	return strings.Join(parts, dim.Render(" · "))
}

func (m Model) usageText(u claude.Usage) string {
	text := fmt.Sprintf("%d pedido(s) · %s tokens", u.Turns, claude.FormatTokens(u.Tokens()))
	if cost, ok := u.Cost(m.cfg.Claude.Prices); ok {
		text += fmt.Sprintf(" · US$ %.2f", cost)
	}
	if !u.LastAt.IsZero() {
		text += " · " + u.LastAt.Local().Format("02/01 15:04")
	}
	return text
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (m Model) windowTitle() string {
	blocked := 0
	for _, a := range m.sui.snap.Agents {
		if a.Status == herdr.StatusBlocked && m.agentOwner(a) != "" {
			blocked++
		}
	}
	if blocked > 0 {
		return fmt.Sprintf("wmonit · %d precisa(m) de você", blocked)
	}
	return "wmonit"
}
