package ui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/gitlab"
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

// interactiveDoneMsg chega quando o claude interativo (tecla 't') fechou
// e a TUI voltou.
type interactiveDoneMsg struct {
	id  string
	err error
}

// openInteractive suspende a TUI e abre o Claude Code interativo no
// worktree da sessão, retomando o contexto quando houver.
func (m Model) openInteractive(s *session.Session) (tea.Model, tea.Cmd) {
	var args []string
	if s.ClaudeID != "" {
		args = append(args, "--resume", s.ClaudeID)
	}
	cmd := exec.Command(m.cfg.Claude.Bin, args...)
	cmd.Dir = s.Worktree
	id := s.ID
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return interactiveDoneMsg{id: id, err: err}
	})
}

// sessTickMsg dispara a releitura dos logs das sessões em execução.
type sessTickMsg time.Time

const sessPollEvery = 2 * time.Second

func sessTick() tea.Cmd {
	return tea.Tick(sessPollEvery, func(t time.Time) tea.Msg { return sessTickMsg(t) })
}

// anyRunning informa se há sessão em execução (mantém o polling vivo).
func (m Model) anyRunning() bool {
	for _, s := range m.sess.Sessions {
		if s.Status == session.StatusRunning {
			return true
		}
	}
	return false
}

// pollProgress relê os logs das sessões rodando e atualiza o progresso.
func (m *Model) pollProgress() {
	for _, s := range m.sess.Sessions {
		if s.Status != session.StatusRunning || s.LogFile == "" {
			continue
		}
		if p, err := claude.ReadProgress(s.LogFile); err == nil {
			m.progress[s.ID] = p
		}
	}
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

// pendingSession guarda o que falta decidir (explicação e serviço) entre
// apertar 'c' e o início do pipeline.
type pendingSession struct {
	sess         session.Session
	createBranch bool
	guess        string // serviço deduzido da #TAG/projeto, se houver
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

// startSession inicia o fluxo da tecla 'c': monta a sessão, muda para a
// aba Sessões e abre o textbox de explicação da tarefa.
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
	m.pending = &pendingSession{sess: sess, createBranch: create, guess: guess}
	m.tab = tabSessoes
	m.cursor = 0
	m.filter = ""
	m.sessInfo = ""
	m.describing = true
	m.descInput.Reset()
	m.descInput.Focus()
	m.vp.GotoTop()
	return m, textarea.Blink
}

// updateDescribe trata o textbox de explicação da tarefa.
func (m Model) updateDescribe(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.describing = false
		m.pending = nil
		m.descInput.Blur()
		m.sessInfo = dim.Render("sessão cancelada")
		return m, nil
	case "ctrl+d", "ctrl+s":
		m.describing = false
		m.descInput.Blur()
		if m.pending == nil {
			return m, nil
		}
		m.pending.sess.UserNote = strings.TrimSpace(m.descInput.Value())
		return m.continueSession()
	}
	var cmd tea.Cmd
	m.descInput, cmd = m.descInput.Update(msg)
	return m, cmd
}

// viewDescribe desenha o textbox de explicação na aba Sessões.
func (m Model) viewDescribe() string {
	var b strings.Builder
	key, title := "", ""
	if m.pending != nil {
		key, title = m.pending.sess.Key, m.pending.sess.Title
	}
	b.WriteString(section.Render("📝 Nova sessão — "+key) + " " + title + "\n\n")
	b.WriteString(dim.Render("Explique a tarefa para o Claude; a descrição e os comentários da issue/MR entram junto no contexto.") + "\n\n")
	b.WriteString(m.descInput.View() + "\n\n")
	b.WriteString(dim.Render("ctrl+d inicia o pipeline (plan → dev → review) · esc cancela"))
	return b.String()
}

