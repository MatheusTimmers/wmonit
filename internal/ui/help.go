package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/paths"
	"github.com/timmers/wmonit/internal/repocfg"
)

type helpTab int

const (
	helpKeys helpTab = iota
	helpConfig
)

type helpState struct {
	open bool
	tab  helpTab
	vp   viewport.Model
}

type configEditedMsg struct{ err error }

type keyHelp struct{ keys, desc string }

type keySection struct {
	title string
	items []keyHelp
}

var keySections = []keySection{
	{"Geral", []keyHelp{
		{"tab / shift+tab · 1-6", "trocar de aba"},
		{"g", "relatório do dia"},
		{"r", "atualizar GitLab e Jira"},
		{"?", "esta tela (atalhos e config)"},
		{"q · ctrl+c", "sair"},
	}},
	{"Workspaces (aba 6)", []keyHelp{
		{"n", "novo workspace livre (formulário)"},
		{"enter", "detalhes: links, git, MR, conversa e tela do Claude"},
		{"a", "abrir o workspace no herdr"},
		{"p", "mandar mensagem ao Claude do workspace"},
		{"x", "mandar esc ao Claude (interromper)"},
		{"i", "pedir a análise de pendências"},
		{"s", "entregar: push, MR no GitLab, issue para revisão"},
		{"C (duas vezes)", "concluir os que têm MR mergeado ou issue resolvida"},
		{"L", "ligar a uma issue ou MR"},
		{"u", "adotar um workspace de fora"},
		{"w", "reabrir um workspace fechado (retoma a conversa)"},
		{"v", "diff contra a branch base"},
		{"o", "abrir a issue/MR no navegador"},
		{"f / F", "concluir (F descarta mudanças)"},
		{"d / D", "concluir e tirar da lista"},
	}},
	{"GitLab e Jira (abas 3 e 4)", []keyHelp{
		{"j / k", "selecionar"},
		{"enter", "detalhes"},
		{"c", "criar workspace para o item"},
		{"o", "abrir no navegador"},
		{"/", "buscar"},
	}},
	{"Tarefas (aba 5)", []keyHelp{
		{"a", "adicionar (@today, @2026-06-15 15:00, !alta, !critica)"},
		{"espaço", "concluir"},
		{"d", "apagar"},
	}},
	{"Formulário de criação", []keyHelp{
		{"tab / enter", "próximo campo"},
		{"shift+tab", "campo anterior"},
		{"/", "filtrar a lista de repositórios"},
		{"← / →", "trocar o modelo"},
		{"alt+enter", "quebrar linha no texto"},
		{"ctrl+e", "escrever o texto no editor"},
		{"esc", "cancelar"},
	}},
	{"Mensagem e listas", []keyHelp{
		{"ctrl+d", "enviar a mensagem"},
		{"ctrl+e", "escrever no editor"},
		{"/", "filtrar a lista (L)"},
		{"pgup / pgdn · home / end", "paginar"},
		{"esc", "cancelar"},
	}},
	{"Detalhes", []keyHelp{
		{"j / k · pgup / pgdn", "rolar"},
		{"o", "abrir no navegador"},
		{"esc / q", "voltar"},
	}},
	{"No herdr", []keyHelp{
		{"prefix+m", "abrir o wmonit num popup (plugin)"},
		{"ctrl+clique num link", "issue do Jira ou MR → formulário da tarefa"},
	}},
	{"Linha de comando", []keyHelp{
		{"wmonit --demo", "dados fictícios"},
		{"wmonit --open <chave|link>", "abrir direto o formulário da tarefa"},
		{"wmonit herdr-plugin install", "ligar o plugin do herdr"},
		{"WMONIT_CONFIG=<arquivo>", "usar outro arquivo de config"},
	}},
}

var (
	helpBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("212")).Padding(0, 1)
	helpKey   = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	helpTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
)

func (m Model) helpSize() (int, int) {
	w := min(110, max(40, m.width-6))
	h := max(10, m.height-4)
	return w, h
}

func (m Model) openHelp() (tea.Model, tea.Cmd) {
	w, h := m.helpSize()
	m.help.open = true
	m.help.vp = viewport.New(viewport.WithWidth(w-4), viewport.WithHeight(h-6))
	m.help.vp.SetContent(m.helpContent())
	return m, nil
}

