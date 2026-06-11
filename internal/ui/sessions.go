package ui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

// sessFinishedMsg chega quando a execução headless do Claude terminou.
type sessFinishedMsg struct {
	id     string
	prompt string
	err    error
}

// sessCreatedMsg chega quando o worktree de uma nova sessão ficou pronto
// (ou falhou); a sessão só entra no store se err == nil.
type sessCreatedMsg struct {
	sess session.Session
	err  error
}

// sessActionMsg é o resultado de uma ação sobre uma sessão existente
// (remover worktree, etc.). id identifica a sessão; remove indica que ela
// deve sair da lista.
type sessActionMsg struct {
	id     string
	remove bool
	status session.Status
	err    error
}

// pendingSession guarda o que falta decidir (serviço) entre apertar 'c'
// e a escolha na lista de serviços.
type pendingSession struct {
	sess         session.Session
	createBranch bool
}

// newSessionFromItem monta a sessão a partir do item selecionado e tenta
// adivinhar o serviço; devolve também se a branch deve ser criada.
func (m Model) newSessionFromItem(it *focusItem) (sess session.Session, guess string, createBranch bool) {
	if it.mr != nil {
		mr := *it.mr
		sess = session.Session{
			Key:    shortRef(mr),
			Title:  mr.ShortTitle(),
			URL:    mr.WebURL,
			Branch: mr.SourceBranch,
		}
		guess = projectOf(mr.References.Full)
		return sess, guess, false
	}
	is := *it.issue
	sess = session.Session{
		Key:    is.Key,
		Title:  is.Summary,
		URL:    strings.TrimRight(m.cfg.Jira.URL, "/") + "/browse/" + is.Key,
		Branch: "feature/" + is.Key,
	}
	// Issue: o serviço vem de um MR já ligado pela #TAG, se houver.
	if m.gl != nil {
		for _, mr := range m.gl.OpenMRs {
			if mr.JiraKey() == is.Key {
				guess = projectOf(mr.References.Full)
				break
			}
		}
	}
	return sess, guess, true
}

// projectOf extrai o nome do projeto da ref completa do MR
// ("Roteamento/hades!9470" → "hades").
func projectOf(fullRef string) string {
	ref := fullRef
	if i := strings.Index(ref, "!"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	return ref
}

// startSession inicia o fluxo da tecla 'c': monta a sessão e, se o
// serviço não puder ser deduzido, abre a lista de escolha.
func (m Model) startSession(it *focusItem) (tea.Model, tea.Cmd) {
	sess, guess, create := m.newSessionFromItem(it)
	if sess.Branch == "" {
		m.sessInfo = errStyle.Render("MR sem branch de origem — atualize (r) e tente de novo")
		return m, nil
	}
	if m.sess.HasActiveFor(sess.Key) {
		m.sessInfo = warnStyle.Render("já existe sessão ativa para " + sess.Key)
		return m, nil
	}
	services, err := worktree.DetectServices(m.cfg.Claude.SourcesDir)
	if err != nil {
		m.sessInfo = errStyle.Render("lendo " + m.cfg.Claude.SourcesDir + ": " + err.Error())
		return m, nil
	}
	if len(services) == 0 {
		m.sessInfo = errStyle.Render("nenhum serviço (repo git) em " + m.cfg.Claude.SourcesDir)
		return m, nil
	}
	for _, s := range services {
		if strings.EqualFold(s, guess) {
			sess.Service = s
			return m.launchCreate(sess, create)
		}
	}
	// Sem dedução: o usuário escolhe na lista.
	m.pickingService = true
	m.pickOptions = services
	m.pickCursor = 0
	m.pending = &pendingSession{sess: sess, createBranch: create}
	return m, nil
}

// launchCreate dispara a criação do worktree em background.
func (m Model) launchCreate(sess session.Session, createBranch bool) (tea.Model, tea.Cmd) {
	sess.ID = session.NewID(sess.Key)
	sess.Repo = filepath.Join(m.cfg.Claude.SourcesDir, sess.Service)
	sess.Worktree = filepath.Join(m.cfg.Claude.WorktreesDir, sess.ID)
	sess.Status = session.StatusPending
	sess.Created = time.Now()
	m.sessInfo = dim.Render("criando worktree de " + sess.Key + " em " + sess.Service + "…")
	return m, func() tea.Msg {
		err := worktree.Add(sess.Repo, sess.Worktree, sess.Branch, createBranch)
		return sessCreatedMsg{sess: sess, err: err}
	}
}

// updatePickService trata a lista de escolha de serviço.
func (m Model) updatePickService(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.pickingService = false
		m.pending = nil
		return m, nil
	case "j", "down":
		if m.pickCursor < len(m.pickOptions)-1 {
			m.pickCursor++
		}
		return m, nil
	case "k", "up":
		if m.pickCursor > 0 {
			m.pickCursor--
		}
		return m, nil
	case "enter":
		p := m.pending
		m.pickingService = false
		m.pending = nil
		if p == nil {
			return m, nil
		}
		p.sess.Service = m.pickOptions[m.pickCursor]
		return m.launchCreate(p.sess, p.createBranch)
	}
	return m, nil
}

