package ui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/timmers/wmonit/internal/agents"
	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

// agentSource é o que a aba Agentes usa do Claude Code: o roster do agent
// view, o que as sessões deixam em disco e as ações sobre elas. A fonte real
// (agents.Local) e a do modo demo o satisfazem.
type agentSource interface {
	List() ([]agents.Live, error)
	Heartbeats() map[string]agents.Heartbeat
	Transcript(cwd, sessionID string) (agents.Transcript, error)
	Launch(o agents.LaunchOpts) (string, error)
	Resume(dir, sessionID string) (string, error)
	Stop(ref string) error
	Remove(ref string) error
	AttachCmd(ref string) *exec.Cmd
	InteractiveCmd(dir, sessionID string) *exec.Cmd
	Summarize(in agents.SummaryInput) (agents.Summary, error)
	Rename(sessionID, name string) error
}

// O roster vem de `claude agents --json` (~1s por chamada): rápido com a
// aba à vista, devagar fora dela só para avisar quando um agent termina.
const (
	agentsPollVisible = 5 * time.Second
	agentsPollHidden  = 30 * time.Second
)

type agentsTickMsg time.Time

// agentsMsg traz uma leitura do agent view e do que as sessões deixaram em
// disco. gen descarta leituras que uma mais nova já substituiu.
type agentsMsg struct {
	gen   int
	live  []agents.Live
	beats map[string]agents.Heartbeat
	trans map[string]agents.Transcript // por session id
	err   error
}

// agentLaunchedMsg chega quando o `claude --bg` de uma sessão voltou.
type agentLaunchedMsg struct {
	id      string // sessão do wmonit
	agentID string
	prompt  string
	err     error
}

// agentActionMsg é o resultado de parar/remover/concluir: remove tira a
// sessão da lista, status muda o estado dela.
type agentActionMsg struct {
	id     string
	remove bool
	status session.Status
	info   string
	err    error
}

// attachDoneMsg chega quando o attach/claude interativo fechou e a TUI voltou.
type attachDoneMsg struct{ err error }

// summaryMsg traz a leitura do haiku sobre um agent. key é a chave da linha
// (id da sessão do wmonit, ou session id do Claude para as de fora).
type summaryMsg struct {
	key     string
	summary agents.Summary
	renamed string // nome pedido à sessão via mod fleet; vazio = não pediu
	err     error
}

func (m Model) agentsTick() tea.Cmd {
	every := agentsPollHidden
	if m.tab == tabAgentes {
		every = agentsPollVisible
	}
	return tea.Tick(every, func(t time.Time) tea.Msg { return agentsTickMsg(t) })
}

// refreshAgents dispara uma leitura nova, descartando a que estiver em voo.
func (m *Model) refreshAgents() tea.Cmd {
	m.sui.agentsGen++
	return m.fetchAgents()
}

// fetchAgents lê o roster, os heartbeats e o fim dos transcripts das sessões
// vivas e das do wmonit que já têm conversa, marcada com a geração atual.
func (m Model) fetchAgents() tea.Cmd {
	gen, ag := m.sui.agentsGen, m.ag
	known := map[string]string{} // session id → pasta, para as que já saíram do roster
	for _, s := range m.sess.Sessions {
		if s.ClaudeID != "" && s.Active() {
			known[s.ClaudeID] = s.Worktree
		}
	}
	return func() tea.Msg {
		live, err := ag.List()
		trans := map[string]agents.Transcript{}
		for _, l := range live {
			if l.IsRunning() {
				known[l.SessionID] = l.Cwd
			}
		}
		for id, cwd := range known {
			if t, err := ag.Transcript(cwd, id); err == nil {
				trans[id] = t
			}
		}
		return agentsMsg{gen: gen, live: live, beats: ag.Heartbeats(), trans: trans, err: err}
	}
}

// applyAgents guarda a leitura nova, aprende o session id dos agents recém
// lançados e avisa quando um agent do wmonit para de trabalhar.
func (m *Model) applyAgents(msg agentsMsg) tea.Cmd {
	if msg.gen != m.sui.agentsGen {
		return nil
	}
	if msg.err != nil {
		m.sui.agentsErr = msg.err.Error()
		return nil // mantém a última leitura boa na tela
	}
	m.sui.agentsErr = ""
	m.sui.live, m.sui.beats, m.sui.trans = msg.live, msg.beats, msg.trans

	var cmds []tea.Cmd
	var pending []string // agents que acabaram de parar: o haiku relê o que falta
	learned := false
	for i := range m.sess.Sessions {
		s := &m.sess.Sessions[i]
		// O session id da conversa (para --resume) vem do roster, mesmo de
		// um agent que já encerrou.
		if s.ClaudeID == "" && s.AgentID != "" {
			for _, l := range m.sui.live {
				if l.ID == s.AgentID && l.SessionID != "" {
					s.ClaudeID, learned = l.SessionID, true
				}
			}
		}
		l := m.liveFor(*s)
		if l == nil {
			delete(m.sui.busy, s.ID)
			continue
		}
		was, seen := m.sui.busy[s.ID]
		now := l.IsBusy()
		m.sui.busy[s.ID] = now
		if seen && was && !now {
			body := "terminou — aguarda você"
			if t, ok := m.sui.trans[l.SessionID]; ok && cleanReply(t.Last()) != "" {
				body = truncate(cleanReply(t.Last()), 160)
			}
			cmds = append(cmds, notifyCmd("wmonit — agent "+s.Key, body))
			pending = append(pending, s.ID)
		}
	}
	if learned {
		m.save(m.sess.Save())
	}
	for _, r := range m.agentRows() {
		for _, id := range pending {
			if r.sess != nil && r.sess.ID == id {
				cmds = append(cmds, m.analyze(r))
			}
		}
	}
	return tea.Batch(cmds...)
}