func (m Model) updateHelp(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "?":
		m.help.open = false
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "tab", "shift+tab", "left", "right", "h", "l":
		if m.help.tab == helpKeys {
			m.help.tab = helpConfig
		} else {
			m.help.tab = helpKeys
		}
		m.help.vp.SetContent(m.helpContent())
		m.help.vp.GotoTop()
		return m, nil
	case "1":
		m.help.tab = helpKeys
		m.help.vp.SetContent(m.helpContent())
		m.help.vp.GotoTop()
		return m, nil
	case "2":
		m.help.tab = helpConfig
		m.help.vp.SetContent(m.helpContent())
		m.help.vp.GotoTop()
		return m, nil
	case "e":
		if m.help.tab == helpConfig {
			return m, editConfigCmd(m.cfg)
		}
	}
	var cmd tea.Cmd
	m.help.vp, cmd = m.help.vp.Update(msg)
	return m, cmd
}

func editConfigCmd(cfg config.Config) tea.Cmd {
	path := config.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() tea.Msg { return configEditedMsg{err: err} }
	}
	return tea.ExecProcess(editorCommand(cfg, path), func(err error) tea.Msg { return configEditedMsg{err: err} })
}

func (m Model) reloadConfig(msg configEditedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.sui.info = errStyle.Render("editor: " + msg.err.Error())
		return m, nil
	}
	cfg, err := config.Load()
	if err != nil {
		m.sui.info = errStyle.Render(err.Error())
	} else {
		m.cfg = cfg
		m.sui.info = okStyle.Render("config recarregada")
	}
	if m.help.open {
		m.help.vp.SetContent(m.helpContent())
	}
	return m, nil
}

func (m Model) helpContent() string {
	w, _ := m.helpSize()
	if m.help.tab == helpConfig {
		return m.configText(w - 4)
	}
	return keysText()
}