// viewPickService desenha a lista de serviços para a sessão pendente.
func (m Model) viewPickService() string {
	var b strings.Builder
	key := ""
	if m.pending != nil {
		key = m.pending.sess.Key
	}
	b.WriteString(section.Render("🛠 Em qual serviço trabalhar "+key+"?") + "\n\n")
	for i, s := range m.pickOptions {
		cursor := "  "
		if i == m.pickCursor {
			cursor = cursorStyle.Render("▌ ")
		}
		b.WriteString(cursor + s + "\n")
	}
	return b.String()
}

// sessionKeys trata as teclas da aba Sessões.
func (m Model) sessionKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.cursor < len(m.sess.Sessions)-1 {
			m.cursor++
		}
		m.scrollToCursor()
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		m.scrollToCursor()
	case "o":
		if s := m.selectedSession(); s != nil && s.URL != "" {
			return m, openURLCmd(s.URL)
		}
	case "s", "enter":
		if s := m.selectedSession(); s != nil && (s.Status == session.StatusPending || s.Status == session.StatusFailed) {
			return m.startRun(s)
		}
	case "d":
		if s := m.selectedSession(); s != nil {
			return m, m.removeSessionCmd(*s, false)
		}
	case "D":
		if s := m.selectedSession(); s != nil {
			return m, m.removeSessionCmd(*s, true)
		}
	}
	return m, nil
}

// selectedSession devolve a sessão sob o cursor, ou nil.
func (m Model) selectedSession() *session.Session {
	if m.cursor < 0 || m.cursor >= len(m.sess.Sessions) {
		return nil
	}
	return &m.sess.Sessions[m.cursor]
}

// removeSessionCmd apaga a sessão; se o worktree ainda existir, remove
// antes (com guarda de mudanças, a menos que force).
func (m Model) removeSessionCmd(s session.Session, force bool) tea.Cmd {
	return func() tea.Msg {
		if s.Active() && s.Worktree != "" {
			if err := worktree.Remove(s.Repo, s.Worktree, force); err != nil {
				if !force {
					return sessActionMsg{id: s.ID, err: fmt.Errorf("%w (use D para forçar)", err)}
				}
				return sessActionMsg{id: s.ID, err: err}
			}
		}
		return sessActionMsg{id: s.ID, remove: true}
	}
}

var jiraKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-\d+$`)

// startRun marca a sessão como rodando e dispara a execução headless do
// Claude no worktree. A descrição da task é buscada dentro do comando
// (rede não pode bloquear a UI).
func (m Model) startRun(s *session.Session) (tea.Model, tea.Cmd) {
	s.Status = session.StatusRunning
	s.Err = ""
	s.Finished = nil
	s.LogFile = filepath.Join(session.LogDir(), s.ID+".jsonl")
	m.sess.Save()
	m.sessInfo = dim.Render("rodando claude em " + s.Worktree + "…")

	cfg := m.cfg
	sess := *s
	// Para MR, a descrição já está em memória; para issue ela é buscada
	// no Jira dentro do comando.
	desc := ""
	if !jiraKeyPattern.MatchString(sess.Key) && m.gl != nil {
		for _, mr := range m.gl.OpenMRs {
			if shortRef(mr) == sess.Key {
				desc = mr.Description
				break
			}
		}
	}
	run := func() tea.Msg {
		d := desc
		if jiraKeyPattern.MatchString(sess.Key) {
			det, err := jira.New(cfg.Jira.URL, cfg.Jira.Auth, cfg.Jira.Email, cfg.Jira.Token, cfg.Jira.ComplexityField).IssueDetail(sess.Key)
			if err == nil {
				d = det.Description
			}
		}
		prompt := claude.BuildPrompt(sess.Key, sess.Title, sess.URL, d, cfg.Claude.Templates[sess.Service])
		err := claude.Run(cfg.Claude.Bin, sess.Worktree, prompt, sess.LogFile, sess.ClaudeID)
		return sessFinishedMsg{id: sess.ID, prompt: prompt, err: err}
	}
	return m, run
}

func statusLabel(s session.Status) string {
	switch s {
	case session.StatusPending:
		return warnStyle.Render("● pendente")
	case session.StatusRunning:
		return okStyle.Render("▶ rodando")
	case session.StatusDone:
		return okStyle.Render("✔ pronta")
	case session.StatusFailed:
		return errStyle.Render("✖ falhou")
	case session.StatusCompleted:
		return dim.Render("✓ concluída")
	case session.StatusCancelled:
		return dim.Render("∅ cancelada")
	}
	return string(s)
}

// viewSessoes desenha a aba Sessões.
func (m Model) viewSessoes() string {
	var b strings.Builder
	if m.sessInfo != "" {
		b.WriteString(m.sessInfo + "\n\n")
	}
	if len(m.sess.Sessions) == 0 {
		b.WriteString(dim.Render("nenhuma sessão — selecione uma issue (Jira) ou MR (GitLab) e pressione 'c'") + "\n")
		return b.String()
	}
	for i, s := range m.sess.Sessions {
		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("▌ ")
		}
		line := cursor + statusLabel(s.Status) + "  " + warnStyle.Render(s.Key) + " " + s.Title
		b.WriteString(line + "\n")
		meta := "    " + s.Service + " · " + s.Branch + " · " + s.Created.Format("02/01 15:04")
		b.WriteString(dim.Render(meta) + "\n")
		if s.Err != "" {
			b.WriteString("    " + errStyle.Render(s.Err) + "\n")
		}
	}
	return b.String()
}