// liveFor acha a sessão viva do agent view que pertence à sessão do wmonit;
// nil quando o agent não está rodando.
func (m Model) liveFor(s session.Session) *agents.Live {
	for i := range m.sui.live {
		l := &m.sui.live[i]
		if !l.IsRunning() {
			continue
		}
		if (s.AgentID != "" && l.ID == s.AgentID) || (s.ClaudeID != "" && l.SessionID == s.ClaudeID) {
			return l
		}
	}
	return nil
}

// agentRow é uma linha da aba: uma sessão do wmonit (com ou sem agent vivo)
// ou uma sessão do agent view que o wmonit não criou.
type agentRow struct {
	sess *session.Session // nil para as sessões de fora do wmonit
	live *agents.Live
	beat *agents.Heartbeat
	tr   *agents.Transcript
	ai   *agents.Summary // última leitura do haiku
}

func (r agentRow) sessionID() string {
	switch {
	case r.live != nil:
		return r.live.SessionID
	case r.sess != nil:
		return r.sess.ClaudeID
	}
	return ""
}

// key identifica a linha nos mapas de estado da UI.
func (r agentRow) key() string {
	if r.sess != nil {
		return r.sess.ID
	}
	return r.live.SessionID
}

func (r agentRow) label() string {
	if r.sess != nil {
		return r.sess.Key
	}
	if r.ai != nil && r.ai.Name != "" {
		return r.ai.Name
	}
	if r.live.Name != "" {
		return r.live.Name
	}
	return filepath.Base(r.live.Cwd)
}

// agentRows junta as sessões do wmonit (na ordem do store) e, depois, as do
// agent view que ele não criou.
func (m Model) agentRows() []agentRow {
	var rows []agentRow
	linked := map[string]bool{}
	attach := func(r agentRow) agentRow {
		if id := r.sessionID(); id != "" {
			if b, ok := m.sui.beats[id]; ok {
				r.beat = &b
			}
			if t, ok := m.sui.trans[id]; ok {
				r.tr = &t
			}
		}
		if a, ok := m.sui.summaries[r.key()]; ok {
			r.ai = &a
		} else if r.sess != nil && r.sess.AIName != "" {
			a := agents.Summary{Name: r.sess.AIName, Missing: r.sess.AIMissing}
			if r.sess.AIAt != nil {
				a.At = *r.sess.AIAt
			}
			r.ai = &a
		}
		return r
	}
	for i := range m.sess.Sessions {
		s := &m.sess.Sessions[i]
		r := agentRow{sess: s, live: m.liveFor(*s)}
		if r.live != nil {
			linked[r.live.SessionID] = true
		}
		rows = append(rows, attach(r))
	}
	for i := range m.sui.live {
		l := &m.sui.live[i]
		// Sessões de fora do wmonit só enquanto vivas: as de background já
		// encerradas (o --all as traz) ficam no agent view.
		if linked[l.SessionID] || l.SessionID == "" || !l.IsRunning() {
			continue
		}
		rows = append(rows, attach(agentRow{live: l}))
	}
	return rows
}

func (m Model) selectedAgent() *agentRow {
	rows := m.agentRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return nil
	}
	return &rows[m.cursor]
}

// liveLabel descreve o estado de uma linha juntando o que o wmonit sabe
// (lançou, parou, concluiu) com o que o agent view diz agora.
func liveLabel(r agentRow) string {
	if r.live != nil {
		switch {
		case r.live.IsBusy():
			return okStyle.Render("● trabalhando")
		case r.live.State == "" || r.live.State == "done":
			return warnStyle.Render("◆ aguarda você")
		default:
			return warnStyle.Render("◆ " + r.live.State)
		}
	}
	if r.sess == nil {
		return dim.Render("·")
	}
	switch r.sess.Status {
	case session.StatusPending:
		return warnStyle.Render("● pendente")
	case session.StatusRunning:
		return dim.Render("○ encerrado")
	case session.StatusFailed:
		return errStyle.Render("✖ falhou")
	case session.StatusCancelled:
		return dim.Render("■ parado")
	case session.StatusCompleted:
		return dim.Render("✓ concluída")
	case session.StatusWaiting, session.StatusDone:
		return dim.Render("○ pipeline antigo")
	}
	return string(r.sess.Status)
}

// usage resume contexto, custo e subagents da linha.
func usage(r agentRow) string {
	var parts []string
	switch {
	case r.beat != nil && r.beat.CtxPct != nil:
		parts = append(parts, fmt.Sprintf("ctx %d%%", *r.beat.CtxPct))
	case r.tr != nil && r.tr.ContextTokens > 0:
		parts = append(parts, fmt.Sprintf("ctx %dk", r.tr.ContextTokens/1000))
	}
	if r.beat != nil && r.beat.USD != nil {
		parts = append(parts, fmt.Sprintf("$%.2f", *r.beat.USD))
	}
	if r.beat != nil && r.beat.Agents > 0 {
		parts = append(parts, fmt.Sprintf("%d subagent(s)", r.beat.Agents))
	}
	return strings.Join(parts, " · ")
}

// collisions aponta agents vivos pisando uns nos outros (mesma tarefa em dois
// worktrees, mesmo worktree, mesmo arquivo).
func (m Model) collisions(rows []agentRow) []string {
	var ms []agents.Member
	for _, r := range rows {
		if r.live == nil {
			continue
		}
		mb := agents.Member{Name: r.label(), Root: r.live.Cwd}
		if r.beat != nil {
			mb.Ticket, mb.Repo, mb.Files = r.beat.Ticket, r.beat.Repo, r.beat.Files
			if r.beat.Root != "" {
				mb.Root = r.beat.Root
			}
		}
		if r.sess != nil {
			if mb.Repo == "" {
				mb.Repo = r.sess.Service
			}
			if mb.Ticket == "" && r.sess.IsIssue() {
				mb.Ticket = r.sess.Key
			}
		}
		ms = append(ms, mb)
	}
	return agents.Collisions(ms)
}

