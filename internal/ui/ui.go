package ui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/history"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/pipeline"
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
	modePickingService
	modeFiltering
	modeDetail
	modeReport
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
	badge string // selo do rodapé (ex.: "🧪 DEMO"); vazio no modo normal

	glSrc  gitlabSource
	jiSrc  jiraSource
	runner *pipeline.Runner

	mode mode
	tab  tab

	fetch  fetchState
	alert  alertState
	detail detailState
	sui    sessionUI

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
}

// sessionUI guarda o estado de tela das sessões de trabalho.
type sessionUI struct {
	descInput   textarea.Model
	pending     *pendingSession
	pickOptions []string
	pickCursor  int
	sessInfo    string
	progress    map[string]claude.Progress
	ticking     bool
}

func New(cfg config.Config, store *tasks.Store, hist *history.Store, sess *session.Store, gl gitlabSource, ji jiraSource, badge string) Model {
	ti := textinput.New()
	ti.Placeholder = "descrição · data @today/@tomorrow/@2026-06-15 · hora @today 15:00 · prioridade !alta/!critica/!media/!baixa"

	fi := textinput.New()
	fi.Placeholder = "filtrar por chave, título ou status"

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))

	ta := textarea.New()
	ta.Placeholder = "explique o que deve ser feito nesta tarefa (contexto, restrições, o que validar)…"
	ta.SetHeight(8)

	ctx, cancel := context.WithCancel(context.Background())
	runner := pipeline.New(cfg.Claude, gl, ji)

	return Model{
		cfg: cfg, store: store, hist: hist, sess: sess, badge: badge,
		glSrc: gl, jiSrc: ji, runner: runner,
		input: ti, filterInput: fi, spin: sp, vp: viewport.New(80, 20),
		fetch: fetchState{ctx: ctx, cancel: cancel, loading: 2},
		alert: alertState{
			notified:    map[string]bool{},
			seenTodos:   map[int]bool{},
			issueStatus: map[string]string{},
		},
		sui: sessionUI{
			descInput: ta,
			progress:  map[string]claude.Progress{},
		},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchAll(), m.spin.Tick, tick(), reminderTick())
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
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(20, msg.Width-10)
		m.sui.descInput.SetWidth(max(40, msg.Width-12))
		m.vp.Width = max(36, m.width-8)
		m.vp.Height = max(5, m.height-5)
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
		return m, m.gitlabAlerts()

	case jiMsg:
		if msg.gen != m.fetch.gen {
			return m, nil
		}
		m.fetch.loading--
		m.fetch.ji, m.fetch.jiErr = msg.sum, msg.err
		m.fetch.updated = time.Now()
		m.recordToday()
		return m, m.jiraAlerts()

	case sessCreatedMsg:
		if msg.err != nil {
			m.sui.sessInfo = errStyle.Render(msg.err.Error())
			return m, nil
		}
		m.sess.Add(msg.sess)
		m.save(m.sess.Save())
		m.sui.sessInfo = okStyle.Render("sessão criada: " + msg.sess.Key + " em " + msg.sess.Worktree)
		// Worktree pronto → o pipeline (plan → dev → review) já inicia.
		return m.startRun(msg.sess.ID)

	case sessTickMsg:
		m.pollProgress()
		if m.anyRunning() {
			return m, sessTick()
		}
		m.sui.ticking = false
		return m, nil

	case sessFinishedMsg:
		sess, ok := m.sess.Get(msg.id)
		if !ok {
			return m, nil
		}
		if sess.Status == session.StatusCancelled {
			m.save(m.sess.Save())
			return m, nil
		}
		var title, body string
		m.sess.Update(msg.id, func(s *session.Session) {
			s.Prompt = msg.prompt
			// O log final traz o session_id do Claude (para retomar a fase
			// certa depois) e o resumo da fase.
			if s.LogFile != "" {
				if p, err := claude.ReadProgress(s.LogFile); err == nil {
					m.sui.progress[s.ID] = p
					if p.SessionID != "" {
						s.SetClaudeID(s.Phase, p.SessionID)
					}
					if p.Result != "" {
						s.SetResult(s.Phase, p.Result)
					}
				}
			}
			now := time.Now()
			s.Finished = &now
			title = "wmonit — sessão " + s.Key
			switch {
			case msg.err != nil:
				s.Status = session.StatusFailed
				s.Err = msg.err.Error()
				body = "falhou na fase " + phaseLabel(s.Phase, s.Mode) + ": " + s.Err
			case !s.IsReview() && session.NextPhase(s.Phase) != "":
				// Gate manual: a próxima fase só roda com a sua aprovação.
				s.Status = session.StatusWaiting
				body = phaseLabel(s.Phase, s.Mode) + " concluído — revise (enter) e aprove (s)"
			case s.IsReview():
				s.Status = session.StatusDone
				body = "revisão concluída — veja o resultado (enter)"
			default:
				s.Status = session.StatusDone
				body = "review concluído — veja o veredito (enter)"
			}
		})
		m.save(m.sess.Save())
		return m, notifyCmd(title, body)

	case interactiveDoneMsg:
		if msg.err != nil {
			m.sui.sessInfo = errStyle.Render("claude interativo: " + msg.err.Error())
		} else if sess, ok := m.sess.Get(msg.id); ok {
			if sess.Status == session.StatusPending {
				m.sess.Update(msg.id, func(s *session.Session) { s.Status = session.StatusDone })
				m.save(m.sess.Save())
			}
			m.sui.sessInfo = dim.Render("sessão interativa de " + sess.Key + " encerrada — revise com v")
		}
		return m, nil

	case editorClosedMsg:
		// Abrir o worktree no editor não devolve conteúdo; só reportamos erro.
		if msg.err != nil {
			m.sui.sessInfo = errStyle.Render("editor: " + msg.err.Error())
		}
		return m, nil

	case descEditedMsg:
		// Voltou do editor da explicação: traz o texto editado pro textbox.
		if msg.err != nil {
			m.sui.sessInfo = errStyle.Render("editor: " + msg.err.Error())
		} else {
			m.sui.descInput.SetValue(strings.TrimRight(msg.text, "\n"))
		}
		if m.mode == modeDescribing {
			m.sui.descInput.Focus()
			return m, textarea.Blink
		}
		return m, nil

	case sessActionMsg:
		if msg.err != nil {
			m.sui.sessInfo = errStyle.Render(msg.err.Error())
			return m, nil
		}
		if msg.remove {
			for i := range m.sess.Sessions {
				if m.sess.Sessions[i].ID == msg.id {
					m.sess.DeleteAt(i)
					break
				}
			}
			if m.cursor >= len(m.sess.Sessions) && m.cursor > 0 {
				m.cursor--
			}
		} else if msg.status != "" {
			m.sess.Update(msg.id, func(s *session.Session) { s.Status = msg.status })
		}
		// Estado de runtime da sessão que saiu de cena não fica para trás.
		delete(m.sui.progress, msg.id)
		m.runner.Forget(msg.id)
		m.save(m.sess.Save())
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeAdding:
			return m.updateAdding(msg)
		case modeDescribing:
			return m.updateDescribe(msg)
		case modePickingService:
			return m.updatePickService(msg)
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
	return m, nil
}

func (m Model) updateAdding(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) updateReport(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		case " ", "x":
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
	if line < m.vp.YOffset {
		m.vp.SetYOffset(line)
	} else if line >= m.vp.YOffset+m.vp.Height {
		m.vp.SetYOffset(line - m.vp.Height + 1)
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
