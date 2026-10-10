package ui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/history"
	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
)

// gitlabSource e jiraSource são os contratos mínimos que a UI usa das
// fontes de dados. O *gitlab.Client/*jira.Client reais e as fontes do modo
// demo os satisfazem — a UI não conhece qual é qual (injetadas por main).
type gitlabSource interface {
	Fetch(ctx context.Context) (*gitlab.Summary, error)
	MRNotes(ctx context.Context, projectID, iid int) ([]gitlab.Note, error)
}

type jiraSource interface {
	Fetch(ctx context.Context) (*jira.Summary, error)
	IssueDetail(ctx context.Context, key string) (*jira.IssueDetail, error)
}

type tab int

const (
	tabHoje tab = iota
	tabDesempenho
	tabGitLab
	tabJira
	tabTarefas
	tabSessoes
	numTabs
)

type mode int

const (
	modeNormal mode = iota
	modeAdding
	modeDescribing
	modePicking
	modeFiltering
	modeDetail
	modeReport
	modeForm
)

const refreshEvery = 5 * time.Minute
const reminderEvery = 30 * time.Second

// glMsg é a resposta de uma rodada de fetch do GitLab. gen marca de qual
// rodada ela veio: o refresh cancela o ctx da rodada anterior, e o gen
// descarta respostas que completaram antes do cancelamento (senão o
// contador de loading fica negativo e dados velhos sobrescrevem os novos).
type glMsg struct {
	gen int
	sum *gitlab.Summary
	err error
}

type jiMsg struct {
	gen int
	sum *jira.Summary
	err error
}

type tickMsg time.Time
type reminderMsg time.Time

type detailMsg struct {
	body string
	err  error
}

type Model struct {
	cfg   config.Config
	store *tasks.Store
	hist  *history.Store
	sess  *session.Store
	badge string
	open  string

	glSrc gitlabSource
	jiSrc jiraSource
	hr    hub.Herdr

	mode mode
	tab  tab

	fetch  fetchState
	alert  alertState
	detail detailState
	sui    sessionUI
	live   liveState
	help   helpState

	cursor  int
	addErr  string
	saveErr string // última falha ao gravar tarefas/sessões, mostrada no rodapé
	input   textinput.Model

	filter      string
	filterInput textinput.Model

	spin spinner.Model
	vp   viewport.Model

	width, height int
}

// fetchState guarda a rodada de fetch do GitLab/Jira e seus resultados. gen
// identifica a rodada, para descartar respostas de uma já substituída.
type fetchState struct {
	gen     int
	ctx     context.Context
	cancel  context.CancelFunc
	loading int
	gl      *gitlab.Summary
	glErr   error
	ji      *jira.Summary
	jiErr   error
	mine    []gitlab.MR
	updated time.Time
}

// alertState guarda o que já foi alertado, para não repetir notificações.
type alertState struct {
	notified    map[string]bool
	seenTodos   map[int]bool
	issueStatus map[string]string
	glBaseline  bool
	jiBaseline  bool
}

// detailState guarda o painel de detalhes (issue/MR/sessão/diff).
type detailState struct {
	loading bool
	title   string
	url     string
	body    string
	hub     bool
}

type sessionUI struct {
	descInput    textarea.Model
	form         *createForm
	confirmClean bool
	pending      *pendingSession
	pick         pickState
	target       string
	info         string
	snap         hub.Snapshot
	lastStatus   map[string]string
	readySince   map[string]time.Time
	delivering   map[string]bool
	hubAsked     time.Time
	hubAt        time.Time
	hubReply     string
	hubWaiting   bool
	hubPending   bool
}