// viewAgentes desenha a aba. Devolve também a linha onde começa a linha
// selecionada — cada agent ocupa 2+ linhas, então o índice não serve para
// rolar.
func (m Model) viewAgentes() (string, int) {
	var b strings.Builder
	sel, line := 0, 0
	write := func(s string) {
		b.WriteString(s)
		line += strings.Count(s, "\n")
	}
	if m.sui.sessInfo != "" {
		write(m.sui.sessInfo + "\n\n")
	}
	if m.sui.agentsErr != "" {
		write(errStyle.Render("agent view: "+m.sui.agentsErr) + "\n\n")
	}
	rows := m.agentRows()
	if warn := m.collisions(rows); len(warn) > 0 {
		for _, w := range warn {
			write(warnStyle.Render("⚠ "+w) + "\n")
		}
		write("\n")
	}
	if len(rows) == 0 {
		write(dim.Render("nenhum agent — 'c' numa issue (Jira) ou MR (GitLab) cria um; 'n' cria um agent livre") + "\n")
		return b.String(), 0
	}
	mine := 0
	for _, r := range rows {
		if r.sess != nil {
			mine++
		}
	}
	if mine > 0 {
		write(section.Render(fmt.Sprintf("Criados pelo wmonit (%d)", mine)) + "\n")
	}
	headerOthers := false
	for i, r := range rows {
		if r.sess == nil && !headerOthers {
			headerOthers = true
			if mine > 0 {
				write("\n")
			}
			write(section.Render(fmt.Sprintf("Outras sessões do agent view (%d)", len(rows)-mine)) + "\n")
		}
		if i == m.cursor {
			sel = line
		}
		write(m.agentCard(r, i == m.cursor) + "\n")
	}
	return b.String(), sel
}

// agentKeys trata as teclas da aba Agentes.
func (m Model) agentKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.agentRows()
	// A confirmação de f/d vale só para a tecla seguinte — mover o cursor
	// também a cancela, senão o segundo f/d cairia noutra linha.
	confirm := m.sui.confirm
	m.sui.confirm = ""
	switch msg.String() {
	case "j", "down":
		if m.cursor < len(rows)-1 {
			m.cursor++
		}
		m.scrollToAgent()
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		m.scrollToAgent()
		return m, nil
	case "n":
		return m.startFreeAgent()
	}
	r := m.selectedAgent()
	if r == nil {
		return m, nil
	}
	switch msg.String() {
	case "enter", "p":
		return m.openAgentDetail(*r)
	case "a", "t":
		return m.attach(*r, true)
	case "A", "T":
		return m.attach(*r, false)
	case "i":
		if m.sui.analyzing[r.key()] {
			return m, nil
		}
		m.sui.sessInfo = dim.Render("🧠 haiku analisando " + r.label() + "…")
		return m, m.analyze(*r)
	case "I":
		var cmds []tea.Cmd
		for _, x := range rows {
			if (x.live != nil || (x.sess != nil && x.sess.Active())) && !m.sui.analyzing[x.key()] {
				cmds = append(cmds, m.analyze(x))
			}
		}
		m.sui.sessInfo = dim.Render(fmt.Sprintf("🧠 haiku analisando %d agents…", len(cmds)))
		return m, tea.Batch(cmds...)
	case "o":
		if u := m.rowURL(*r); u != "" {
			return m, openURLCmd(u)
		}
		m.sui.sessInfo = dim.Render(r.label() + " não tem issue/MR ligada")
		return m, nil
	case "s":
		if r.sess == nil {
			m.sui.sessInfo = dim.Render("s só vale para agents criados pelo wmonit — use a para abrir a conversa")
			return m, nil
		}
		return m.startAgent(*r)
	case "x":
		return m.stopAgent(*r)
	case "v":
		dir := rowDir(*r)
		if dir == "" {
			m.sui.sessInfo = dim.Render(r.label() + " não tem pasta para comparar")
			return m, nil
		}
		url := ""
		if r.sess != nil {
			url = r.sess.URL
		}
		return m.openDiff(r.label(), url, dir)
	case "e":
		if dir := rowDir(*r); dir != "" {
			return m, openWorkspaceCmd(m.cfg, dir)
		}
	case "f", "F", "d", "D":
		key := msg.String()
		if r.live != nil && r.live.IsBusy() {
			m.sui.sessInfo = warnStyle.Render(r.label() + " está trabalhando — pare com x antes")
			return m, nil
		}
		if r.sess == nil {
			return m.removeOutside(*r, key, confirm)
		}
		if key == "f" || key == "F" {
			if !r.sess.Active() {
				m.sui.sessInfo = dim.Render(r.sess.Key + " já está concluída — d tira da lista")
				return m, nil
			}
			return m, m.finishAgentCmd(*r, key == "F")
		}
		return m, m.removeAgentCmd(*r, key == "D")
	}
	return m, nil
}

// rowURL é o link da linha: a issue/MR da sessão do wmonit ou, numa sessão
// de fora com ticket no heartbeat, a issue no Jira.
func (m Model) rowURL(r agentRow) string {
	if r.sess != nil {
		return r.sess.URL
	}
	if r.beat != nil && r.beat.Ticket != "" && m.cfg.Jira.URL != "" {
		return strings.TrimRight(m.cfg.Jira.URL, "/") + "/browse/" + r.beat.Ticket
	}
	return ""
}

