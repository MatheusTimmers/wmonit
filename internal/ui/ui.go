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
	"github.com/timmers/wmonit/internal/demo"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/history"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
)

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

// mode é o overlay de entrada ativo. Substitui um punhado de bools que
// precisavam ser mutuamente exclusivos sem nada garantir isso: agora o
// estado é impossível de ficar inconsistente e o dispatch é um só switch.
// modeAdding e modeFiltering são overlays "inline" (a aba continua desenhada
// por baixo, com o input embutido); os demais substituem o conteúdo.
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

// gen marca de qual rodada de fetch a resposta veio. O refresh cancela o
// ctx da rodada anterior (aborta as requisições de verdade); o gen descarta
// a janela restante — respostas que completaram antes do cancelamento
// (senão o contador de loading fica negativo e dados velhos sobrescrevem os
// novos).
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
	demo  bool // dados inventados, sem rede (flag --demo)

	// Clients reaproveitados entre refreshes — sem isso o cache de
	// metadados do Jira (campos, prioridades) não sobreviveria a cada busca.
	glClient *gitlab.Client
	jiClient *jira.Client

	mode mode // overlay de entrada ativo (normal, adding, describing…)

	// Estado das sessões de trabalho
	descInput   textarea.Model
	pickOptions []string
	pickCursor  int
	pending     *pendingSession
	sessInfo    string // última mensagem de status das sessões
	progress    map[string]claude.Progress
	handles     map[string]*claude.Handle      // execuções vivas, p/ cancelar
	taskCtx     map[string]*claude.TaskContext // contexto por sessão (1 busca por pipeline)
	ticking     bool                           // cadeia de polling das sessões viva

	tab     tab
	gl      *gitlab.Summary
	mine    []gitlab.MR
	glErr   error
	ji      *jira.Summary
	jiErr   error
	loading int

	fetchGen    int                // rodada atual de fetch; respostas de rodadas velhas são descartadas
	fetchCtx    context.Context    // ctx da rodada atual; cancelado quando outra começa
	fetchCancel context.CancelFunc

	cursor int
	addErr string // erro da última tentativa de adicionar tarefa (vencimento inválido)
	input  textinput.Model

	filter      string
	filterInput textinput.Model

	detailLoading bool
	detailTitle   string
	detailURL     string
	detailBody    string

	spin    spinner.Model
	vp      viewport.Model
	updated time.Time

	notified map[string]bool

	seenTodos   map[int]bool
	issueStatus map[string]string
	glBaseline  bool
	jiBaseline  bool

	width, height int
}

func New(cfg config.Config, store *tasks.Store, demo bool) Model {
	ti := textinput.New()
	ti.Placeholder = "descrição · data @today/@tomorrow/@2026-06-15 · hora @today 15:00 · prioridade !alta/!critica/!media/!baixa"
	fi := textinput.New()
	fi.Placeholder = "filtrar por chave, título ou status"
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	ta := textarea.New()
	ta.Placeholder = "explique o que deve ser feito nesta tarefa (contexto, restrições, o que validar)…"
	ta.SetHeight(8)
	hist, _ := history.Load() // sem histórico ainda não é erro fatal
	sess, _ := session.Load() // mesmo com erro o store volta utilizável
	glClient := gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token)
	jiClient := jira.New(cfg.Jira.URL, cfg.Jira.Auth, cfg.Jira.Email, cfg.Jira.Token, cfg.Jira.ComplexityField)
	ctx, cancel := context.WithCancel(context.Background())
	return Model{cfg: cfg, store: store, hist: hist, sess: sess, demo: demo, glClient: glClient, jiClient: jiClient, fetchCtx: ctx, fetchCancel: cancel, input: ti, filterInput: fi, descInput: ta, spin: sp, vp: viewport.New(80, 20), loading: 2, notified: map[string]bool{}, seenTodos: map[int]bool{}, issueStatus: map[string]string{}, progress: map[string]claude.Progress{}, handles: map[string]*claude.Handle{}, taskCtx: map[string]*claude.TaskContext{}}
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

