package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/tasks"
)

type tab int

const (
	tabHoje tab = iota
	tabDesempenho
	tabGitLab
	tabJira
	tabTarefas
	numTabs
)

const refreshEvery = 5 * time.Minute

type glMsg struct {
	sum *gitlab.Summary
	err error
}

type jiMsg struct {
	sum *jira.Summary
	err error
}

type tickMsg time.Time

type Model struct {
	cfg   config.Config
	store *tasks.Store

	tab     tab
	gl      *gitlab.Summary
	glErr   error
	ji      *jira.Summary
	jiErr   error
	loading int

	cursor  int
	adding  bool
	input   textinput.Model
	spin    spinner.Model
	vp      viewport.Model
	updated time.Time

	width, height int
}

func New(cfg config.Config, store *tasks.Store) Model {
	ti := textinput.New()
	ti.Placeholder = "descrição (opcional: @hoje, @amanha ou @2026-06-15 no final)"
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	return Model{cfg: cfg, store: store, input: ti, spin: sp, vp: viewport.New(80, 20), loading: 2}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchAll(), m.spin.Tick, tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
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

	case glMsg:
		m.loading--
		m.gl, m.glErr = msg.sum, msg.err
		m.updated = time.Now()
		return m, nil

	case jiMsg:
		m.loading--
		m.ji, m.jiErr = msg.sum, msg.err
		m.updated = time.Now()
		return m, nil

	case tea.KeyMsg:
		if m.adding {
			return m.updateAdding(msg)
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

func (m Model) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "1", "2", "3", "4", "5":
		m.tab = tab(msg.String()[0] - '1')
		m.cursor = 0
		m.vp.GotoTop()
		return m, nil
	case "tab", "l", "right":
		m.tab, m.cursor = (m.tab+1)%numTabs, 0
		m.vp.GotoTop()
		return m, nil
	case "shift+tab", "h", "left":
		m.tab, m.cursor = (m.tab+numTabs-1)%numTabs, 0
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

	// Nas demais abas o viewport cuida da rolagem (j/k, setas, pgup/pgdn…).
	// O conteúdo precisa estar carregado nele para a altura ser conhecida.
	m.vp.SetContent(m.content())
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *Model) scrollToCursor() {
	m.vp.SetContent(m.content())
	if m.cursor < m.vp.YOffset {
		m.vp.SetYOffset(m.cursor)
	} else if m.cursor >= m.vp.YOffset+m.vp.Height {
		m.vp.SetYOffset(m.cursor - m.vp.Height + 1)
	}
}