// continueSession segue após a explicação: deduz o serviço ou abre a
// lista de escolha; com serviço definido, cria o worktree.
func (m Model) continueSession() (tea.Model, tea.Cmd) {
	p := m.pending
	services, err := worktree.DetectServices(m.cfg.Claude.SourcesDir)
	if err != nil {
		m.pending = nil
		m.sessInfo = errStyle.Render("lendo " + m.cfg.Claude.SourcesDir + ": " + err.Error())
		return m, nil
	}
	if len(services) == 0 {
		m.pending = nil
		m.sessInfo = errStyle.Render("nenhum serviço (repo git) em " + m.cfg.Claude.SourcesDir)
		return m, nil
	}
	for _, s := range services {
		if strings.EqualFold(s, p.guess) {
			p.sess.Service = s
			m.pending = nil
			return m.launchCreate(p.sess, p.createBranch)
		}
	}
	// Sem dedução: o usuário escolhe na lista.
	m.pickingService = true
	m.pickOptions = services
	m.pickCursor = 0
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
		// pending/failed: roda do zero; done com ClaudeID: retoma (--resume).
		if s := m.selectedSession(); s != nil &&
			(s.Status == session.StatusPending || s.Status == session.StatusFailed || s.Status == session.StatusDone) {
			return m.startRun(s)
		}
	case "t":
		// Terminal interativo: suspende a TUI e abre o claude no worktree.
		if s := m.selectedSession(); s != nil && s.Active() && s.Status != session.StatusRunning {
			return m.openInteractive(s)
		}
	case "v":
		if s := m.selectedSession(); s != nil && s.Active() {
			return m.openDiff(*s)
		}
	case "e":
		if s := m.selectedSession(); s != nil && s.Active() {
			return m, openEditorCmd(s.Worktree)
		}
	case "f":
		if s := m.selectedSession(); s != nil && (s.Status == session.StatusDone || s.Status == session.StatusFailed || s.Status == session.StatusPending) {
			return m, m.finishSessionCmd(*s, false)
		}
	case "F":
		if s := m.selectedSession(); s != nil && s.Active() && s.Status != session.StatusRunning {
			return m, m.finishSessionCmd(*s, true)
		}
	case "x":
		if s := m.selectedSession(); s != nil {
			return m.cancelSession(s)
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

// openDiff mostra o diff do worktree no painel de detalhes.
func (m Model) openDiff(s session.Session) (tea.Model, tea.Cmd) {
	m.detail = true
	m.detailLoading = true
	m.detailBody = ""
	m.detailTitle = "diff " + s.Key
	m.detailURL = s.URL
	m.vp.GotoTop()
	return m, func() tea.Msg {
		d, err := worktree.Diff(s.Worktree)
		if err != nil {
			return detailMsg{err: err}
		}
		if strings.TrimSpace(d) == "" {
			d = dim.Render("(sem mudanças no worktree)")
		}
		return detailMsg{body: d}
	}
}

// finishSessionCmd conclui a sessão: remove o worktree (com guarda de
// mudanças não commitadas, a menos que force) e marca como concluída.
func (m Model) finishSessionCmd(s session.Session, force bool) tea.Cmd {
	return func() tea.Msg {
		if s.Worktree != "" {
			if err := worktree.Remove(s.Repo, s.Worktree, force); err != nil {
				if !force {
					return sessActionMsg{id: s.ID, err: fmt.Errorf("%w (commits do Claude ficam na branch; use F para descartar o resto)", err)}
				}
				return sessActionMsg{id: s.ID, err: err}
			}
		}
		return sessActionMsg{id: s.ID, status: session.StatusCompleted}
	}
}

// cancelSession mata a execução em andamento (se houver) e marca a
// sessão como cancelada; o worktree fica para inspeção (remova com d/D).
func (m Model) cancelSession(s *session.Session) (tea.Model, tea.Cmd) {
	if h, ok := m.handles[s.ID]; ok {
		h.Kill()
		delete(m.handles, s.ID)
	}
	if s.Status == session.StatusRunning || s.Status == session.StatusPending {
		s.Status = session.StatusCancelled
		now := time.Now()
		s.Finished = &now
		m.sess.Save()
		m.sessInfo = warnStyle.Render("sessão " + s.Key + " cancelada — worktree mantido (d remove)")
	}
	return m, nil
}

var jiraKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-\d+$`)

// mrRef aponta o MR da sessão (ou ligado à issue pela #TAG), com o que já
// está em memória — os comentários são buscados depois, fora da UI.
type mrRef struct {
	projectID, iid int
	ref, desc      string
}

// mrFor acha o MR aberto da sessão: pela ref (sessão de MR) ou pela #TAG
// no título (sessão de issue).
func (m Model) mrFor(key string) *mrRef {
	if m.gl == nil {
		return nil
	}
	for _, mr := range m.gl.OpenMRs {
		if shortRef(mr) == key || mr.JiraKey() == key {
			return &mrRef{projectID: mr.ProjectID, iid: mr.IID, ref: shortRef(mr), desc: mr.Description}
		}
	}
	return nil
}

// fetchTaskContext compõe o contexto completo da tarefa (Jira + GitLab).
// Roda fora da UI; erros de rede degradam para o contexto parcial.
func fetchTaskContext(cfg config.Config, sess session.Session, mr *mrRef) claude.TaskContext {
	ctx := claude.TaskContext{
		Key:       sess.Key,
		Title:     sess.Title,
		URL:       sess.URL,
		UserNote:  sess.UserNote,
		Template:  cfg.Claude.Templates[sess.Service],
		HasBranch: !jiraKeyPattern.MatchString(sess.Key), // sessão de MR usa branch existente
	}
	var notes []gitlab.Note
	if mr != nil {
		notes, _ = gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token).MRNotes(mr.projectID, mr.iid)
	}
	if jiraKeyPattern.MatchString(sess.Key) {
		// Sessão de issue: descrição/comentários do Jira; o MR ligado vira contexto extra.
		if det, err := jira.New(cfg.Jira.URL, cfg.Jira.Auth, cfg.Jira.Email, cfg.Jira.Token, cfg.Jira.ComplexityField).IssueDetail(sess.Key); err == nil {
			ctx.Description = det.Description
			for _, c := range det.Comments {
				ctx.Comments = append(ctx.Comments, c.Author+": "+c.Body)
			}
		}
		if mr != nil {
			var b strings.Builder
			b.WriteString(mr.ref)
			if mr.desc != "" {
				b.WriteString("\n" + mr.desc)
			}
			for _, n := range notes {
				if !n.System {
					b.WriteString("\n- " + n.Author.Name + ": " + strings.TrimSpace(n.Body))
				}
			}
			ctx.MRInfo = b.String()
		}
	} else if mr != nil {
		// Sessão do próprio MR: descrição e comentários do MR são a tarefa.
		ctx.Description = mr.desc
		for _, n := range notes {
			if !n.System {
				ctx.Comments = append(ctx.Comments, n.Author.Name+": "+strings.TrimSpace(n.Body))
			}
		}
	}
	return ctx
}

// startRun inicia o pipeline de agents (plan → dev → review) ou, se a
// sessão já tem contexto do Claude, retoma de onde parou.
func (m Model) startRun(s *session.Session) (tea.Model, tea.Cmd) {
	if s.ClaudeID != "" {
		return m.runPhase(s, s.Phase, true)
	}
	return m.runPhase(s, session.PhasePlan, false)
}

// runPhase dispara uma fase do pipeline em background. resume retoma a
// sessão anterior do Claude em vez de montar o prompt da fase.
func (m Model) runPhase(s *session.Session, phase string, resume bool) (tea.Model, tea.Cmd) {
	s.Phase = phase
	s.Status = session.StatusRunning
	s.Err = ""
	s.Finished = nil
	s.LogFile = filepath.Join(session.LogDir(), s.ID+"-"+phase+".jsonl")
	m.sess.Save()
	m.sessInfo = dim.Render("rodando " + phaseLabel(phase) + " de " + s.Key + "…")

	cfg := m.cfg
	sess := *s
	mr := m.mrFor(sess.Key) // dados do GitLab já em memória; rede fica no closure
	h := &claude.Handle{}
	m.handles[s.ID] = h
	run := func() tea.Msg {
		var prompt, resumeID string
		if resume {
			prompt = "Continue a tarefa de onde parou, seguindo as instruções anteriores. Revise o estado atual do repositório antes de prosseguir."
			resumeID = sess.ClaudeID
		} else {
			ctx := fetchTaskContext(cfg, sess, mr)
			switch phase {
			case session.PhaseDev:
				prompt = claude.DevPrompt(ctx)
			case session.PhaseReview:
				prompt = claude.ReviewPrompt(ctx)
			default:
				prompt = claude.PlanPrompt(ctx)
			}
		}
		err := claude.Run(cfg.Claude.Bin, sess.Worktree, prompt, sess.LogFile, cfg.Claude.Models[phase], resumeID, h)
		return sessFinishedMsg{id: sess.ID, prompt: prompt, err: err}
	}
	return m, tea.Batch(run, sessTick())
}

// phaseLabel descreve a fase do pipeline para a UI.
func phaseLabel(p string) string {
	switch p {
	case session.PhasePlan:
		return "plano 1/3"
	case session.PhaseDev:
		return "dev 2/3"
	case session.PhaseReview:
		return "review 3/3"
	}
	return ""
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
		if pl := phaseLabel(s.Phase); pl != "" {
			meta += " · " + pl
		}
		b.WriteString(dim.Render(meta) + "\n")
		if s.Err != "" {
			b.WriteString("    " + errStyle.Render(s.Err) + "\n")
		}
		if p, ok := m.progress[s.ID]; ok && s.Status == session.StatusRunning {
			b.WriteString(dim.Render(fmt.Sprintf("    %s %s · %d turnos", m.spin.View(), phaseLabel(s.Phase), p.Turns)))
			if len(p.Tools) > 0 {
				b.WriteString(dim.Render(" · " + strings.Join(p.Tools, " → ")))
			}
			b.WriteString("\n")
			if p.LastText != "" {
				b.WriteString("    " + dim.Render(truncate(p.LastText, 120)) + "\n")
			}
		}
		if s.Status == session.StatusDone {
			if p, ok := m.progress[s.ID]; ok && p.Result != "" {
				b.WriteString("    " + okStyle.Render(truncate(p.Result, 200)) + "\n")
			}
		}
	}
	return b.String()
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