func New(cfg config.Config, store *tasks.Store, hist *history.Store, sess *session.Store, gl gitlabSource, ji jiraSource, hr hub.Herdr, badge string) Model {
	ti := textinput.New()
	ti.Placeholder = "descrição · data @today/@tomorrow/@2026-06-15 · hora @today 15:00 · prioridade !alta/!critica/!media/!baixa"

	fi := textinput.New()
	fi.Placeholder = "filtrar por chave, título ou status"

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))

	ta := textarea.New()
	ta.Placeholder = "explique o que deve ser feito nesta tarefa (contexto, restrições, o que validar)…"
	ta.SetHeight(8)

	ctx, cancel := context.WithCancel(context.Background())

	sui := sessionUI{
		descInput:  ta,
		lastStatus: map[string]string{},
		readySince: map[string]time.Time{},
		delivering: map[string]bool{},
	}
	if reply, at, ok := hub.ReadReply(hub.HubDir(sess.Dir()), time.Time{}); ok {
		sui.hubReply, sui.hubAt = reply, at
	}

	return Model{
		cfg: cfg, store: store, hist: hist, sess: sess, badge: badge,
		glSrc: gl, jiSrc: ji, hr: hr,
		input: ti, filterInput: fi, spin: sp, vp: viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		fetch: fetchState{ctx: ctx, cancel: cancel, loading: 2},
		alert: alertState{
			notified:    map[string]bool{},
			seenTodos:   map[int]bool{},
			issueStatus: map[string]string{},
		},
		sui:  sui,
		live: newLiveState(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchAll(), m.spin.Tick, tick(), reminderTick(), m.poll(true), m.startEvents(), m.watchHubReply())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func reminderTick() tea.Cmd {
	return tea.Tick(reminderEvery, func(t time.Time) tea.Msg { return reminderMsg(t) })
}

func (m *Model) checkReminders() tea.Cmd {
	now := time.Now()
	today := now.Format("2006-01-02")
	var cmds []tea.Cmd
	for _, t := range m.store.Tasks {
		if t.Done || t.DueTime == "" || t.Due > today {
			continue
		}

		due, err := time.ParseInLocation("2006-01-02 15:04", t.Due+" "+t.DueTime, time.Local)
		if err != nil || now.Before(due) {
			continue
		}

		if t.Urgent() {
			title := "wmonit — ⬆ tarefa de prioridade ALTA"
			if t.Priority == tasks.PriorityCritical {
				title = "wmonit — 🔴 tarefa CRÍTICA"
			}
			cmds = append(cmds, notifyCmd(title, t.Text+" ("+t.DueTime+")"))
			continue
		}

		key := t.Due + " " + t.DueTime + " " + t.Text
		if m.alert.notified[key] {
			continue
		}

		m.alert.notified[key] = true
		cmds = append(cmds, notifyCmd("wmonit — lembrete", t.Text))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) recordToday() {
	if m.hist == nil || m.fetch.gl == nil || m.fetch.ji == nil {
		return
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	tsk := 0
	for _, t := range m.store.Tasks {
		if t.Done && t.DoneAt != nil && t.DoneAt.Format("2006-01-02") == today {
			tsk++
		}
	}
	if m.hist.Upsert(history.Day{
		Date:   today,
		MRs:    len(mergedIn(m.fetch.gl.Merged, dayStart, now)),
		Issues: len(resolvedIn(m.fetch.ji.Resolved, today, today)),
		Tasks:  tsk,
	}) {
		m.hist.Save()
	}
}

func (m Model) fetchAll() tea.Cmd {
	gen := m.fetch.gen
	gl, ji := m.glSrc, m.jiSrc
	ctx := m.fetch.ctx
	return tea.Batch(
		func() tea.Msg {
			s, err := gl.Fetch(ctx)
			return glMsg{gen, s, err}
		},
		func() tea.Msg {
			s, err := ji.Fetch(ctx)
			return jiMsg{gen, s, err}
		},
	)
}

// save registra no rodapé quando a gravação de tarefas/sessões falha —
// perder uma tarefa em silêncio é o pior caso do app.
func (m *Model) save(err error) {
	if err != nil {
		m.saveErr = "não gravou: " + err.Error()
		return
	}
	m.saveErr = ""
}

func (m *Model) refresh() tea.Cmd {
	// Cancela o ctx da rodada anterior: aborta as requisições de verdade em
	// vez de só ignorar as respostas (senão elas seguem vivas até o timeout
	// de 15s).
	m.fetch.cancel()
	m.fetch.ctx, m.fetch.cancel = context.WithCancel(context.Background())
	m.fetch.gen++
	m.fetch.loading = 2
	return m.fetchAll()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, ok := m.updateLive(msg); ok {
		return next, cmd
	}
	if next, cmd, ok := m.updateWorkspaces(msg); ok {
		return next, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(20, msg.Width-10))
		m.sui.descInput.SetWidth(max(40, msg.Width-12))
		m.vp.SetWidth(max(36, m.width-8))
		m.vp.SetHeight(max(5, m.height-5))
		if m.sui.form != nil {
			m.sui.form.form.WithWidth(max(40, m.vp.Width()-2))
		}
		if m.help.open {
			w, h := m.helpSize()
			m.help.vp.SetWidth(w - 4)
			m.help.vp.SetHeight(h - 6)
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())

	case reminderMsg:
		return m, tea.Batch(m.checkReminders(), reminderTick())

	case detailMsg:
		m.detail.loading = false
		if msg.err != nil {
			m.detail.body = errStyle.Render(msg.err.Error())
		} else {
			m.detail.body = msg.body
		}
		m.vp.GotoTop()
		return m, nil

	case glMsg:
		if msg.gen != m.fetch.gen {
			return m, nil // resposta de uma rodada já substituída
		}
		m.fetch.loading--
		m.fetch.gl, m.fetch.glErr = msg.sum, msg.err
		m.fetch.mine = nil
		if m.fetch.gl != nil {
			m.fetch.mine = m.fetch.gl.Mine()
		}
		m.fetch.updated = time.Now()
		m.recordToday()
		if m.open != "" && m.fetch.loading == 0 && m.mode == modeNormal {
			next, cmd := m.resolveOpen()
			return next, tea.Batch(m.gitlabAlerts(), m.fetchMRStatus(), cmd)
		}
		return m, tea.Batch(m.gitlabAlerts(), m.fetchMRStatus())

	case jiMsg:
		if msg.gen != m.fetch.gen {
			return m, nil
		}
		m.fetch.loading--
		m.fetch.ji, m.fetch.jiErr = msg.sum, msg.err
		m.fetch.updated = time.Now()
		m.recordToday()
		if m.open != "" && m.fetch.loading == 0 && m.mode == modeNormal {
			next, cmd := m.resolveOpen()
			return next, tea.Batch(m.jiraAlerts(), cmd)
		}
		return m, m.jiraAlerts()

	case descEditedMsg:
		if msg.err != nil {
			m.sui.info = errStyle.Render("editor: " + msg.err.Error())
		} else {
			m.sui.descInput.SetValue(strings.TrimRight(msg.text, "\n"))
		}
		if m.mode == modeDescribing {
			m.sui.descInput.Focus()
			return m, textarea.Blink
		}
		return m, nil

	case configEditedMsg:
		return m.reloadConfig(msg)

	case tea.KeyPressMsg:
		if m.help.open {
			return m.updateHelp(msg)
		}
		if msg.String() == "?" && m.mode != modeForm && m.mode != modeDescribing && m.mode != modeAdding && m.mode != modeFiltering && !m.sui.pick.filtering {
			return m.openHelp()
		}
		switch m.mode {
		case modeForm:
			return m.updateForm(msg)
		case modeAdding:
			return m.updateAdding(msg)
		case modeDescribing:
			return m.updateDescribe(msg)
		case modePicking:
			return m.updatePick(msg)
		case modeFiltering:
			return m.updateFilter(msg)
		case modeDetail:
			return m.updateDetail(msg)
		case modeReport:
			return m.updateReport(msg)
		default:
			return m.updateKeys(msg)
		}
	}
	if m.mode == modeForm {
		return m.updateForm(msg)
	}
	return m, nil
}

func (m Model) updateAdding(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if v := strings.TrimSpace(m.input.Value()); v != "" {
			if err := m.store.Add(v); err != nil {
				// Vencimento inválido: mantém o textbox aberto com o aviso.
				m.addErr = err.Error()
				return m, nil
			}
			m.save(m.store.Save())
		}
		m.mode = modeNormal
		m.addErr = ""
		m.input.Reset()
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.addErr = ""
		m.input.Reset()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.addErr = "" // ao continuar digitando, some o aviso anterior
	return m, cmd
}

func (m Model) updateFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.mode = modeNormal
		m.filter = strings.TrimSpace(m.filterInput.Value())
		m.cursor = 0
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.filter = ""
		m.filterInput.Reset()
		m.cursor = 0
		return m, nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	m.filter = m.filterInput.Value()
	m.cursor = 0
	return m, cmd
}

func (m Model) updateReport(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "g":
		m.mode = modeNormal
		m.vp.GotoTop()
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "r":
		return m, m.refresh()
	}
	// As demais teclas rolam o relatório no viewport.
	m.vp.SetContent(m.content())
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) updateKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "g":
		m.mode = modeReport
		m.vp.GotoTop()
		return m, nil
	case "1", "2", "3", "4", "5", "6":
		m.tab = tab(msg.String()[0] - '1')
		m.cursor, m.filter = 0, ""
		m.vp.GotoTop()
		return m, nil
	case "tab", "l", "right":
		m.tab, m.cursor, m.filter = (m.tab+1)%numTabs, 0, ""
		m.vp.GotoTop()
		return m, nil
	case "shift+tab", "h", "left":
		m.tab, m.cursor, m.filter = (m.tab+numTabs-1)%numTabs, 0, ""
		m.vp.GotoTop()
		return m, nil
	case "r":
		return m, m.refresh()
	}

	if m.tab == tabTarefas {
		switch msg.String() {
		case "a":
			m.mode = modeAdding
			m.input.Focus()
			return m, textinput.Blink
		case "j", "down":
			if m.cursor < len(m.store.Tasks)-1 {
				m.cursor++
			}
			m.scrollToCursor()
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
			m.scrollToCursor()
		case "space", "x":
			m.store.ToggleAt(m.cursor)
			m.save(m.store.Save())
		case "d":
			m.store.DeleteAt(m.cursor)
			if m.cursor >= len(m.store.Tasks) && m.cursor > 0 {
				m.cursor--
			}
			m.save(m.store.Save())
		}
		return m, nil
	}

	if m.tab == tabSessoes {
		return m.sessionKeys(msg)
	}

	if m.tab == tabGitLab || m.tab == tabJira {
		switch msg.String() {
		case "j", "down":
			if m.cursor < m.focusCount()-1 {
				m.cursor++
			}
			m.scrollToItem()
			return m, nil
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
			m.scrollToItem()
			return m, nil
		case "o":
			if it := m.selectedItem(); it != nil {
				return m, openURLCmd(m.itemURL(it))
			}
			return m, nil
		case "enter":
			if it := m.selectedItem(); it != nil {
				return m.openDetail(it)
			}
			return m, nil
		case "/":
			m.mode = modeFiltering
			m.filter = ""
			m.filterInput.SetValue("")
			m.cursor = 0
			m.filterInput.Focus()
			return m, textinput.Blink
		case "c":
			if it := m.selectedItem(); it != nil {
				return m.startSession(it)
			}
			return m, nil
		}
		// Demais teclas (pgup/pgdn…) rolam o viewport.
		m.vp.SetContent(m.content())
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}

	// Nas demais abas o viewport cuida da rolagem (j/k, setas, pgup/pgdn…).
	// O conteúdo precisa estar carregado nele para a altura ser conhecida.
	m.vp.SetContent(m.content())
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// scrollTo carrega content no viewport e rola o mínimo para deixar a linha
// visível.
func (m *Model) scrollTo(content string, line int) {
	m.vp.SetContent(content)
	if line < m.vp.YOffset() {
		m.vp.SetYOffset(line)
	} else if line >= m.vp.YOffset()+m.vp.Height() {
		m.vp.SetYOffset(line - m.vp.Height() + 1)
	}
}

// scrollToItem mantém visível o item selecionado nas abas GitLab/Jira.
func (m *Model) scrollToItem() {
	content, sel := renderRows(m.currentRows(), m.cursor)
	m.scrollTo(content, sel)
}

// scrollToSession mantém visível a sessão selecionada — cada sessão ocupa
// mais de uma linha, então usa a linha real do item, não o índice do cursor.
func (m *Model) scrollToSession() {
	content, sel := m.viewSessoes()
	m.scrollTo(content, sel)
}

func (m *Model) scrollToCursor() { m.scrollTo(m.content(), m.cursor) }