// removeOutside trata f/d numa sessão que o wmonit não criou: tira-a do agent
// view (`claude rm`, que também apaga o worktree que o próprio Claude criou,
// se for seguro). Como a sessão não é do wmonit, pede a tecla duas vezes.
func (m Model) removeOutside(r agentRow, key, confirm string) (tea.Model, tea.Cmd) {
	if r.live == nil || !r.live.IsBackground() {
		m.sui.sessInfo = dim.Render(r.label() + " é interativa — feche no terminal dela")
		return m, nil
	}
	want := key + ":" + r.key()
	if confirm != want {
		m.sui.confirm = want
		m.sui.sessInfo = warnStyle.Render(key + " de novo para remover " + r.label() + " do agent view (para a sessão e apaga; o transcript fica em disco)")
		return m, nil
	}
	ag, ref, label := m.ag, r.live.Ref(), r.label()
	m.sui.sessInfo = dim.Render("removendo " + label + "…")
	return m, func() tea.Msg {
		_ = ag.Stop(ref) // já pode estar parada
		if err := ag.Remove(ref); err != nil {
			return agentActionMsg{err: err}
		}
		return agentActionMsg{info: label + " removida do agent view"}
	}
}

func rowDir(r agentRow) string {
	switch {
	case r.sess != nil && r.sess.Active():
		return r.sess.Worktree
	case r.live != nil:
		return r.live.Cwd
	}
	return ""
}

// scrollToAgent mantém visível a linha selecionada.
func (m *Model) scrollToAgent() {
	content, sel := m.viewAgentes()
	m.scrollTo(content, sel)
}

// attach abre a conversa do agent: `claude attach` para os de background
// vivos; para uma sessão do wmonit sem agent vivo, o Claude interativo no
// worktree retomando a conversa. external abre numa aba nova do terminal (o
// wmonit segue na tela); sem terminal configurado, ou com external false,
// suspende a TUI e abre aqui.
func (m Model) attach(r agentRow, external bool) (tea.Model, tea.Cmd) {
	var cmd *exec.Cmd
	switch {
	case r.live != nil && r.live.IsBackground():
		cmd = m.ag.AttachCmd(r.live.Ref())
	case r.live != nil:
		m.sui.sessInfo = dim.Render("sessão interativa — está aberta em outro terminal")
		return m, nil
	case r.sess != nil && r.sess.Active() && r.sess.Worktree != "":
		cmd = m.ag.InteractiveCmd(r.sess.Worktree, r.sess.ClaudeID)
	}
	if cmd == nil {
		m.sui.sessInfo = dim.Render("nada para abrir")
		return m, nil
	}
	if r.live != nil && cmd.Dir == "" {
		cmd.Dir = r.live.Cwd
	}
	if ext := externalTerminal(m.cfg, r.label(), cmd); external && ext != nil {
		if err := ext.Start(); err == nil {
			go func() { _ = ext.Wait() }()
			m.sui.sessInfo = dim.Render("conversa de " + r.label() + " aberta numa aba nova do terminal")
			return m, nil
		}
		// Sem o terminal externo, abre aqui mesmo.
	}
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return attachDoneMsg{err: err} })
}

// analyze pede ao haiku uma leitura por cima do agent (nome e o que falta) e
// propaga o nome para a sessão, via mod fleet.
func (m Model) analyze(r agentRow) tea.Cmd {
	key := r.key()
	m.sui.analyzing[key] = true // mapa compartilhado entre as cópias do Model
	ag := m.ag
	in := agents.SummaryInput{Dir: rowDir(r), Running: r.live != nil && r.live.IsBusy()}
	prefix := ""
	if r.sess != nil {
		in.Task = r.sess.Key + " " + r.sess.Title
		if n := strings.TrimSpace(r.sess.UserNote); n != "" {
			in.Task += "\n" + n
		}
		prefix = r.sess.Key
	} else {
		in.Task = r.live.Name + " (" + r.live.Cwd + ")"
		if r.beat != nil && r.beat.Ticket != "" {
			in.Task = r.beat.Ticket + " " + in.Task
			prefix = r.beat.Ticket
		}
	}
	if r.tr != nil {
		in.Recent = r.tr.Recent
		if len(in.Recent) > 3 {
			in.Recent = in.Recent[len(in.Recent)-3:]
		}
	}
	sessionID := r.sessionID()
	isLive := r.live != nil
	return func() tea.Msg {
		sum, err := ag.Summarize(in)
		msg := summaryMsg{key: key, summary: sum, err: err}
		if err == nil && isLive && sessionID != "" {
			name := sum.Name
			if prefix != "" {
				name = prefix + " · " + name
			}
			if ag.Rename(sessionID, name) == nil {
				msg.renamed = name
			}
		}
		return msg
	}
}

// applySummary guarda a leitura do haiku (no store, para as do wmonit).
func (m *Model) applySummary(msg summaryMsg) {
	delete(m.sui.analyzing, msg.key)
	if msg.err != nil {
		m.sui.sessInfo = errStyle.Render(msg.err.Error())
		return
	}
	m.sui.summaries[msg.key] = msg.summary
	if m.sess.Update(msg.key, func(s *session.Session) {
		at := msg.summary.At
		s.AIName, s.AIMissing, s.AIAt = msg.summary.Name, msg.summary.Missing, &at
	}) {
		m.save(m.sess.Save())
	}
	if msg.renamed != "" {
		m.sui.sessInfo = dim.Render("🧠 " + msg.renamed + " — renomeado no agent view")
	}
}

// statusWidth é a coluna do estado: o título começa sempre no mesmo ponto.
const statusWidth = 16