// checkReminders usa receiver por ponteiro porque grava em m.notified; por
// valor só funcionava por o map ser referência — frágil para o próximo campo.
func (m *Model) checkReminders() tea.Cmd {
	now := time.Now()
	today := now.Format("2006-01-02")
	var cmds []tea.Cmd
	for _, t := range m.store.Tasks {
		if t.Done || t.DueTime == "" || t.Due != today {
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
		if m.notified[key] {
			continue
		}

		m.notified[key] = true
		cmds = append(cmds, notifyCmd("wmonit — lembrete", t.Text))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) recordToday() {
	if m.hist == nil || m.gl == nil || m.ji == nil {
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
		MRs:    len(mergedIn(m.gl.Merged, dayStart, now)),
		Issues: len(resolvedIn(m.ji.Resolved, today, today)),
		Tasks:  tsk,
	}) {
		m.hist.Save() // só grava quando o resumo do dia mudou
	}
}

func (m Model) fetchAll() tea.Cmd {
	gen := m.fetchGen
	if m.demo {
		return tea.Batch(
			func() tea.Msg { return glMsg{gen, demo.GitLab(), nil} },
			func() tea.Msg { return jiMsg{gen, demo.Jira(), nil} },
		)
	}
	gl, ji := m.glClient, m.jiClient
	ctx := m.fetchCtx
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

func (m *Model) refresh() tea.Cmd {
	// Aborta as requisições da rodada anterior em vez de só ignorar as
	// respostas — sem isso elas seguiriam vivas até o timeout de 15s.
	m.fetchCancel()
	m.fetchCtx, m.fetchCancel = context.WithCancel(context.Background())
	m.fetchGen++
	m.loading = 2
	return m.fetchAll()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(20, msg.Width-10)
		m.descInput.SetWidth(max(40, msg.Width-12))
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
		m.detailLoading = false
		if msg.err != nil {
			m.detailBody = errStyle.Render(msg.err.Error())
		} else {
			m.detailBody = msg.body
		}
		m.vp.GotoTop()
		return m, nil

	case glMsg:
		if msg.gen != m.fetchGen {
			return m, nil // resposta de uma rodada já substituída
		}
		m.loading--
		m.gl, m.glErr = msg.sum, msg.err
		m.mine = nil
		if m.gl != nil {
			m.mine = m.gl.Mine()
		}
		m.updated = time.Now()
		m.recordToday()
		return m, m.gitlabAlerts()

	case jiMsg:
		if msg.gen != m.fetchGen {
			return m, nil
		}
		m.loading--
		m.ji, m.jiErr = msg.sum, msg.err
		m.updated = time.Now()
		m.recordToday()
		return m, m.jiraAlerts()

	case sessCreatedMsg:
		if msg.err != nil {
			m.sessInfo = errStyle.Render(msg.err.Error())
			return m, nil
		}
		m.sess.Add(msg.sess)
		m.sess.Save()
		m.sessInfo = okStyle.Render("sessão criada: " + msg.sess.Key + " em " + msg.sess.Worktree)
		// Worktree pronto → o pipeline (plan → dev → review) já inicia.
		if s := m.sess.Find(msg.sess.ID); s != nil {
			return m.startRun(s)
		}
		return m, nil

	case sessTickMsg:
		m.pollProgress()
		if m.anyRunning() {
			return m, sessTick()
		}
		m.ticking = false
		return m, nil

	case sessFinishedMsg:
		delete(m.handles, msg.id)
		if msg.ctx != nil {
			// Contexto da tarefa buscado nesta fase: as próximas reutilizam.
			m.taskCtx[msg.id] = msg.ctx
		}
		if s := m.sess.Find(msg.id); s != nil {
			if s.Status == session.StatusCancelled {
				m.sess.Save()
				return m, nil
			}
			s.Prompt = msg.prompt
			// O log final traz o session_id do Claude (para retomar a fase
			// certa depois) e o resumo da fase.
			if s.LogFile != "" {
				if p, err := claude.ReadProgress(s.LogFile); err == nil {
					m.progress[s.ID] = p
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
			title := "wmonit — sessão " + s.Key
			var body string
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
			m.sess.Save()
			return m, notifyCmd(title, body)
		}
		return m, nil

	case interactiveDoneMsg:
		if msg.err != nil {
			m.sessInfo = errStyle.Render("claude interativo: " + msg.err.Error())
		} else if s := m.sess.Find(msg.id); s != nil {
			if s.Status == session.StatusPending {
				s.Status = session.StatusDone
				m.sess.Save()
			}
			m.sessInfo = dim.Render("sessão interativa de " + s.Key + " encerrada — revise com v")
		}
		return m, nil

	case editorClosedMsg:
		// Abrir o worktree no editor não devolve conteúdo; só reportamos erro.
		if msg.err != nil {
			m.sessInfo = errStyle.Render("editor: " + msg.err.Error())
		}
		return m, nil

	case descEditedMsg:
		// Voltou do editor da explicação: traz o texto editado pro textbox.
		if msg.err != nil {
			m.sessInfo = errStyle.Render("editor: " + msg.err.Error())
		} else {
			m.descInput.SetValue(strings.TrimRight(msg.text, "\n"))
		}
		if m.mode == modeDescribing {
			m.descInput.Focus()
			return m, textarea.Blink
		}
		return m, nil

	case sessActionMsg:
		if msg.err != nil {
			m.sessInfo = errStyle.Render(msg.err.Error())
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
		} else if s := m.sess.Find(msg.id); s != nil && msg.status != "" {
			s.Status = msg.status
		}
		// Estado de runtime da sessão que saiu de cena não fica para trás.
		delete(m.progress, msg.id)
		delete(m.taskCtx, msg.id)
		m.sess.Save()
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
			m.store.Save()
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
			m.store.Save()
		case "d":
			m.store.DeleteAt(m.cursor)
			if m.cursor >= len(m.store.Tasks) && m.cursor > 0 {
				m.cursor--
			}
			m.store.Save()
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

// scrollToItem garante que o item selecionado nas abas GitLab/Jira fique
// visível no viewport.
func (m *Model) scrollToItem() {
	content, sel := renderRows(m.currentRows(), m.cursor)
	m.vp.SetContent(content)
	if sel < m.vp.YOffset {
		m.vp.SetYOffset(sel)
	} else if sel >= m.vp.YOffset+m.vp.Height {
		m.vp.SetYOffset(sel - m.vp.Height + 1)
	}
}

// scrollToSession garante que a sessão selecionada fique visível —
// sessões ocupam mais de uma linha, então usa a linha real do item.
func (m *Model) scrollToSession() {
	content, sel := m.viewSessoes()
	m.vp.SetContent(content)
	if sel < m.vp.YOffset {
		m.vp.SetYOffset(sel)
	} else if sel >= m.vp.YOffset+m.vp.Height {
		m.vp.SetYOffset(sel - m.vp.Height + 1)
	}
}

func (m *Model) scrollToCursor() {
	m.vp.SetContent(m.content())
	if m.cursor < m.vp.YOffset {
		m.vp.SetYOffset(m.cursor)
	} else if m.cursor >= m.vp.YOffset+m.vp.Height {
		m.vp.SetYOffset(m.cursor - m.vp.Height + 1)
	}
}
