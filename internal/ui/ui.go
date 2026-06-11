package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/config"
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

const refreshEvery = 5 * time.Minute
const reminderEvery = 30 * time.Second

type glMsg struct {
	sum *gitlab.Summary
	err error
}

type jiMsg struct {
	sum *jira.Summary
	err error
}

type tickMsg time.Time
type reminderMsg time.Time

// detailMsg traz o conteúdo do painel de detalhes carregado de forma assíncrona.
type detailMsg struct {
	body string
	err  error
}

type Model struct {
	cfg   config.Config
	store *tasks.Store
	hist  *history.Store
	sess  *session.Store

	// Estado das sessões de trabalho (aba Sessões e tecla 'c').
	pickingService bool
	pickOptions    []string
	pickCursor     int
	pending        *pendingSession
	sessInfo       string // última mensagem de status das sessões
	progress       map[string]claude.Progress
	handles        map[string]*claude.Handle // execuções vivas, p/ cancelar

	tab     tab
	gl      *gitlab.Summary
	glErr   error
	ji      *jira.Summary
	jiErr   error
	loading int

	cursor int
	adding bool
	report bool
	input  textinput.Model

	filtering   bool
	filter      string
	filterInput textinput.Model

	detail        bool
	detailLoading bool
	detailTitle   string
	detailURL     string
	detailBody    string

	spin    spinner.Model
	vp      viewport.Model
	updated time.Time

	notified map[string]bool // lembretes já disparados nesta sessão

	width, height int
}

func New(cfg config.Config, store *tasks.Store) Model {
	ti := textinput.New()
	ti.Placeholder = "descrição (opcional no final: @today, @tomorrow, @2026-06-15; hora: @today 15:00)"
	fi := textinput.New()
	fi.Placeholder = "filtrar por chave, título ou status"
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	hist, _ := history.Load() // sem histórico ainda não é erro fatal
	sess, _ := session.Load() // mesmo com erro o store volta utilizável
	return Model{cfg: cfg, store: store, hist: hist, sess: sess, input: ti, filterInput: fi, spin: sp, vp: viewport.New(80, 20), loading: 2, notified: map[string]bool{}, progress: map[string]claude.Progress{}, handles: map[string]*claude.Handle{}}
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

// checkReminders dispara uma notificação para cada tarefa cujo horário de
// lembrete já chegou hoje e que ainda não foi notificada nesta sessão.
func (m Model) checkReminders() tea.Cmd {
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

// recordToday salva no histórico o que já foi entregue hoje. Roda a cada
// atualização; como Upsert substitui o registro do dia, os números só
// crescem ao longo do dia.
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
	cfg := m.cfg
	return tea.Batch(
		func() tea.Msg {
			s, err := gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token).Fetch()
			return glMsg{s, err}
		},
		func() tea.Msg {
			s, err := jira.New(cfg.Jira.URL, cfg.Jira.Auth, cfg.Jira.Email, cfg.Jira.Token, cfg.Jira.ComplexityField).Fetch()
			return jiMsg{s, err}
		},
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(20, msg.Width-10)
		m.vp.Width = max(36, m.width-8)
		m.vp.Height = max(5, m.height-5)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tickMsg:
		m.loading = 2
		return m, tea.Batch(m.fetchAll(), tick())

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
		m.loading--
		m.gl, m.glErr = msg.sum, msg.err
		m.updated = time.Now()
		m.recordToday()
		return m, nil

	case jiMsg:
		m.loading--
		m.ji, m.jiErr = msg.sum, msg.err
		m.updated = time.Now()
		m.recordToday()
		return m, nil

	case sessCreatedMsg:
		if msg.err != nil {
			m.sessInfo = errStyle.Render(msg.err.Error())
			return m, nil
		}
		m.sess.Add(msg.sess)
		m.sess.Save()
		m.sessInfo = okStyle.Render("sessão criada: " + msg.sess.Key + " em " + msg.sess.Worktree)
		return m, nil

	case sessTickMsg:
		m.pollProgress()
		if m.anyRunning() {
			return m, sessTick()
		}
		return m, nil

	case sessFinishedMsg:
		delete(m.handles, msg.id)
		if s := m.sess.Find(msg.id); s != nil {
			if s.Status == session.StatusCancelled {
				m.sess.Save()
				return m, nil
			}
			now := time.Now()
			s.Finished = &now
			s.Prompt = msg.prompt
			// O log final traz o session_id do Claude (para retomar) e o resumo.
			if s.LogFile != "" {
				if p, err := claude.ReadProgress(s.LogFile); err == nil {
					m.progress[s.ID] = p
					if p.SessionID != "" {
						s.ClaudeID = p.SessionID
					}
				}
			}
			if msg.err != nil {
				s.Status = session.StatusFailed
				s.Err = msg.err.Error()
			} else {
				s.Status = session.StatusDone
			}
			m.sess.Save()
			title := "wmonit — sessão " + s.Key
			body := "Claude terminou — revise o resultado"
			if msg.err != nil {
				body = "Claude falhou: " + s.Err
			}
			return m, notifyCmd(title, body)
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
		m.sess.Save()
		return m, nil

	case tea.KeyMsg:
		if m.adding {
			return m.updateAdding(msg)
		}
		if m.pickingService {
			return m.updatePickService(msg)
		}
		if m.filtering {
			return m.updateFilter(msg)
		}
		if m.detail {
			return m.updateDetail(msg)
		}
		if m.report {
			return m.updateReport(msg)
		}
		return m.updateKeys(msg)
	}
	return m, nil
}

func (m Model) updateAdding(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if v := strings.TrimSpace(m.input.Value()); v != "" {
			m.store.Add(v)
			m.store.Save()
		}
		m.adding = false
		m.input.Reset()
		return m, nil
	case "esc":
		m.adding = false
		m.input.Reset()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// updateFilter trata a digitação da busca; o filtro é aplicado ao vivo.
func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filtering = false
		m.filter = strings.TrimSpace(m.filterInput.Value())
		m.cursor = 0
		return m, nil
	case "esc":
		m.filtering = false
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
		m.report = false
		m.vp.GotoTop()
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "r":
		m.loading = 2
		return m, m.fetchAll()
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
		m.report = true
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
		m.loading = 2
		return m, m.fetchAll()
	}

	if m.tab == tabTarefas {
		switch msg.String() {
		case "a":
			m.adding = true
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
			m.filtering = true
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

func (m *Model) scrollToCursor() {
	m.vp.SetContent(m.content())
	if m.cursor < m.vp.YOffset {
		m.vp.SetYOffset(m.cursor)
	} else if m.cursor >= m.vp.YOffset+m.vp.Height {
		m.vp.SetYOffset(m.cursor - m.vp.Height + 1)
	}
}