// agentCard desenha um agent em bloco: estado e título com o uso alinhado à
// direita, a linha de onde/quando, o que falta (haiku) e a última resposta.
// O selecionado leva a barra do cursor em todas as linhas.
func (m Model) agentCard(r agentRow, selected bool) string {
	width := max(60, m.vp.Width-2)
	bar := "  "
	if selected {
		bar = cursorStyle.Render("▌ ")
	}
	indent := strings.Repeat(" ", statusWidth)
	var lines []string

	status := liveLabel(r)
	status += strings.Repeat(" ", max(1, statusWidth-lipgloss.Width(status)))
	var title, meta string
	if r.sess != nil {
		s := r.sess
		title = warnStyle.Render(s.Key) + " " + lipgloss.NewStyle().Bold(true).Render(s.Title)
		if s.IsReview() {
			title += dim.Render("  revisão")
		}
		parts := []string{s.Service, s.Branch, s.Created.Format("02/01 15:04")}
		if s.Kind == session.KindFree {
			parts[1] = "livre"
		}
		meta = strings.Join(nonEmpty(parts), " · ")
	} else {
		title = lipgloss.NewStyle().Bold(true).Render(r.label())
		kind := "interativa"
		if r.live.IsBackground() {
			kind = "background"
		}
		parts := []string{kind, r.live.Cwd}
		if r.beat != nil {
			if r.beat.Ticket != "" {
				parts = append(parts, r.beat.Ticket)
			} else if r.beat.Branch != "" {
				parts = append(parts, r.beat.Branch)
			}
		}
		meta = strings.Join(nonEmpty(parts), " · ")
	}
	head := status + title
	if u := usage(r); u != "" {
		gap := width - lipgloss.Width(head) - lipgloss.Width(u)
		if gap >= 2 {
			head += strings.Repeat(" ", gap) + dim.Render(u)
		} else {
			meta += " · " + u // estreito: o uso desce para a linha de baixo
		}
	}
	lines = append(lines, head, indent+dim.Render(truncate(meta, width-statusWidth)))

	if r.sess != nil && r.sess.Err != "" {
		lines = append(lines, indent+errStyle.Render(truncate(r.sess.Err, width-statusWidth)))
	}
	switch {
	case m.sui.analyzing[r.key()]:
		lines = append(lines, indent+dim.Render(m.spin.View()+" haiku analisando…"))
	case r.ai != nil && r.ai.Missing != "":
		missing := r.ai.Missing
		label := warnStyle.Render("falta ")
		if strings.EqualFold(strings.TrimSpace(missing), "nada") {
			missing, label = "nada — pronto para revisar", okStyle.Render("falta ")
		}
		at := ""
		if !r.ai.At.IsZero() {
			at = dim.Render("  haiku " + r.ai.At.Format("15:04"))
		}
		lines = append(lines, indent+label+truncate(missing, width-statusWidth-20)+at)
	}
	if r.tr != nil && (r.live != nil || (r.sess != nil && r.sess.Active())) {
		if last := cleanReply(r.tr.Last()); last != "" {
			lines = append(lines, indent+dim.Render("“"+truncate(last, width-statusWidth-2)+"”"))
		}
	}

	var b strings.Builder
	for _, l := range lines {
		b.WriteString(bar + l + "\n")
	}
	return b.String()
}

// cleanReply tira da resposta do agent o que não diz nada no resumo: o bloco
// de feedback do inglês que o CLAUDE.md põe no topo (citação "> …"), cercas
// de código e marcação markdown. Fica com as duas primeiras linhas úteis.
func cleanReply(s string) string {
	var keep []string
	inFence := false
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || t == "" || strings.HasPrefix(t, ">") || t == "---" || strings.HasPrefix(t, "|") {
			continue
		}
		t = strings.TrimLeft(t, "#*-• ")
		t = strings.NewReplacer("**", "", "`", "", "__", "").Replace(t)
		if t != "" {
			keep = append(keep, t)
		}
		if len(keep) == 2 {
			break
		}
	}
	return strings.Join(keep, " ")
}

// startAgent é a tecla 's': lança o agent de uma sessão que não tem um vivo.
// Com conversa anterior retoma-a em background; sem, lança com o prompt da
// tarefa.
func (m Model) startAgent(r agentRow) (tea.Model, tea.Cmd) {
	s := *r.sess
	switch {
	case r.live != nil:
		m.sui.sessInfo = dim.Render("agent já está no agent view — a abre a conversa")
		return m, nil
	case !s.Active() && s.Status != session.StatusCancelled:
		m.sui.sessInfo = dim.Render("sessão concluída — crie outra com 'c'")
		return m, nil
	}
	m.sess.Update(s.ID, func(x *session.Session) { x.Status, x.Err = session.StatusPending, "" })
	m.save(m.sess.Save())
	if s.ClaudeID != "" {
		m.sui.sessInfo = dim.Render("retomando o agent de " + s.Key + "…")
		ag := m.ag
		return m, func() tea.Msg {
			id, err := ag.Resume(s.Worktree, s.ClaudeID)
			return agentLaunchedMsg{id: s.ID, agentID: id, err: err}
		}
	}
	return m, m.launchAgentCmd(s)
}

// launchAgentCmd monta o prompt da tarefa (Jira + GitLab) e cria o agent em
// background no worktree da sessão.
func (m *Model) launchAgentCmd(s session.Session) tea.Cmd {
	m.sui.sessInfo = dim.Render("lançando o agent de " + s.Key + "…")
	ag, gl, ji, cfg := m.ag, m.glSrc, m.jiSrc, m.cfg.Claude
	mrs := m.myMRs() // dados do GitLab já em memória; rede fica no closure
	return func() tea.Msg {
		var prompt string
		switch {
		case s.Kind == session.KindFree:
			prompt = claude.FreePrompt(s.Service, s.UserNote)
		case s.IsReview():
			prompt = claude.ReviewMRPrompt(agents.BuildContext(gl, ji, cfg.Templates[s.Service], s, mrs))
		default:
			prompt = claude.AgentPrompt(agents.BuildContext(gl, ji, cfg.Templates[s.Service], s, mrs))
		}
		id, err := ag.Launch(agents.LaunchOpts{
			Dir:            s.Worktree,
			Name:           truncate(s.Key+" "+s.Title, 60),
			Prompt:         prompt,
			Model:          cfg.Model,
			PermissionMode: cfg.PermissionMode,
		})
		return agentLaunchedMsg{id: s.ID, agentID: id, prompt: prompt, err: err}
	}
}

