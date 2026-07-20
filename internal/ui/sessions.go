package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/gitlab"
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
	if m.sui.ticking {
		return nil
	}
	m.sui.ticking = true
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
			m.sui.progress[s.ID] = p
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
			Key:    mr.ShortRef(),
			Title:  mr.ShortTitle(),
			URL:    mr.WebURL,
			Branch: mr.SourceBranch,
			Kind:   session.KindMR,
			Mode:   mode,
		}
		guess = mr.Project()
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
	if m.fetch.gl != nil {
		for _, mr := range m.fetch.gl.OpenMRs {
			if mr.JiraKey() == is.Key {
				guess = mr.Project()
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

// startSession inicia o fluxo da tecla 'c': monta a sessão, muda para a
// aba Sessões e abre o textbox de explicação da tarefa.
func (m Model) startSession(it *focusItem) (tea.Model, tea.Cmd) {
	sess, guess, create := m.newSessionFromItem(it)
	if sess.Branch == "" {
		m.sui.sessInfo = errStyle.Render("MR sem branch de origem — atualize (r) e tente de novo")
		return m, nil
	}
	if m.sess.HasActiveFor(sess.Key) {
		m.sui.sessInfo = warnStyle.Render("já existe sessão ativa para " + sess.Key)
		return m, nil
	}
	// Falha rápida: detectar os serviços antes do usuário investir tempo
	// digitando a explicação.
	services, err := worktree.DetectServices(m.cfg.Claude.SourcesDir)
	if err != nil {
		m.sui.sessInfo = errStyle.Render("lendo " + m.cfg.Claude.SourcesDir + ": " + err.Error())
		return m, nil
	}
	if len(services) == 0 {
		m.sui.sessInfo = errStyle.Render("nenhum serviço (repo git) em " + m.cfg.Claude.SourcesDir)
		return m, nil
	}
	m.sui.pending = &pendingSession{sess: sess, createBranch: create, guess: guess, services: services}
	m.tab = tabSessoes
	m.cursor = 0
	m.filter = ""
	m.sui.sessInfo = ""
	m.mode = modeDescribing
	m.sui.descInput.Reset()
	m.sui.descInput.Focus()
	m.vp.GotoTop()
	return m, textarea.Blink
}

// updateDescribe trata o textbox de explicação da tarefa.
func (m Model) updateDescribe(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.sui.pending = nil
		m.sui.descInput.Blur()
		m.sui.sessInfo = dim.Render("sessão cancelada")
		return m, nil
	case "ctrl+d", "ctrl+s":
		m.mode = modeNormal
		m.sui.descInput.Blur()
		if m.sui.pending == nil {
			return m, nil
		}
		m.sui.pending.sess.UserNote = strings.TrimSpace(m.sui.descInput.Value())
		return m.continueSession()
	case "ctrl+r":
		// Alterna entre desenvolvimento e revisão sem perder a explicação.
		if m.sui.pending != nil {
			if m.sui.pending.sess.IsReview() {
				m.sui.pending.sess.Mode = session.ModeImplement
			} else {
				m.sui.pending.sess.Mode = session.ModeReview
			}
		}
		return m, nil
	case "ctrl+e":
		// Edita a explicação no editor externo (nvim): leva o texto atual e
		// traz de volta o que for salvo. A TUI fica suspensa enquanto isso.
		return m, editNoteCmd(m.cfg, m.sui.descInput.Value())
	}
	var cmd tea.Cmd
	m.sui.descInput, cmd = m.sui.descInput.Update(msg)
	return m, cmd
}

// viewDescribe desenha o textbox de explicação na aba Sessões.
func (m Model) viewDescribe() string {
	var b strings.Builder
	key, title, review := "", "", false
	if m.sui.pending != nil {
		key, title = m.sui.pending.sess.Key, m.sui.pending.sess.Title
		review = m.sui.pending.sess.IsReview()
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
	b.WriteString(m.sui.descInput.View() + "\n\n")
	b.WriteString(dim.Render(start + " · ctrl+e edita no editor · ctrl+r troca o modo · esc cancela"))
	return b.String()
}

// continueSession segue após a explicação: deduz o serviço ou abre a
// lista de escolha; com serviço definido, cria o worktree.
func (m Model) continueSession() (tea.Model, tea.Cmd) {
	p := m.sui.pending
	for _, s := range p.services {
		if strings.EqualFold(s, p.guess) {
			p.sess.Service = s
			m.sui.pending = nil
			return m.launchCreate(p.sess, p.createBranch)
		}
	}
	// Sem dedução: o usuário escolhe na lista.
	m.mode = modePickingService
	m.sui.pickOptions = p.services
	m.sui.pickCursor = 0
	return m, nil
}

// launchCreate dispara a criação do worktree em background.
func (m Model) launchCreate(sess session.Session, createBranch bool) (tea.Model, tea.Cmd) {
	sess.ID = session.NewID(sess.Key)
	sess.Repo = filepath.Join(m.cfg.Claude.SourcesDir, sess.Service)
	sess.Worktree = filepath.Join(m.cfg.Claude.WorktreesDir, sess.ID)
	sess.Status = session.StatusPending
	sess.Created = time.Now()
	m.sui.sessInfo = dim.Render("criando worktree de " + sess.Key + " em " + sess.Service + "…")
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
		if m.sui.pending != nil {
			m.mode = modeDescribing
			m.sui.descInput.Focus()
			return m, textarea.Blink
		}
		m.mode = modeNormal
		return m, nil
	case "j", "down":
		if m.sui.pickCursor < len(m.sui.pickOptions)-1 {
			m.sui.pickCursor++
		}
		return m, nil
	case "k", "up":
		if m.sui.pickCursor > 0 {
			m.sui.pickCursor--
		}
		return m, nil
	case "enter":
		p := m.sui.pending
		m.mode = modeNormal
		m.sui.pending = nil
		if p == nil {
			return m, nil
		}
		p.sess.Service = m.sui.pickOptions[m.sui.pickCursor]
		return m.launchCreate(p.sess, p.createBranch)
	}
	return m, nil
}

// viewPickService desenha a lista de serviços para a sessão pendente.
func (m Model) viewPickService() string {
	var b strings.Builder
	key := ""
	if m.sui.pending != nil {
		key = m.sui.pending.sess.Key
	}
	b.WriteString(section.Render("🛠 Em qual serviço trabalhar "+key+"?") + "\n\n")
	for i, s := range m.sui.pickOptions {
		cursor := "  "
		if i == m.sui.pickCursor {
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
			return m.startRun(s.ID)
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
			return m.cancelSession(s.ID)
		}
	case "d":
		if s := m.selectedSession(); s != nil {
			if s.Status == session.StatusRunning {
				m.sui.sessInfo = warnStyle.Render("sessão em execução — cancele com x antes de remover")
				return m, nil
			}
			return m, m.removeSessionCmd(*s, false)
		}
	case "D":
		if s := m.selectedSession(); s != nil {
			if s.Status == session.StatusRunning {
				m.sui.sessInfo = warnStyle.Render("sessão em execução — cancele com x antes de remover")
				return m, nil
			}
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
	m.detail.loading = true
	m.detail.body = ""
	m.detail.title = "sessão " + s.Key
	m.detail.url = s.URL
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
	m.detail.loading = true
	m.detail.body = ""
	m.detail.title = "diff " + s.Key
	m.detail.url = s.URL
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
func (m Model) cancelSession(id string) (tea.Model, tea.Cmd) {
	m.runner.Cancel(id)
	var key string
	m.sess.Update(id, func(s *session.Session) {
		if s.Status == session.StatusRunning || s.Status == session.StatusPending {
			s.Status = session.StatusCancelled
			now := time.Now()
			s.Finished = &now
			key = s.Key
		}
	})
	if key != "" {
		m.save(m.sess.Save())
		m.sui.sessInfo = warnStyle.Render("sessão " + key + " cancelada — worktree mantido (d remove)")
	}
	return m, nil
}

// startRun é a tecla 's': o que ela faz depende de onde a sessão está no
// pipeline com gates.
//
//	pendente → inicia o pipeline (plan)
//	aguardando aprovação → aprova e dispara a próxima fase
//	falhou → tenta a fase de novo (retomando a conversa, se houver)
//	pronta → ciclo de correção: retoma o dev com o veredito do review
func (m Model) startRun(id string) (tea.Model, tea.Cmd) {
	sess, ok := m.sess.Get(id)
	if !ok {
		return m, nil
	}
	act, ok := sess.Plan()
	if !ok {
		switch {
		case sess.IsReview():
			m.sui.sessInfo = dim.Render("revisão concluída — enter vê o resultado · t pergunta mais · f conclui")
		case sess.Status == session.StatusDone:
			m.sui.sessInfo = warnStyle.Render("sem conversa para retomar — use 't' para abrir o Claude interativo")
		}
		return m, nil
	}
	return m.runPhase(id, act)
}

// runPhase dispara a Action do pipeline em background; a orquestração fica
// no pipeline.Runner. O LogFile é resolvido aqui e gravado na sessão antes
// da execução para o polling conseguir ler o progresso enquanto roda.
func (m Model) runPhase(id string, act session.Action) (tea.Model, tea.Cmd) {
	var sess session.Session
	m.sess.Update(id, func(s *session.Session) {
		s.Phase = act.Phase
		s.Status = session.StatusRunning
		s.Err = ""
		s.Finished = nil
		s.LogFile = filepath.Join(m.sess.LogDir(), s.ID+"-"+act.Phase+".jsonl")
		sess = *s
	})
	m.save(m.sess.Save())
	delete(m.sui.progress, id) // o painel não deve mostrar a fase anterior
	m.sui.sessInfo = dim.Render("rodando " + phaseLabel(act.Phase, sess.Mode) + " de " + sess.Key + "…")

	runner := m.runner
	mrs := m.myMRs() // dados do GitLab já em memória; rede fica no closure
	run := func() tea.Msg {
		prompt, err := runner.RunPhase(sess, act, mrs)
		return sessFinishedMsg{id: sess.ID, prompt: prompt, err: err}
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
	if m.sui.sessInfo != "" {
		write(m.sui.sessInfo + "\n\n")
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
		if p, ok := m.sui.progress[s.ID]; ok && s.Status == session.StatusRunning {
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
			} else if p, ok := m.sui.progress[s.ID]; ok && p.Result != "" {
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
