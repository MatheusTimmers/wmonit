package ui

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/session"
)

const (
	actionSend   = "send"
	actionCreate = "create"
)

type createForm struct {
	form    *huh.Form
	pending pendingSession
	repo    string
	branch  string
	mode    string
	model   string
	note    string
	action  string
}

func (m Model) openCreateForm(p *pendingSession) (tea.Model, tea.Cmd) {
	f := &createForm{pending: *p, branch: p.sess.Branch, mode: p.sess.Mode, action: actionSend}
	if f.mode == "" {
		f.mode = session.ModeImplement
	}
	f.repo = p.services[0]
	for _, s := range p.services {
		if strings.EqualFold(s, p.guess) {
			f.repo = s
		}
	}

	var fields []huh.Field
	title, desc := "🆕 Novo workspace", "Cada workspace abre as abas terminal, claude e nvim na pasta do repositório."
	if p.sess.Key != "" {
		title = "📝 Novo workspace — " + p.sess.Key + " " + p.sess.Title
		desc = "A descrição e os comentários da issue/MR entram no contexto (" + noteEscape(claude.TaskFile) + ")."
	}
	fields = append(fields, huh.NewNote().Title(title).Description(desc))

	repos := make([]huh.Option[string], len(p.services))
	for i, s := range p.services {
		repos[i] = huh.NewOption(s, s)
	}
	fields = append(fields, huh.NewSelect[string]().
		Title("Repositório").
		Description("/ filtra").
		Options(repos...).
		Height(min(len(repos)+2, 10)).
		Value(&f.repo))

	if p.createBranch {
		fields = append(fields, huh.NewInput().
			Title("Branch").
			Value(&f.branch).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" || strings.ContainsAny(s, " \t~^:?*[\\") {
					return errors.New("nome de branch inválido")
				}
				return nil
			}))
	}
	if p.sess.Kind == session.KindMR {
		fields = append(fields, huh.NewSelect[string]().
			Title("Modo").
			Options(
				huh.NewOption("desenvolvimento (trabalhar no MR)", session.ModeImplement),
				huh.NewOption("revisão (só revisa e reporta)", session.ModeReview),
			).
			Value(&f.mode))
	}

	models := []huh.Option[string]{huh.NewOption("padrão ("+m.defaultModelLabel()+")", "")}
	for _, md := range m.cfg.Claude.Models {
		models = append(models, huh.NewOption(md, md))
	}
	fields = append(fields, huh.NewSelect[string]().Title("Modelo do Claude").Options(models...).Inline(true).Value(&f.model))

	noteTitle := "O que fazer"
	if p.sess.Kind == session.KindFree {
		noteTitle += " (a primeira linha vira o título; vazio = só abre)"
	}
	fields = append(fields, huh.NewText().
		Title(noteTitle).
		Description("alt+enter quebra linha · ctrl+e abre no editor").
		Lines(6).
		Value(&f.note))

	fields = append(fields, huh.NewSelect[string]().
		Title("Ao criar").
		Options(
			huh.NewOption("criar e mandar a tarefa ao Claude", actionSend),
			huh.NewOption("só criar (o Claude espera instruções)", actionCreate),
		).
		Value(&f.action))

	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "cancela"))
	f.form = huh.NewForm(huh.NewGroup(fields...)).
		WithKeyMap(km).
		WithTheme(huh.ThemeFunc(huh.ThemeCatppuccin)).
		WithShowHelp(true).
		WithWidth(max(40, m.vp.Width()-2))

	m.sui.form = f
	m.sui.pending = nil
	m.tab = tabSessoes
	m.filter = ""
	m.sui.info = ""
	m.mode = modeForm
	m.vp.GotoTop()
	return m, f.form.Init()
}

func (m Model) defaultModelLabel() string {
	if m.cfg.Claude.Model != "" {
		return m.cfg.Claude.Model
	}
	return "settings do Claude"
}

func (m Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := m.sui.form
	if f == nil {
		m.mode = modeNormal
		return m, nil
	}
	next, cmd := f.form.Update(msg)
	if hf, ok := next.(*huh.Form); ok {
		f.form = hf
	}
	switch f.form.State {
	case huh.StateAborted:
		m.sui.form = nil
		m.mode = modeNormal
		m.sui.info = dim.Render("cancelado")
		return m, nil
	case huh.StateCompleted:
		return m.submitForm(f)
	}
	return m, cmd
}

func (m Model) submitForm(f *createForm) (tea.Model, tea.Cmd) {
	m.sui.form = nil
	m.mode = modeNormal
	p := f.pending
	p.sess.Service = f.repo
	p.sess.UserNote = strings.TrimSpace(f.note)
	p.model = f.model
	if p.createBranch {
		p.sess.Branch = strings.TrimSpace(f.branch)
	}
	if p.sess.Kind == session.KindMR {
		p.sess.Mode = f.mode
	}
	return m.launchCreate(p, f.action == actionSend)
}

func (m Model) viewForm() string {
	if m.sui.form == nil {
		return ""
	}
	return m.sui.form.form.View()
}

var noteEscaper = strings.NewReplacer("_", "\\_", "*", "\\*", "`", "\\`")

func noteEscape(s string) string { return noteEscaper.Replace(s) }