// stopAgent para o agent de background (a conversa fica; 's' retoma).
func (m Model) stopAgent(r agentRow) (tea.Model, tea.Cmd) {
	if r.live == nil || !r.live.IsBackground() {
		m.sui.sessInfo = dim.Render("só agents de background vivos podem ser parados aqui")
		return m, nil
	}
	ag, ref := m.ag, r.live.Ref()
	id := ""
	if r.sess != nil {
		id = r.sess.ID
	}
	label := r.label()
	m.sui.sessInfo = dim.Render("parando " + label + "…")
	return m, func() tea.Msg {
		if err := ag.Stop(ref); err != nil {
			return agentActionMsg{err: err}
		}
		msg := agentActionMsg{id: id, info: label + " parado — s retoma"}
		if id != "" {
			msg.status = session.StatusCancelled
		}
		return msg
	}
}

// finishAgentCmd conclui a sessão: tira o agent do agent view, remove o
// worktree (com guarda de mudanças não commitadas, a menos que force) e
// marca como concluída. Os commits ficam na branch.
func (m Model) finishAgentCmd(r agentRow, force bool) tea.Cmd {
	s, ag := *r.sess, m.ag
	ref := s.AgentID
	if r.live != nil {
		ref = r.live.Ref()
	}
	return func() tea.Msg {
		if s.OwnsWorktree() {
			if err := worktree.Remove(s.Repo, s.Worktree, force); err != nil {
				if !force {
					return agentActionMsg{id: s.ID, err: fmt.Errorf("%w (commits ficam na branch; use F para descartar o resto)", err)}
				}
				return agentActionMsg{id: s.ID, err: err}
			}
		}
		if ref != "" {
			_ = ag.Remove(ref) // já pode ter saído do agent view
		}
		return agentActionMsg{id: s.ID, status: session.StatusCompleted, info: s.Key + " concluída"}
	}
}

// removeAgentCmd apaga a sessão da lista; se o worktree ainda existir,
// remove antes (com guarda de mudanças, a menos que force).
func (m Model) removeAgentCmd(r agentRow, force bool) tea.Cmd {
	s, ag := *r.sess, m.ag
	return func() tea.Msg {
		if s.Active() && s.OwnsWorktree() {
			if err := worktree.Remove(s.Repo, s.Worktree, force); err != nil {
				if !force {
					return agentActionMsg{id: s.ID, err: fmt.Errorf("%w (use D para forçar)", err)}
				}
				return agentActionMsg{id: s.ID, err: err}
			}
		}
		if s.AgentID != "" {
			_ = ag.Remove(s.AgentID)
		}
		return agentActionMsg{id: s.ID, remove: true}
	}
}

// openAgentDetail mostra a sessão no painel de detalhes: estado, uso,
// arquivos editados, a explicação e as últimas respostas do agent.
func (m Model) openAgentDetail(r agentRow) (tea.Model, tea.Cmd) {
	m.mode = modeDetail
	m.detail.loading = false
	m.detail.title = "agent " + r.label()
	m.detail.url = ""
	if r.sess != nil {
		m.detail.url = r.sess.URL
	}
	m.vp.GotoTop()

	width := max(40, m.vp.Width-2)
	rule := dim.Render(strings.Repeat("─", min(width, 100)))
	field := func(name, value string) string {
		return dim.Render(fmt.Sprintf("%-10s", name)) + value + "\n"
	}

	var b strings.Builder
	title := r.label()
	if r.sess != nil {
		title = r.sess.Key + "  " + r.sess.Title
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(title) + "\n\n")
	b.WriteString(field("estado", liveLabel(r)))
	if r.sess != nil {
		mode := "desenvolvimento"
		switch {
		case r.sess.Kind == session.KindFree:
			mode = "livre"
		case r.sess.IsReview():
			mode = "revisão de MR"
		}
		b.WriteString(field("tipo", mode))
		b.WriteString(field("serviço", strings.Join(nonEmpty([]string{r.sess.Service, r.sess.Branch}), " · ")))
		b.WriteString(field("pasta", r.sess.Worktree))
	} else {
		b.WriteString(field("tipo", r.live.Kind))
		b.WriteString(field("pasta", r.live.Cwd))
	}
	if r.live != nil && r.live.Ref() != "" {
		b.WriteString(field("id", r.live.Ref()))
	}
	if u := usage(r); u != "" {
		b.WriteString(field("uso", u))
	}
	if r.ai != nil && r.ai.Missing != "" {
		b.WriteString(field("falta", warnStyle.Render(r.ai.Missing)+dim.Render("  (haiku "+r.ai.At.Format("15:04")+")")))
	}
	if r.sess != nil && r.sess.Err != "" {
		b.WriteString(field("erro", errStyle.Render(r.sess.Err)))
	}
	if r.beat != nil && len(r.beat.Files) > 0 {
		b.WriteString("\n" + section.Render("Arquivos editados") + "\n")
		for _, f := range r.beat.Files {
			b.WriteString("  " + f + "\n")
		}
	}
	if r.sess != nil {
		if n := strings.TrimSpace(r.sess.UserNote); n != "" {
			b.WriteString("\n" + section.Render("Sua explicação") + "\n" + renderMarkdown(n, width) + "\n")
		}
	}
	b.WriteString("\n" + section.Render("Últimas respostas do agent") + dim.Render("  (mais nova primeiro)") + "\n")
	if r.tr == nil || len(r.tr.Recent) == 0 {
		b.WriteString(dim.Render("(o agent ainda não respondeu)") + "\n")
	} else {
		for i := len(r.tr.Recent) - 1; i >= 0; i-- {
			b.WriteString(rule + "\n" + renderMarkdown(r.tr.Recent[i], width) + "\n\n")
		}
	}
	b.WriteString(rule + "\n" + dim.Render("a abre a conversa · x para · s retoma · v diff · esc volta"))
	m.detail.body = b.String()
	return m, nil
}

func nonEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// pendingSession guarda o que falta decidir (explicação e serviço) entre
// apertar 'c'/'n' e o lançamento do agent.
type pendingSession struct {
	sess         session.Session
	createBranch bool
	guess        string   // serviço deduzido da #TAG/projeto, se houver
	services     []string // serviços detectados no momento do 'c'
}

// newSessionFromItem monta a sessão a partir do item selecionado e tenta
// adivinhar o serviço; devolve também se a branch deve ser criada.
func (m Model) newSessionFromItem(it *focusItem) (sess session.Session, guess string, createBranch bool) {
	if it.mr != nil {
		mr := *it.mr
		// MR de outra pessoa (você é só revisor) → revisão; o seu próprio →
		// desenvolvimento. O usuário pode trocar.
		mode := session.ModeImplement
		if !m.ownsMR(mr) {
			mode = session.ModeReview
		}
		sess = session.Session{
			Key:    mr.ShortRef(),
			Title:  mr.ShortTitle(),
			URL:    mr.WebURL,
			Branch: mr.SourceBranch,
			Kind:   session.KindMR,
			Mode:   mode,
		}
		guess = mr.Project()
		return sess, guess, false
	}
	is := *it.issue
	sess = session.Session{
		Key:    is.Key,
		Title:  is.Summary,
		URL:    strings.TrimRight(m.cfg.Jira.URL, "/") + "/browse/" + is.Key,
		Branch: m.cfg.Claude.BranchPrefix + is.Key,
		Kind:   session.KindIssue,
	}
	// Issue: o serviço vem de um MR já ligado pela #TAG, se houver.
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

// ownsMR informa se o MR é de autoria do usuário — o que decide o modo
// padrão: desenvolvimento no seu, revisão no dos outros.
func (m Model) ownsMR(mr gitlab.MR) bool {
	for _, x := range m.myMRs() {
		if x.ProjectID == mr.ProjectID && x.IID == mr.IID {
			return true
		}
	}
	return false
}

// startSession inicia o fluxo da tecla 'c': monta a sessão, muda para a
// aba Agentes e abre o textbox de explicação da tarefa.
func (m Model) startSession(it *focusItem) (tea.Model, tea.Cmd) {
	sess, guess, create := m.newSessionFromItem(it)
	if sess.Branch == "" {
		m.sui.sessInfo = errStyle.Render("MR sem branch de origem — atualize (r) e tente de novo")
		return m, nil
	}
	if m.sess.HasActiveFor(sess.Key) {
		m.sui.sessInfo = warnStyle.Render("já existe agent ativo para " + sess.Key)
		return m, nil
	}
	return m.describe(sess, guess, create)
}

// startFreeAgent é a tecla 'n': um agent sem issue/MR, que roda direto no
// repositório escolhido.
func (m Model) startFreeAgent() (tea.Model, tea.Cmd) {
	return m.describe(session.Session{Kind: session.KindFree, Mode: session.ModeImplement}, "", false)
}

// describe detecta os serviços (falha rápida, antes de o usuário digitar a
// explicação) e abre o textbox.
func (m Model) describe(sess session.Session, guess string, create bool) (tea.Model, tea.Cmd) {
	services, err := worktree.DetectServices(m.cfg.Claude.SourcesDir)
	if err != nil {
		m.sui.sessInfo = errStyle.Render("lendo " + m.cfg.Claude.SourcesDir + ": " + err.Error())
		return m, nil
	}
	if len(services) == 0 {
		m.sui.sessInfo = errStyle.Render("nenhum serviço (repo git) em " + m.cfg.Claude.SourcesDir)
		return m, nil
	}
	m.sui.pending = &pendingSession{sess: sess, createBranch: create, guess: guess, services: services}
	m.tab = tabAgentes
	m.cursor = 0
	m.filter = ""
	m.sui.sessInfo = ""
	m.mode = modeDescribing
	m.sui.descInput.Reset()
	m.sui.descInput.Focus()
	m.vp.GotoTop()
	return m, textarea.Blink
}

// updateDescribe trata o textbox de explicação da tarefa.
func (m Model) updateDescribe(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.sui.pending = nil
		m.sui.descInput.Blur()
		m.sui.sessInfo = dim.Render("agent cancelado")
		return m, nil
	case "ctrl+d", "ctrl+s":
		if m.sui.pending == nil {
			m.mode = modeNormal
			return m, nil
		}
		note := strings.TrimSpace(m.sui.descInput.Value())
		if m.sui.pending.sess.Kind == session.KindFree && note == "" {
			m.sui.sessInfo = warnStyle.Render("agent livre precisa da explicação da tarefa")
			return m, nil
		}
		m.mode = modeNormal
		m.sui.descInput.Blur()
		m.sui.pending.sess.UserNote = note
		return m.continueSession()
	case "ctrl+r":
		// Alterna entre desenvolvimento e revisão sem perder a explicação.
		if p := m.sui.pending; p != nil && p.sess.Kind != session.KindFree {
			if p.sess.IsReview() {
				p.sess.Mode = session.ModeImplement
			} else {
				p.sess.Mode = session.ModeReview
			}
		}
		return m, nil
	case "ctrl+e":
		// Edita a explicação no editor externo: leva o texto atual e traz de
		// volta o que for salvo. A TUI fica suspensa enquanto isso.
		return m, editNoteCmd(m.cfg, m.sui.descInput.Value())
	}
	var cmd tea.Cmd
	m.sui.descInput, cmd = m.sui.descInput.Update(msg)
	return m, cmd
}