func keysText() string {
	width := 0
	for _, s := range keySections {
		for _, it := range s.items {
			width = max(width, lipgloss.Width(it.keys))
		}
	}
	var b strings.Builder
	for i, s := range keySections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(section.Render(s.title) + "\n")
		for _, it := range s.items {
			pad := strings.Repeat(" ", width-lipgloss.Width(it.keys))
			b.WriteString("  " + helpKey.Render(it.keys) + pad + "  " + it.desc + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type kv struct{ k, v string }

func secret(s string) string {
	if s == "" {
		return dim.Render("(vazio)")
	}
	return okStyle.Render("definido")
}

func orEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return dim.Render("(vazio)")
	}
	return s
}

func onOff(v bool) string {
	if v {
		return okStyle.Render("ligado")
	}
	return dim.Render("desligado")
}

func (m Model) configText(width int) string {
	c := m.cfg
	path := config.Path()
	var b strings.Builder
	state := okStyle.Render("existe")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		state = warnStyle.Render("não existe — usando os padrões (e cria)")
	}
	b.WriteString(dim.Render("arquivo ") + truncate(path, max(20, width-lipgloss.Width(state)-12)) + "  " + state + "\n")
	b.WriteString(dim.Render("dados   ") + truncate(paths.DataDir(), max(20, width-10)) + "\n")

	group := func(title string, rows []kv) {
		b.WriteString("\n" + section.Render(title) + "\n")
		kw := 0
		for _, r := range rows {
			kw = max(kw, len(r.k))
		}
		for _, r := range rows {
			v := r.v
			if lipgloss.Width(v) > width-kw-4 {
				v = truncate(v, max(10, width-kw-5))
			}
			b.WriteString("  " + helpKey.Render(fmt.Sprintf("%-*s", kw, r.k)) + "  " + v + "\n")
		}
	}

	group("[gitlab]", []kv{
		{"url", orEmpty(c.GitLab.URL)},
		{"token", secret(c.GitLab.Token)},
		{"draft_mr", onOff(c.GitLab.DraftMR)},
	})
	group("[jira]", []kv{
		{"url", orEmpty(c.Jira.URL)},
		{"auth", c.Jira.Auth},
		{"email", orEmpty(c.Jira.Email)},
		{"token", secret(c.Jira.Token)},
		{"status_order", orEmpty(strings.Join(c.Jira.StatusOrder, ", "))},
		{"complexity_field", orEmpty(c.Jira.ComplexityField)},
		{"start_transition", orEmpty(c.Jira.StartTransition)},
		{"review_transition", orEmpty(c.Jira.ReviewTransition)},
	})
	group("[goals]", []kv{
		{"weekly_mrs", fmt.Sprint(c.Goals.WeeklyMRs)},
		{"weekly_issues", fmt.Sprint(c.Goals.WeeklyIssues)},
	})
	var services, prices []string
	for s := range c.Claude.Templates {
		services = append(services, s)
	}
	for p := range c.Claude.Prices {
		prices = append(prices, p)
	}
	sort.Strings(services)
	sort.Strings(prices)
	group("[claude]", []kv{
		{"sources_dir", c.Claude.SourcesDir},
		{"worktrees_dir", c.Claude.WorktreesDir},
		{"bin", c.Claude.Bin},
		{"branch_prefix", orEmpty(c.Claude.BranchPrefix)},
		{"model", orEmpty(c.Claude.Model)},
		{"models", strings.Join(c.Claude.Models, ", ")},
		{"permission_mode", orEmpty(c.Claude.PermissionMode)},
		{"template", orEmpty(firstLine(c.Claude.Template))},
		{"templates", orEmpty(strings.Join(services, ", "))},
		{"prices", orEmpty(strings.Join(prices, ", "))},
	})
	group("[herdr]", []kv{
		{"bin", c.Herdr.Bin},
		{"session", orEmpty(c.Herdr.Session)},
		{"editor", c.Herdr.Editor},
		{"events", onOff(c.Herdr.EventsOn()) + dim.Render(" · "+m.eventsState())},
		{"metadata", onOff(c.Herdr.MetadataOn())},
		{"notify", orEmpty(c.Herdr.Notify)},
	})

	b.WriteString("\n" + section.Render("Repositórios com "+repocfg.File) + "\n")
	var withCfg []string
	if svcs, err := os.ReadDir(c.Claude.SourcesDir); err == nil {
		for _, e := range svcs {
			if _, err := os.Stat(filepath.Join(c.Claude.SourcesDir, e.Name(), repocfg.File)); err == nil {
				withCfg = append(withCfg, e.Name())
			}
		}
	}
	if len(withCfg) == 0 {
		b.WriteString("  " + dim.Render("nenhum em "+c.Claude.SourcesDir) + "\n")
	} else {
		b.WriteString("  " + strings.Join(withCfg, ", ") + "\n")
	}
	b.WriteString("\n" + dim.Render("Variáveis WMONIT_GITLAB_URL/TOKEN e WMONIT_JIRA_URL/TOKEN/EMAIL têm precedência sobre o arquivo."))
	return b.String()
}

func (m Model) eventsState() string {
	switch {
	case !m.sui.snap.Online():
		return "herdr offline"
	case m.live.eventsOn:
		return "conectado"
	}
	return "polling"
}

func (m Model) helpView() string {
	w, h := m.helpSize()
	tabs := []string{"Atalhos", "Config"}
	var head []string
	for i, t := range tabs {
		label := fmt.Sprintf("[%d] %s", i+1, t)
		if helpTab(i) == m.help.tab {
			head = append(head, tabActive.Render(label))
		} else {
			head = append(head, tabInactive.Render(label))
		}
	}
	title := helpTitle.Render("wmonit") + "  " + lipgloss.JoinHorizontal(lipgloss.Top, head...)
	foot := "tab troca · j/k rola · esc fecha"
	if m.help.tab == helpConfig {
		foot = "e edita a config (recarrega ao sair) · " + foot
	}
	if p := m.help.vp.ScrollPercent(); p < 1 {
		foot += fmt.Sprintf(" · %d%%", int(p*100))
	}
	body := title + "\n\n" + m.help.vp.View() + "\n\n" + dim.Render(foot)
	return helpBox.Width(w).Height(h).Render(body)
}

func overlay(base, top string, width, height int) string {
	x := max(0, (width-lipgloss.Width(top))/2)
	y := max(0, (height-lipgloss.Height(top))/2)
	canvas := lipgloss.NewCanvas(max(width, lipgloss.Width(base)), max(height, lipgloss.Height(base)))
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(top).X(x).Y(y).Z(1),
	))
	return canvas.Render()
}
