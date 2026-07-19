package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// ctx (quando a fase montou o contexto da tarefa) é cacheado para as fases
// seguintes não repetirem as buscas no Jira/GitLab.
type sessFinishedMsg struct {
	id     string
	prompt string
	ctx    *claude.TaskContext
	err    error
}

// interactiveDoneMsg chega quando o claude interativo (tecla 't') fechou
// e a TUI voltou.
type interactiveDoneMsg struct {
	id  string
	err error
}

// openInteractive suspende a TUI e abre o Claude Code interativo no
// worktree da sessão. Retoma a conversa do DEV quando houver — é a que
// pode editar código; a última fase costuma ser o review, que foi
// instruído a só analisar. ClaudeID solto cobre sessões antigas.
func (m Model) openInteractive(s *session.Session) (tea.Model, tea.Cmd) {
	resume := s.ClaudeIDs[session.PhaseDev]
	if resume == "" {
		resume = s.ClaudeID
	}
	var args []string
	if resume != "" {
		args = append(args, "--resume", resume)
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

// maybeTick inicia a cadeia de polling só se ela não estiver viva — sem
// isso cada fase/sessão empilharia mais uma cadeia de ticks de 2s.
func (m *Model) maybeTick() tea.Cmd {
	if m.ticking {
		return nil
	}
	m.ticking = true
	return sessTick()
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
	guess        string   // serviço deduzido da #TAG/projeto, se houver
	services     []string // serviços detectados no momento do 'c'
}

// newSessionFromItem monta a sessão a partir do item selecionado e tenta
// adivinhar o serviço; devolve também se a branch deve ser criada.
func (m Model) newSessionFromItem(it *focusItem) (sess session.Session, guess string, createBranch bool) {
	if it.mr != nil {
		mr := *it.mr
		// MR de outra pessoa (você é só revisor) → sessão de revisão; o seu
		// próprio MR → pipeline de desenvolvimento. O usuário pode trocar.
		mode := session.ModeImplement
		if !m.ownsMR(mr) {
			mode = session.ModeReview
		}
		sess = session.Session{
			Key:    shortRef(mr),
			Title:  mr.ShortTitle(),
			URL:    mr.WebURL,
			Branch: mr.SourceBranch,
			Kind:   session.KindMR,
			Mode:   mode,
		}
		guess = projectOf(mr.References.Full)
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

// ownsMR informa se o MR é de autoria do usuário (consta entre os meus) —
// o que decide o modo padrão da sessão: desenvolvimento no seu, revisão no
// dos outros.
func (m Model) ownsMR(mr gitlab.MR) bool {
	for _, x := range m.myMRs() {
		if x.ProjectID == mr.ProjectID && x.IID == mr.IID {
			return true
		}
	}
	return false
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
	// Falha rápida: detectar os serviços antes do usuário investir tempo
	// digitando a explicação.
	services, err := worktree.DetectServices(m.cfg.Claude.SourcesDir)
	if err != nil {
		m.sessInfo = errStyle.Render("lendo " + m.cfg.Claude.SourcesDir + ": " + err.Error())
		return m, nil
	}
	if len(services) == 0 {
		m.sessInfo = errStyle.Render("nenhum serviço (repo git) em " + m.cfg.Claude.SourcesDir)
		return m, nil
	}
	m.pending = &pendingSession{sess: sess, createBranch: create, guess: guess, services: services}
	m.tab = tabSessoes
	m.cursor = 0
	m.filter = ""
	m.sessInfo = ""
	m.mode = modeDescribing
	m.descInput.Reset()
	m.descInput.Focus()
	m.vp.GotoTop()
	return m, textarea.Blink
}

// updateDescribe trata o textbox de explicação da tarefa.
func (m Model) updateDescribe(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.pending = nil
		m.descInput.Blur()
		m.sessInfo = dim.Render("sessão cancelada")
		return m, nil
	case "ctrl+d", "ctrl+s":
		m.mode = modeNormal
		m.descInput.Blur()
		if m.pending == nil {
			return m, nil
		}
		m.pending.sess.UserNote = strings.TrimSpace(m.descInput.Value())
		return m.continueSession()
	case "ctrl+r":
		// Alterna entre desenvolvimento e revisão sem perder a explicação.
		if m.pending != nil {
			if m.pending.sess.IsReview() {
				m.pending.sess.Mode = session.ModeImplement
			} else {
				m.pending.sess.Mode = session.ModeReview
			}
		}
		return m, nil
	case "ctrl+e":
		// Edita a explicação no editor externo (nvim): leva o texto atual e
		// traz de volta o que for salvo. A TUI fica suspensa enquanto isso.
		return m, editNoteCmd(m.cfg, m.descInput.Value())
	}
	var cmd tea.Cmd
	m.descInput, cmd = m.descInput.Update(msg)
	return m, cmd
}

// viewDescribe desenha o textbox de explicação na aba Sessões.
func (m Model) viewDescribe() string {
	var b strings.Builder
	key, title, review := "", "", false
	if m.pending != nil {
		key, title = m.pending.sess.Key, m.pending.sess.Title
		review = m.pending.sess.IsReview()
	}
	head, mode, expl, start := "📝 Nova sessão — ",
		okStyle.Render("desenvolvimento (plan → dev → review)"),
		"Explique a tarefa para o Claude; a descrição e os comentários da issue/MR entram junto no contexto.",
		"ctrl+d inicia o pipeline (plan → dev → review)"
	if review {
		head, mode, expl, start = "🔎 Nova revisão — ",
			warnStyle.Render("revisão (só revisa o MR e reporta)"),
			"Diga o que olhar com atenção na revisão (opcional); a descrição e os comentários do MR entram no contexto.",
			"ctrl+d inicia a revisão"
	}
	b.WriteString(section.Render(head+key) + " " + title + "\n\n")
	b.WriteString(dim.Render("modo: ") + mode + dim.Render("  (ctrl+r troca)") + "\n")
	b.WriteString(dim.Render(expl) + "\n\n")
	b.WriteString(m.descInput.View() + "\n\n")
	b.WriteString(dim.Render(start + " · ctrl+e edita no editor · ctrl+r troca o modo · esc cancela"))
	return b.String()
}

// continueSession segue após a explicação: deduz o serviço ou abre a
// lista de escolha; com serviço definido, cria o worktree.
func (m Model) continueSession() (tea.Model, tea.Cmd) {
	p := m.pending
	for _, s := range p.services {
		if strings.EqualFold(s, p.guess) {
			p.sess.Service = s
			m.pending = nil
			return m.launchCreate(p.sess, p.createBranch)
		}
	}
	// Sem dedução: o usuário escolhe na lista.
	m.mode = modePickingService
	m.pickOptions = p.services
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
		if err == nil {
			// O plano do pipeline fica fora do git: não suja o status
			// (que travaria o 'f') nem vai parar num commit.
			_ = worktree.ExcludeFiles(sess.Worktree, claude.PlanFile)
		}
		return sessCreatedMsg{sess: sess, err: err}
	}
}

// updatePickService trata a lista de escolha de serviço.
func (m Model) updatePickService(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		// Volta para o textbox sem perder a explicação digitada.
		if m.pending != nil {
			m.mode = modeDescribing
			m.descInput.Focus()
			return m, textarea.Blink
		}
		m.mode = modeNormal
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
		m.mode = modeNormal
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
		m.scrollToSession()
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		m.scrollToSession()
	case "o":
		if s := m.selectedSession(); s != nil && s.URL != "" {
			return m, openURLCmd(s.URL)
		}
	case "s":
		// pendente: inicia · aguardando: aprova a fase · falhou: tenta de
		// novo · pronta: ciclo de correção (dev com o veredito do review).
		if s := m.selectedSession(); s != nil &&
			(s.Status == session.StatusPending || s.Status == session.StatusWaiting ||
				s.Status == session.StatusFailed || s.Status == session.StatusDone) {
			return m.startRun(s)
		}
	case "enter", "p":
		// Resultado das fases (plano, resumo do dev, veredito) sem sair do TUI.
		if s := m.selectedSession(); s != nil {
			return m.openSessionDetail(*s)
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
			return m, openEditorCmd(m.cfg, s.Worktree)
		}
	case "f":
		if s := m.selectedSession(); s != nil &&
			(s.Status == session.StatusDone || s.Status == session.StatusWaiting ||
				s.Status == session.StatusFailed || s.Status == session.StatusPending) {
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

// openSessionDetail mostra o estado do pipeline no painel de detalhes:
// veredito/resumo de cada fase e o conteúdo do plano (WMONIT_PLAN.md).
func (m Model) openSessionDetail(s session.Session) (tea.Model, tea.Cmd) {
	m.mode = modeDetail
	m.detailLoading = true
	m.detailBody = ""
	m.detailTitle = "sessão " + s.Key
	m.detailURL = s.URL
	m.vp.GotoTop()
	wrap := m.wrapText
	return m, func() tea.Msg {
		var b strings.Builder
		b.WriteString(statusLabel(s.Status) + "  " + warnStyle.Render(s.Key) + " " + s.Title + "\n")
		meta := s.Service + " · " + s.Branch
		if s.IsReview() {
			meta += " · revisão de MR"
		}
		if pl := phaseLabel(s.Phase, s.Mode); pl != "" {
			meta += " · fase " + pl
		}
		b.WriteString(dim.Render(meta) + "\n\n")
		if s.Err != "" {
			b.WriteString(errStyle.Render(s.Err) + "\n\n")
		}
		if n := strings.TrimSpace(s.UserNote); n != "" {
			b.WriteString(section.Render("📝 Sua explicação") + "\n" + wrap(n) + "\n\n")
		}
		titles := map[string]string{
			session.PhasePlan:   "🧭 Plano (resumo)",
			session.PhaseDev:    "🔨 Desenvolvimento",
			session.PhaseReview: "🔎 Review",
		}
		for _, ph := range []string{session.PhasePlan, session.PhaseDev, session.PhaseReview} {
			if r := strings.TrimSpace(s.Results[ph]); r != "" {
				b.WriteString(section.Render(titles[ph]) + "\n" + wrap(r) + "\n\n")
			}
		}
		// O plano completo gravado pelo agente, se ainda existir no worktree.
		if s.Worktree != "" {
			if data, err := os.ReadFile(filepath.Join(s.Worktree, claude.PlanFile)); err == nil {
				b.WriteString(section.Render("📋 "+claude.PlanFile) + "\n" + wrap(strings.TrimSpace(string(data))) + "\n")
			}
		}
		body := b.String()
		if strings.TrimSpace(body) == "" {
			body = dim.Render("(sem resultados ainda)")
		}
		return detailMsg{body: body}
	}
}

// openDiff mostra o diff do worktree no painel de detalhes.
func (m Model) openDiff(s session.Session) (tea.Model, tea.Cmd) {
	m.mode = modeDetail
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

// mrRef aponta o MR da sessão (ou ligado à issue pela #TAG), com o que já
// está em memória — os comentários são buscados depois, fora da UI.
type mrRef struct {
	projectID, iid int
	ref, desc      string
}

func newMRRef(mr gitlab.MR) *mrRef {
	return &mrRef{projectID: mr.ProjectID, iid: mr.IID, ref: shortRef(mr), desc: mr.Description}
}

// mrFor acha o MR da sessão: pela ref exata (sessão de MR, mesmo já
// mergeado) ou pela #TAG no título (sessão de issue), preferindo o MR do
// mesmo serviço quando a issue tem MRs em mais de um repositório.
func (m Model) mrFor(sess session.Session) *mrRef {
	if m.gl == nil {
		return nil
	}
	all := m.myMRs()
	for i := range all {
		if shortRef(all[i]) == sess.Key {
			return newMRRef(all[i])
		}
	}
	if !sess.IsIssue() {
		return nil
	}
	var fallback *mrRef
	for i := range all {
		if all[i].JiraKey() != sess.Key {
			continue
		}
		if strings.EqualFold(projectOf(all[i].References.Full), sess.Service) {
			return newMRRef(all[i])
		}
		if fallback == nil {
			fallback = newMRRef(all[i])
		}
	}
	return fallback
}

// noteLines formata os comentários (não-sistema) de um MR.
func noteLines(notes []gitlab.Note) []string {
	var out []string
	for _, n := range notes {
		if !n.System {
			out = append(out, n.Author.Name+": "+strings.TrimSpace(n.Body))
		}
	}
	return out
}

// jiraDetail busca os detalhes da issue, tolerando client nulo (testes/demo).
func jiraDetail(ji *jira.Client, key string) (*jira.IssueDetail, error) {
	if ji == nil {
		return nil, fmt.Errorf("jira não configurado")
	}
	return ji.IssueDetail(context.Background(), key)
}

// fetchTaskContext compõe o contexto completo da tarefa (Jira + GitLab),
// reusando os clients do Model. Roda fora da UI; erros de rede degradam
// para o contexto parcial.
func fetchTaskContext(gl *gitlab.Client, ji *jira.Client, cfg config.Config, sess session.Session, mr *mrRef) claude.TaskContext {
	isIssue := sess.IsIssue()
	ctx := claude.TaskContext{
		Key:       sess.Key,
		Title:     sess.Title,
		URL:       sess.URL,
		UserNote:  sess.UserNote,
		Template:  cfg.Claude.Templates[sess.Service],
		HasBranch: !isIssue, // sessão de MR usa branch existente
	}
	var notes []string
	if mr != nil && gl != nil {
		raw, _ := gl.MRNotes(context.Background(), mr.projectID, mr.iid)
		notes = noteLines(raw)
	}
	if isIssue {
		// Sessão de issue: descrição/comentários do Jira; o MR ligado vira contexto extra.
		if det, err := jiraDetail(ji, sess.Key); err == nil {
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
				b.WriteString("\n- " + n)
			}
			ctx.MRInfo = b.String()
		}
	} else if mr != nil {
		// Sessão do próprio MR: descrição e comentários do MR são a tarefa.
		ctx.Description = mr.desc
		ctx.Comments = notes
	}
	return ctx
}

// startRun é a tecla 's': o que ela faz depende de onde a sessão está no
// pipeline com gates.
//
//	pendente → inicia o pipeline (plan)
//	aguardando aprovação → aprova e dispara a próxima fase
//	falhou → tenta a fase de novo (retomando a conversa, se houver)
//	pronta → ciclo de correção: retoma o dev com o veredito do review
func (m Model) startRun(s *session.Session) (tea.Model, tea.Cmd) {
	act, ok := s.Plan()
	if !ok {
		switch {
		case s.IsReview():
			m.sessInfo = dim.Render("revisão concluída — enter vê o resultado · t pergunta mais · f conclui")
		case s.Status == session.StatusDone:
			m.sessInfo = warnStyle.Render("sem conversa para retomar — use 't' para abrir o Claude interativo")
		}
		return m, nil
	}
	return m.runPhase(s, act)
}

// runPhase dispara a Action do pipeline em background. Quando act tem
// ResumeID, retoma aquela conversa do Claude em vez de começar do zero (com
// prompt de correção quando act.UseFix).
func (m Model) runPhase(s *session.Session, act session.Action) (tea.Model, tea.Cmd) {
	s.Phase = act.Phase
	s.Status = session.StatusRunning
	s.Err = ""
	s.Finished = nil
	s.LogFile = filepath.Join(session.LogDir(), s.ID+"-"+act.Phase+".jsonl")
	m.sess.Save()
	delete(m.progress, s.ID) // o painel não deve mostrar a fase anterior
	m.sessInfo = dim.Render("rodando " + phaseLabel(act.Phase, s.Mode) + " de " + s.Key + "…")

	cfg := m.cfg
	sess := *s
	gl, ji := m.glClient, m.jiClient
	verdict := s.Results[session.PhaseReview]
	cached := m.taskCtx[s.ID] // contexto já buscado numa fase anterior
	var mr *mrRef
	if cached == nil {
		mr = m.mrFor(sess) // dados do GitLab já em memória; rede fica no closure
	}
	opts := claude.Opts{
		Bin:            cfg.Claude.Bin,
		Dir:            sess.Worktree,
		LogFile:        sess.LogFile,
		Model:          cfg.Claude.Models[act.Phase],
		PermissionMode: cfg.Claude.PermissionMode,
	}
	h := &claude.Handle{}
	m.handles[s.ID] = h
	run := func() tea.Msg {
		if act.ResumeID != "" {
			if act.UseFix {
				opts.Prompt = claude.FixPrompt(verdict)
			} else {
				opts.Prompt = claude.ResumePrompt()
			}
			opts.Resume = act.ResumeID
			err := claude.Run(opts, h)
			return sessFinishedMsg{id: sess.ID, prompt: opts.Prompt, err: err}
		}
		ctx := cached
		if ctx == nil {
			c := fetchTaskContext(gl, ji, cfg, sess, mr)
			ctx = &c
		}
		switch act.Phase {
		case session.PhaseDev:
			opts.Prompt = claude.DevPrompt(*ctx)
		case session.PhaseReview:
			if sess.IsReview() {
				opts.Prompt = claude.ReviewMRPrompt(*ctx)
			} else {
				opts.Prompt = claude.ReviewPrompt(*ctx)
			}
		default:
			opts.Prompt = claude.PlanPrompt(*ctx)
		}
		err := claude.Run(opts, h)
		return sessFinishedMsg{id: sess.ID, prompt: opts.Prompt, ctx: ctx, err: err}
	}
	return m, tea.Batch(run, m.maybeTick())
}

// phaseLabel descreve a fase para a UI. No modo revisão há só uma fase, sem
// a numeração "x/3" do pipeline de desenvolvimento.
func phaseLabel(p, mode string) string {
	if mode == session.ModeReview {
		if p == session.PhaseReview {
			return "revisão"
		}
		return ""
	}
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
	case session.StatusWaiting:
		return warnStyle.Render("⏸ aguardando aprovação")
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

// viewSessoes desenha a aba Sessões. Devolve também a linha onde começa a
// sessão selecionada — cada sessão ocupa 2+ linhas, então o índice do
// cursor não serve para a rolagem.
func (m Model) viewSessoes() (string, int) {
	var b strings.Builder
	sel, line := 0, 0
	write := func(s string) {
		b.WriteString(s)
		line += strings.Count(s, "\n")
	}
	if m.sessInfo != "" {
		write(m.sessInfo + "\n\n")
	}
	if len(m.sess.Sessions) == 0 {
		write(dim.Render("nenhuma sessão — selecione uma issue (Jira) ou MR (GitLab) e pressione 'c'") + "\n")
		return b.String(), 0
	}
	for i, s := range m.sess.Sessions {
		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("▌ ")
			sel = line
		}
		write(cursor + statusLabel(s.Status) + "  " + warnStyle.Render(s.Key) + " " + s.Title + "\n")
		meta := "    " + s.Service + " · " + s.Branch + " · " + s.Created.Format("02/01 15:04")
		if pl := phaseLabel(s.Phase, s.Mode); pl != "" {
			meta += " · " + pl
		}
		write(dim.Render(meta) + "\n")
		if s.Err != "" {
			write("    " + errStyle.Render(s.Err) + "\n")
		}
		if p, ok := m.progress[s.ID]; ok && s.Status == session.StatusRunning {
			write(dim.Render(fmt.Sprintf("    %s %s · %d turnos", m.spin.View(), phaseLabel(s.Phase, s.Mode), p.Turns)))
			if len(p.Tools) > 0 {
				write(dim.Render(" · " + strings.Join(p.Tools, " → ")))
			}
			write("\n")
			if p.LastText != "" {
				write("    " + dim.Render(truncate(p.LastText, 120)) + "\n")
			}
		}
		if s.Status == session.StatusWaiting {
			if r := s.Results[s.Phase]; r != "" {
				write("    " + truncate(r, 160) + "\n")
			}
			next := session.NextPhase(s.Phase)
			write("    " + dim.Render("enter vê o resultado · s aprova e roda "+phaseLabel(next, s.Mode)) + "\n")
		}
		if s.Status == session.StatusDone {
			if r := s.Results[session.PhaseReview]; r != "" {
				write("    " + okStyle.Render(truncate(r, 200)) + "\n")
			} else if p, ok := m.progress[s.ID]; ok && p.Result != "" {
				write("    " + okStyle.Render(truncate(p.Result, 200)) + "\n")
			}
			hint := "enter vê o veredito · s corrige os ajustes · f conclui"
			if s.IsReview() {
				hint = "enter vê a revisão · t pergunta mais · f conclui"
			}
			write("    " + dim.Render(hint) + "\n")
		}
	}
	return b.String(), sel
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