// viewDescribe desenha o textbox de explicação na aba Agentes.
func (m Model) viewDescribe() string {
	var b strings.Builder
	p := m.sui.pending
	if p == nil {
		return ""
	}
	head, mode, expl, start := "🤖 Novo agent — "+p.sess.Key,
		okStyle.Render("desenvolvimento"),
		"Explique a tarefa; a descrição e os comentários da issue/MR entram junto no prompt.",
		"ctrl+d lança o agent em background"
	switch {
	case p.sess.Kind == session.KindFree:
		head, mode = "🤖 Novo agent livre", okStyle.Render("livre (roda no repositório, sem worktree)")
		expl = "Explique a tarefa; depois você escolhe o serviço (repositório)."
	case p.sess.IsReview():
		head, mode = "🔎 Nova revisão — "+p.sess.Key, warnStyle.Render("revisão (só revisa o MR e reporta)")
		expl = "Diga o que olhar com atenção (opcional); a descrição e os comentários do MR entram no prompt."
	}
	b.WriteString(section.Render(head) + " " + p.sess.Title + "\n\n")
	b.WriteString(dim.Render("modo: ") + mode)
	if p.sess.Kind != session.KindFree {
		b.WriteString(dim.Render("  (ctrl+r troca)"))
	}
	b.WriteString("\n" + dim.Render(expl) + "\n\n")
	b.WriteString(m.sui.descInput.View() + "\n\n")
	b.WriteString(dim.Render(start + " · ctrl+e edita no editor · esc cancela"))
	return b.String()
}

// continueSession segue após a explicação: deduz o serviço ou abre a lista
// de escolha; com serviço definido, prepara a pasta e lança o agent.
func (m Model) continueSession() (tea.Model, tea.Cmd) {
	p := m.sui.pending
	for _, s := range p.services {
		if strings.EqualFold(s, p.guess) {
			p.sess.Service = s
			m.sui.pending = nil
			return m.launchCreate(p.sess, p.createBranch)
		}
	}
	// Sem dedução: o usuário escolhe na lista.
	m.mode = modePickingService
	m.sui.pickOptions = p.services
	m.sui.pickCursor = 0
	return m, nil
}

// launchCreate prepara a pasta do agent em background: um worktree novo para
// issue/MR, o próprio repositório para o agent livre.
func (m Model) launchCreate(sess session.Session, createBranch bool) (tea.Model, tea.Cmd) {
	sess.Repo = filepath.Join(m.cfg.Claude.SourcesDir, sess.Service)
	if sess.Kind == session.KindFree {
		sess.Key = sess.Service
		sess.Title = truncate(firstLineOf(sess.UserNote), 60)
	}
	sess.ID = session.NewID(sess.Key)
	sess.Status = session.StatusPending
	sess.Created = time.Now()
	if sess.Kind == session.KindFree {
		sess.Worktree = sess.Repo
		return m, func() tea.Msg { return sessCreatedMsg{sess: sess} }
	}
	sess.Worktree = filepath.Join(m.cfg.Claude.WorktreesDir, sess.ID)
	m.sui.sessInfo = dim.Render("criando worktree de " + sess.Key + " em " + sess.Service + "…")
	return m, func() tea.Msg {
		err := worktree.Add(sess.Repo, sess.Worktree, sess.Branch, createBranch)
		return sessCreatedMsg{sess: sess, err: err}
	}
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// sessCreatedMsg chega quando a pasta de uma nova sessão ficou pronta (ou
// falhou); a sessão só entra no store se err == nil.
type sessCreatedMsg struct {
	sess session.Session
	err  error
}

// updatePickService trata a lista de escolha de serviço.
func (m Model) updatePickService(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		// Volta para o textbox sem perder a explicação digitada.
		if m.sui.pending != nil {
			m.mode = modeDescribing
			m.sui.descInput.Focus()
			return m, textarea.Blink
		}
		m.mode = modeNormal
		return m, nil
	case "j", "down":
		if m.sui.pickCursor < len(m.sui.pickOptions)-1 {
			m.sui.pickCursor++
		}
		return m, nil
	case "k", "up":
		if m.sui.pickCursor > 0 {
			m.sui.pickCursor--
		}
		return m, nil
	case "enter":
		p := m.sui.pending
		m.mode = modeNormal
		m.sui.pending = nil
		if p == nil {
			return m, nil
		}
		p.sess.Service = m.sui.pickOptions[m.sui.pickCursor]
		return m.launchCreate(p.sess, p.createBranch)
	}
	return m, nil
}

// viewPickService desenha a lista de serviços para a sessão pendente.
func (m Model) viewPickService() string {
	var b strings.Builder
	key := "o agent"
	if m.sui.pending != nil && m.sui.pending.sess.Key != "" {
		key = m.sui.pending.sess.Key
	}
	b.WriteString(section.Render("🛠 Em qual serviço trabalhar "+key+"?") + "\n\n")
	for i, s := range m.sui.pickOptions {
		cursor := "  "
		if i == m.sui.pickCursor {
			cursor = cursorStyle.Render("▌ ")
		}
		b.WriteString(cursor + s + "\n")
	}
	return b.String()
}

// openDiff mostra no painel de detalhes o que a branch da pasta traz.
func (m Model) openDiff(label, url, dir string) (tea.Model, tea.Cmd) {
	m.mode = modeDetail
	m.detail.loading = true
	m.detail.body = ""
	m.detail.title = "diff " + label
	m.detail.url = url
	m.vp.GotoTop()
	return m, func() tea.Msg {
		d, err := worktree.Diff(dir)
		if err != nil {
			return detailMsg{err: err}
		}
		if strings.TrimSpace(d) == "" {
			d = dim.Render("(sem mudanças no worktree)")
		}
		return detailMsg{body: d}
	}
}

// truncate corta o texto numa linha só, com reticências.
func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
