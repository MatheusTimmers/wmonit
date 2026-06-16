package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/tasks"
)

var (
	tabActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Padding(0, 1)
	tabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Padding(0, 1)
	panel       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
	section     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	statusHead  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
	dim         = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	barStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	barStyle2   = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	barStyle3   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	alertStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("214"))
	critStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
)

// taskPrioBadge destaca a prioridade da tarefa; média/sem prioridade não
// ganham selo (para não poluir a lista).
func taskPrioBadge(p string) string {
	switch p {
	case tasks.PriorityCritical:
		return " " + critStyle.Render("🔴 CRÍTICA")
	case tasks.PriorityHigh:
		return " " + warnStyle.Render("⬆ alta")
	case tasks.PriorityLow:
		return " " + dim.Render("⬇ baixa")
	}
	return ""
}

var tabNames = []string{"Hoje", "Desempenho", "GitLab", "Jira", "Tarefas", "Sessões"}

var ptMonths = [...]string{"jan", "fev", "mar", "abr", "mai", "jun", "jul", "ago", "set", "out", "nov", "dez"}

func ptMonth(t time.Time) string { return ptMonths[t.Month()-1] }

// startOfWeek devolve a segunda-feira (00:00) da semana de t.
func startOfWeek(t time.Time) time.Time {
	wd := (int(t.Weekday()) + 6) % 7 // segunda = 0
	d := t.AddDate(0, 0, -wd)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
}

// mergedIn filtra MRs mergeados em [start, end).
func mergedIn(mrs []gitlab.MR, start, end time.Time) []gitlab.MR {
	var out []gitlab.MR
	for _, mr := range mrs {
		t := mr.MergedTime()
		if !t.Before(start) && t.Before(end) {
			out = append(out, mr)
		}
	}
	return out
}

// createdIn filtra MRs criados em [start, end).
func createdIn(mrs []gitlab.MR, start, end time.Time) []gitlab.MR {
	var out []gitlab.MR
	for _, mr := range mrs {
		if !mr.CreatedAt.Before(start) && mr.CreatedAt.Before(end) {
			out = append(out, mr)
		}
	}
	return out
}

// myMRs devolve os MRs do usuário deduplicados — cacheado no glMsg para
// não realocar a cada render; o fallback cobre Models montados em testes.
func (m Model) myMRs() []gitlab.MR {
	if m.mine != nil {
		return m.mine
	}
	if m.gl == nil {
		return nil
	}
	return m.gl.Mine()
}

// resolvedIn filtra issues resolvidas entre as datas (inclusivas, YYYY-MM-DD).
func resolvedIn(issues []jira.Issue, startDate, endDate string) []jira.Issue {
	var out []jira.Issue
	for _, is := range issues {
		if is.Resolved >= startDate && is.Resolved <= endDate {
			out = append(out, is)
		}
	}
	return out
}

// avgLeadDays calcula a média de dias entre criação e resolução.
func avgLeadDays(issues []jira.Issue) float64 {
	sum, n := 0.0, 0
	for _, is := range issues {
		c, err1 := time.Parse("2006-01-02", is.Created)
		r, err2 := time.Parse("2006-01-02", is.Resolved)
		if err1 != nil || err2 != nil {
			continue
		}
		sum += r.Sub(c).Hours() / 24
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// countBy agrega por uma chave e devolve "chave n · chave n…" em ordem
// decrescente de contagem.
func countBy[T any](items []T, key func(T) string) string {
	counts := map[string]int{}
	var order []string
	for _, it := range items {
		k := key(it)
		if k == "" {
			continue
		}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	if len(order) == 0 {
		return ""
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	var parts []string
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}
	return strings.Join(parts, " · ")
}

func fmtNum(p float64) string {
	if p == math.Trunc(p) {
		return fmt.Sprintf("%.0f", p)
	}
	return fmt.Sprintf("%.1f", p)
}

func (m Model) View() string {
	var b strings.Builder

	var topRow string
	if m.mode == modeDetail {
		topRow = tabActive.Render("🔍 "+m.detailTitle) + tabInactive.Render("esc volta · o abre no navegador")
	} else if m.mode == modeReport {
		topRow = tabActive.Render("📋 Relatório do dia") + tabInactive.Render(m.reportSummary())
	} else {
		var tabsRow []string
		for i, name := range tabNames {
			label := fmt.Sprintf("[%d] %s", i+1, name)
			if tab(i) == m.tab {
				tabsRow = append(tabsRow, tabActive.Render(label))
			} else {
				tabsRow = append(tabsRow, tabInactive.Render(label))
			}
		}
		topRow = lipgloss.JoinHorizontal(lipgloss.Top, tabsRow...)
	}
	if a := m.alerts(); a != "" {
		topRow = lipgloss.JoinHorizontal(lipgloss.Top, topRow, "  ", a)
	}
	b.WriteString(topRow)
	b.WriteString("\n")

	vp := m.vp
	vp.SetContent(m.content())
	b.WriteString(panel.Width(max(40, m.width-4)).Render(vp.View()))
	b.WriteString("\n")
	b.WriteString(m.footer(vp))
	return b.String()
}

// content gera o texto da aba ativa; é usado tanto para desenhar quanto
// para alimentar o viewport do modelo (sem isso a rolagem não tem altura).
func (m Model) content() string {
	switch m.mode {
	case modeDescribing:
		return m.viewDescribe()
	case modePickingService:
		return m.viewPickService()
	case modeDetail:
		if m.detailLoading {
			return dim.Render("carregando…")
		}
		return m.detailBody
	case modeReport:
		return m.viewReport()
	}
	switch m.tab {
	case tabHoje:
		return m.viewHoje()
	case tabDesempenho:
		return m.viewDesempenho()
	case tabGitLab, tabJira:
		s, _ := renderRows(m.currentRows(), m.cursor)
		return s
	case tabTarefas:
		return m.viewTarefas()
	case tabSessoes:
		s, _ := m.viewSessoes()
		return s
	}
	return ""
}

// alerts resume os itens que pedem atenção — vencendo hoje/atrasados e
// reviews aguardando você — num realce no topo. Devolve "" se não há nada.
func (m Model) alerts() string {
	today := time.Now().Format("2006-01-02")
	due := 0
	if m.ji != nil {
		for _, is := range m.ji.Open {
			if is.Due != "" && is.Due <= today {
				due++
			}
		}
	}
	for _, t := range m.store.Tasks {
		if !t.Done && t.Due != "" && t.Due <= today {
			due++
		}
	}
	reviews := 0
	if m.gl != nil {
		reviews = len(m.gl.ReviewPending)
	}
	var parts []string
	if due > 0 {
		parts = append(parts, fmt.Sprintf("⚠ %d vencendo/atrasado", due))
	}
	if reviews > 0 {
		parts = append(parts, fmt.Sprintf("👀 %d review aguardando", reviews))
	}
	if len(parts) == 0 {
		return ""
	}
	return alertStyle.Render(" " + strings.Join(parts, " · ") + " ")
}

func (m Model) footer(vp interface{ ScrollPercent() float64 }) string {
	if m.mode == modeFiltering {
		return dim.Render(" buscar: ") + m.filterInput.View() + dim.Render("  (enter aplica · esc limpa)")
	}
	help := "tab/1-6 abas · g relatório do dia · j/k rolar · r atualizar · q sair"
	if m.mode == modeDescribing {
		help = "ctrl+d inicia · ctrl+r alterna dev/revisão · esc cancela"
	} else if m.mode == modePickingService {
		help = "j/k escolher serviço · enter confirmar · esc cancelar"
	} else if m.mode == modeDetail {
		help = "esc/q voltar · o abrir no navegador · j/k rolar"
	} else if m.mode == modeReport {
		help = "esc/q voltar · j/k rolar · r atualizar"
	} else if m.tab == tabGitLab || m.tab == tabJira {
		help = "j/k selecionar · enter detalhes · o navegador · c sessão · / buscar · r atualizar · q sair"
	} else if m.tab == tabSessoes {
		help = "s iniciar/aprovar/corrigir · enter resultado · t interativo · v diff · e editor · f concluir · x cancelar · d/D remover"
	} else if m.tab == tabTarefas {
		if m.mode == modeAdding {
			help = "enter salvar · esc cancelar"
		} else {
			help = "a adicionar · espaço concluir · d apagar · j/k navegar · tab/1-6 abas · q sair"
		}
	}
	status := ""
	if m.loading > 0 {
		status = m.spin.View() + " atualizando…"
	} else if !m.updated.IsZero() {
		status = "atualizado às " + m.updated.Format("15:04:05")
	}
	if p := vp.ScrollPercent(); p < 1 {
		status += dim.Render(fmt.Sprintf(" · %d%%", int(p*100)))
	}
	if m.demo {
		return alertStyle.Render(" 🧪 DEMO ") + dim.Render(" "+help+"   "+status)
	}
	return dim.Render(" " + help + "   " + status)
}

// shortRef encurta a referência do MR ("Roteamento/hades!9470" → "hades!9470").
func shortRef(mr gitlab.MR) string {
	ref := mr.References.Full
	if ref == "" {
		return fmt.Sprintf("!%d", mr.IID)
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	return ref
}

// prioBadge destaca prioridades fora do comum; as medianas ficam ocultas.
func prioBadge(p string) string {
	l := strings.ToLower(p)
	switch {
	case strings.Contains(l, "high"), strings.Contains(l, "alt"),
		strings.Contains(l, "urgent"), strings.Contains(l, "block"):
		return " " + errStyle.Render("⬆ "+p)
	case strings.Contains(l, "low"), strings.Contains(l, "baix"), strings.Contains(l, "minor"):
		return " " + dim.Render("⬇ "+p)
	}
	return ""
}

// sidelineKind classifica issues que estão "paradas" do ponto de vista de
// quem trabalha nelas: aguardando deploy ou bloqueadas.
func sidelineKind(status string) string {
	l := strings.ToLower(status)
	switch {
	case strings.Contains(l, "deploy"):
		return "em deploy"
	case strings.Contains(l, "bloquead"), strings.Contains(l, "impedid"):
		return "bloqueada"
	}
	return ""
}

// mrBadge resume os MRs ligados a uma issue (pela #TAG no título).
func (m Model) mrBadge(key string) string {
	if m.gl == nil || key == "" {
		return ""
	}
	var parts []string
	for _, mr := range m.gl.OpenMRs {
		if mr.JiraKey() == key {
			parts = append(parts, fmt.Sprintf("!%d aberto", mr.IID))
		}
	}
	for _, mr := range m.gl.Merged {
		if mr.JiraKey() == key {
			parts = append(parts, fmt.Sprintf("!%d mergeado", mr.IID))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return dim.Render(" · " + strings.Join(parts, ", "))
}

const hojeMaxIssues = 6

func (m Model) viewHoje() string {
	var b strings.Builder
	today := time.Now().Format("2006-01-02")
	week := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	switch {
	case m.glErr != nil:
		b.WriteString(section.Render("⏳ Reviews aguardando você") + "\n")
		b.WriteString(errStyle.Render("  "+m.glErr.Error()) + "\n")
	case m.gl == nil:
		b.WriteString(section.Render("⏳ Reviews aguardando você") + "\n")
		b.WriteString(dim.Render("  carregando…") + "\n")
	case len(m.gl.ReviewPending) == 0:
		b.WriteString(section.Render("⏳ Reviews aguardando você") + "\n")
		b.WriteString(okStyle.Render("  nenhum review pendente ✓") + "\n")
	default:
		b.WriteString(section.Render(fmt.Sprintf("⏳ Reviews aguardando você (%d)", len(m.gl.ReviewPending))) + "\n")
		for _, mr := range m.gl.ReviewPending {
			line := "  " + dim.Render(shortRef(mr)) + " " + mr.ShortTitle()
			if k := mr.JiraKey(); k != "" {
				line += dim.Render(" #" + k)
			}
			if age := humanAge(mr.UpdatedAt); age != "" {
				line += dim.Render(" · há " + age)
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n")

	b.WriteString(m.viewNovidades())

	switch {
	case m.jiErr != nil:
		b.WriteString(section.Render("🔧 Em andamento") + "\n")
		b.WriteString(errStyle.Render("  "+m.jiErr.Error()) + "\n")
	case m.ji == nil:
		b.WriteString(section.Render("🔧 Em andamento") + "\n")
		b.WriteString(dim.Render("  carregando…") + "\n")
	default:
		var active []jira.Issue
		sidelined := map[string]int{}
		for _, is := range m.ji.Open {
			if is.Category != "indeterminate" {
				continue
			}
			if k := sidelineKind(is.Status); k != "" {
				sidelined[k]++
				continue
			}
			active = append(active, is)
		}
		sort.SliceStable(active, func(i, j int) bool { return active[i].PrioRank < active[j].PrioRank })

		b.WriteString(section.Render(fmt.Sprintf("🔧 Em andamento (%d)", len(active))) + "\n")
		shown := active
		if len(shown) > hojeMaxIssues {
			shown = shown[:hojeMaxIssues]
		}
		for _, is := range shown {
			line := "  " + warnStyle.Render(is.Key) + " " + is.Summary +
				dim.Render(" ["+is.Status+"]") + prioBadge(is.Priority) + m.mrBadge(is.Key)
			b.WriteString(line + "\n")
		}
		if len(active) > hojeMaxIssues {
			b.WriteString(dim.Render(fmt.Sprintf("  … e mais %d na aba Jira", len(active)-hojeMaxIssues)) + "\n")
		}
		if len(active) == 0 {
			b.WriteString(dim.Render("  nada em andamento") + "\n")
		}
		if len(sidelined) > 0 {
			var parts []string
			for _, k := range []string{"em deploy", "bloqueada"} {
				if sidelined[k] > 0 {
					parts = append(parts, fmt.Sprintf("%d %s", sidelined[k], k))
				}
			}
			b.WriteString(dim.Render("  fora do fluxo: "+strings.Join(parts, " · ")) + "\n")
		}
	}
	b.WriteString("\n")

	b.WriteString(section.Render("🔥 Vence hoje / atrasado") + "\n")
	n := 0
	if m.ji != nil {
		for _, is := range m.ji.Open {
			if is.Due != "" && is.Due <= today {
				b.WriteString("  " + warnStyle.Render(is.Key) + " " + is.Summary + dim.Render(" ("+is.Due+")") + "\n")
				n++
			}
		}
	}
	for _, t := range m.store.Tasks {
		if !t.Done && t.Due != "" && t.Due <= today {
			b.WriteString("  " + warnStyle.Render("tarefa") + " " + t.Text + dim.Render(" ("+t.Due+")") + "\n")
			n++
		}
	}
	if n == 0 {
		b.WriteString(okStyle.Render("  nada vencendo hoje ✓") + "\n")
	}
	b.WriteString("\n")

	b.WriteString(section.Render("📅 Próximos 7 dias") + "\n")
	n = 0
	if m.ji != nil {
		for _, is := range m.ji.Open {
			if is.Due != "" && is.Due > today && is.Due <= week {
				b.WriteString("  " + is.Key + " " + is.Summary + dim.Render(" ("+is.Due+")") + "\n")
				n++
			}
		}
	}
	for _, t := range m.store.Tasks {
		if !t.Done && t.Due != "" && t.Due > today && t.Due <= week {
			b.WriteString("  tarefa: " + t.Text + dim.Render(" ("+t.Due+")") + "\n")
			n++
		}
	}
	if n == 0 {
		b.WriteString(dim.Render("  nada agendado") + "\n")
	}

	return b.String()
}

// trendLine compara dois valores e marca melhor/pior; lowerBetter inverte o
// sentido (ex.: lead time, onde menos é melhor).
func trendLine(name string, cur, prev float64, lowerBetter bool) string {
	pctStr := ""
	if prev != 0 {
		pctStr = fmt.Sprintf(" (%+.0f%%)", (cur-prev)/prev*100)
	}
	arrow := "↑"
	if cur < prev {
		arrow = "↓"
	}
	base := fmt.Sprintf("  %-18s %6s vs %-6s ", name, fmtNum(cur), fmtNum(prev))
	better := cur > prev
	if lowerBetter {
		better = cur < prev
	}
	switch {
	case cur == prev:
		return base + dim.Render("= igual")
	case better:
		return base + okStyle.Render(arrow+" melhor"+pctStr)
	default:
		return base + errStyle.Render(arrow+" pior"+pctStr)
	}
}

// barCell desenha uma barra proporcional (n em relação a maxN) com largura
// visível fixa, seguida do número — o padding fica dentro do Render para o
// alinhamento não quebrar com os códigos de cor.
func barCell(style lipgloss.Style, n, maxN, width int) string {
	w := 0
	if maxN > 0 && n > 0 {
		w = n * width / maxN
		if w == 0 {
			w = 1
		}
	}
	return style.Render(fmt.Sprintf("%-*s", width, strings.Repeat("▇", w))) + fmt.Sprintf(" %2d", n)
}

// goalBar desenha o progresso de uma meta semanal: barra proporcional,
// "atual/meta" e um ✓ quando batida.
func goalBar(label string, cur, goal int) string {
	const w = 12
	fill := cur * w / goal
	if fill > w {
		fill = w
	}
	if fill < 0 {
		fill = 0
	}
	bar := barStyle.Render(fmt.Sprintf("%-*s", w, strings.Repeat("▇", fill)))
	mark := ""
	if cur >= goal {
		mark = okStyle.Render(" ✓")
	}
	return fmt.Sprintf("  %-16s %s %d/%d%s", label, bar, cur, goal, mark)
}

func (m Model) viewDesempenho() string {
	var b strings.Builder
	b.WriteString(section.Render("📈 Desempenho") + "\n\n")

	if m.glErr != nil {
		b.WriteString(errStyle.Render(m.glErr.Error()) + "\n")
	}
	if m.jiErr != nil {
		b.WriteString(errStyle.Render(m.jiErr.Error()) + "\n")
	}
	if m.gl == nil && m.ji == nil {
		if m.glErr == nil && m.jiErr == nil {
			b.WriteString(dim.Render("carregando…") + "\n")
		}
		return b.String()
	}
	if m.glErr != nil || m.jiErr != nil {
		b.WriteString("\n")
	}

	now := time.Now()
	today := now.Format("2006-01-02")
	weekStart := startOfWeek(now)
	curDayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	curStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	prevStart := curStart.AddDate(0, -1, 0)
	prevEndDate := curStart.AddDate(0, 0, -1).Format("2006-01-02")
	dCur, dPrev := curStart.Format("2006-01-02"), prevStart.Format("2006-01-02")

	type prow struct {
		label  string
		tStart time.Time // início (para MRs)
		tEnd   time.Time // fim exclusivo (para MRs)
		dStart string    // início (para issues, inclusivo)
		dEnd   string    // fim (para issues, inclusivo)
	}
	rows := []prow{
		{"esta semana", weekStart, now, weekStart.Format("2006-01-02"), today},
		{fmt.Sprintf("%s (até dia %d)", ptMonth(curStart), now.Day()), curStart, now, dCur, today},
		{ptMonth(prevStart) + " (completo)", prevStart, curStart, dPrev, prevEndDate},
	}

	mine := m.myMRs()
	b.WriteString(dim.Render(fmt.Sprintf("  %-20s %12s %14s %8s", "", "MRs abertos", "MRs mergeados", "Issues")) + "\n")
	for _, r := range rows {
		opened, mrs := 0, 0
		if m.gl != nil {
			opened = len(createdIn(mine, r.tStart, r.tEnd))
			mrs = len(mergedIn(m.gl.Merged, r.tStart, r.tEnd))
		}
		n := 0
		if m.ji != nil {
			n = len(resolvedIn(m.ji.Resolved, r.dStart, r.dEnd))
		}
		b.WriteString(fmt.Sprintf("  %-20s %12d %14d %8d\n", r.label, opened, mrs, n))
	}
	b.WriteString("\n")

	// Metas semanais e sequência de dias com entrega.
	if m.hist != nil {
		b.WriteString(section.Render("🎯 Metas e sequência") + "\n")
		if streak := m.hist.Streak(now); streak > 0 {
			b.WriteString(okStyle.Render(fmt.Sprintf("  🔥 sequência: %d dias úteis com entrega", streak)) + "\n")
		} else {
			b.WriteString(dim.Render("  sem sequência ativa — entregue algo para começar") + "\n")
		}
		if g := m.cfg.Goals.WeeklyMRs; g > 0 && m.gl != nil {
			b.WriteString(goalBar("MRs na semana", len(mergedIn(m.gl.Merged, weekStart, now)), g) + "\n")
		}
		if g := m.cfg.Goals.WeeklyIssues; g > 0 && m.ji != nil {
			b.WriteString(goalBar("Issues na semana", len(resolvedIn(m.ji.Resolved, weekStart.Format("2006-01-02"), today)), g) + "\n")
		}
		b.WriteString("\n")
	}

	// Tendência: dia, semana e mês, cada um contra o período anterior
	// equivalente (mesmo nº de dias decorridos), para um termômetro justo.
	b.WriteString(section.Render("⚖ Tendência") + "\n")

	// writeMRTrends compara abertos e mergeados do período atual contra o
	// anterior equivalente — usado nas três janelas (dia, semana, mês).
	writeMRTrends := func(curStart, curEnd, prevStart, prevEnd time.Time) {
		if m.gl == nil {
			return
		}
		c := len(createdIn(mine, curStart, curEnd))
		p := len(createdIn(mine, prevStart, prevEnd))
		b.WriteString(trendLine("MRs abertos", float64(c), float64(p), false) + "\n")
		c = len(mergedIn(m.gl.Merged, curStart, curEnd))
		p = len(mergedIn(m.gl.Merged, prevStart, prevEnd))
		b.WriteString(trendLine("MRs mergeados", float64(c), float64(p), false) + "\n")
	}

	// Hoje vs ontem.
	yStart := curDayStart.AddDate(0, 0, -1)
	yDate := yStart.Format("2006-01-02")
	b.WriteString(dim.Render("  hoje vs ontem") + "\n")
	writeMRTrends(curDayStart, now, yStart, curDayStart)
	if m.ji != nil {
		c := len(resolvedIn(m.ji.Resolved, today, today))
		p := len(resolvedIn(m.ji.Resolved, yDate, yDate))
		b.WriteString(trendLine("Issues resolvidas", float64(c), float64(p), false) + "\n")
	}

	// Esta semana vs a passada, até o mesmo momento da semana.
	elapsed := now.Sub(weekStart)
	lastWeekStart := weekStart.AddDate(0, 0, -7)
	lastWeekEnd := lastWeekStart.Add(elapsed)
	b.WriteString(dim.Render(fmt.Sprintf("  esta semana vs passada (até %s)", now.Format("02/01"))) + "\n")
	writeMRTrends(weekStart, now, lastWeekStart, lastWeekEnd)
	if m.ji != nil {
		c := len(resolvedIn(m.ji.Resolved, weekStart.Format("2006-01-02"), today))
		p := len(resolvedIn(m.ji.Resolved, lastWeekStart.Format("2006-01-02"), lastWeekEnd.Format("2006-01-02")))
		b.WriteString(trendLine("Issues resolvidas", float64(c), float64(p), false) + "\n")
	}

	// Este mês até hoje vs o mesmo nº de dias do mês anterior.
	prevCmpEnd := prevStart.AddDate(0, 0, now.Day()-1)
	if !prevCmpEnd.Before(curStart) {
		prevCmpEnd = curStart.AddDate(0, 0, -1)
	}
	var curResolved, prevResolved, prevMonthAll []jira.Issue
	if m.ji != nil {
		curResolved = resolvedIn(m.ji.Resolved, dCur, today)
		prevResolved = resolvedIn(m.ji.Resolved, dPrev, prevCmpEnd.Format("2006-01-02"))
		prevMonthAll = resolvedIn(m.ji.Resolved, dPrev, prevEndDate)
	}
	b.WriteString(dim.Render(fmt.Sprintf("  %s vs %s (até o dia %d)", ptMonth(curStart), ptMonth(prevStart), prevCmpEnd.Day())) + "\n")
	writeMRTrends(curStart, now, prevStart, prevCmpEnd.AddDate(0, 0, 1))
	if m.ji != nil {
		b.WriteString(trendLine("Issues resolvidas", float64(len(curResolved)), float64(len(prevResolved)), false) + "\n")
		curLT, prevLT := avgLeadDays(curResolved), avgLeadDays(prevResolved)
		if curLT > 0 && prevLT > 0 {
			b.WriteString(trendLine("Lead time (dias)", curLT, prevLT, true) + "\n")
		}
	}
	b.WriteString("\n")

	// Ritmo semanal: produção em cada semana (seg–dom) desde o início do
	// mês anterior, para enxergar aceleração ou queda ao longo do tempo.
	b.WriteString(section.Render("📅 Ritmo semanal") + "\n")
	const barWidth = 12
	b.WriteString(dim.Render(fmt.Sprintf("  %-13s %-*s %-*s %-*s", "seg–dom", barWidth+3, "MRs abertos", barWidth+3, "MRs mergeados", barWidth+3, "issues resolvidas")) + "\n")
	type wk struct {
		label            string
		opened, mrs, iss int
	}
	var weeks []wk
	maxOpened, maxMRs, maxIss := 0, 0, 0
	for ws := startOfWeek(prevStart); ws.Before(now); ws = ws.AddDate(0, 0, 7) {
		we := ws.AddDate(0, 0, 7)
		w := wk{label: ws.Format("02/01") + "–" + we.AddDate(0, 0, -1).Format("02/01")}
		if we.After(now) {
			w.label += "*"
		}
		if m.gl != nil {
			w.opened = len(createdIn(mine, ws, we))
			w.mrs = len(mergedIn(m.gl.Merged, ws, we))
		}
		if m.ji != nil {
			w.iss = len(resolvedIn(m.ji.Resolved, ws.Format("2006-01-02"), we.AddDate(0, 0, -1).Format("2006-01-02")))
		}
		maxOpened = max(maxOpened, w.opened)
		maxMRs = max(maxMRs, w.mrs)
		maxIss = max(maxIss, w.iss)
		weeks = append(weeks, w)
	}
	for _, w := range weeks {
		b.WriteString("  " + dim.Render(fmt.Sprintf("%-13s", w.label)) + " " +
			barCell(barStyle3, w.opened, maxOpened, barWidth) + " " +
			barCell(barStyle, w.mrs, maxMRs, barWidth) + " " +
			barCell(barStyle2, w.iss, maxIss, barWidth) + "\n")
	}
	b.WriteString(dim.Render("  * semana atual, ainda incompleta") + "\n\n")

	// Composição do trabalho por mês.
	writeBreakdown := func(title string, mrs []gitlab.MR, issues []jira.Issue) {
		b.WriteString(section.Render(title) + "\n")
		wrote := false
		if s := countBy(mrs, func(mr gitlab.MR) string {
			if k := mr.Kind(); k != "" {
				return k
			}
			return "outros"
		}); s != "" {
			b.WriteString("  MRs por tipo:       " + s + "\n")
			wrote = true
		}
		if s := countBy(issues, func(is jira.Issue) string { return is.Type }); s != "" {
			b.WriteString("  Issues por tipo:    " + s + "\n")
			wrote = true
		}
		if s := countBy(issues, func(is jira.Issue) string { return is.Complexity }); s != "" {
			b.WriteString("  Por complexidade:   " + s + "\n")
			wrote = true
		}
		if lt := avgLeadDays(issues); lt > 0 {
			b.WriteString("  Lead time médio:    " + fmtNum(lt) + " dias\n")
			wrote = true
		}
		if !wrote {
			b.WriteString(dim.Render("  sem dados") + "\n")
		}
		b.WriteString("\n")
	}
	var curMRs, prevMRs []gitlab.MR
	if m.gl != nil {
		curMRs = mergedIn(m.gl.Merged, curStart, now)
		prevMRs = mergedIn(m.gl.Merged, prevStart, curStart)
	}
	var curMonthAll []jira.Issue
	if m.ji != nil {
		curMonthAll = resolvedIn(m.ji.Resolved, dCur, today)
	}
	writeBreakdown("🧩 "+ptMonth(curStart)+" — composição", curMRs, curMonthAll)
	writeBreakdown("🧩 "+ptMonth(prevStart)+" — composição", prevMRs, prevMonthAll)

	if m.gl != nil {
		b.WriteString(section.Render("Agora") + "\n")
		b.WriteString(fmt.Sprintf("  %d MRs abertos · %d reviews pendentes\n", len(m.gl.OpenMRs), len(m.gl.ReviewPending)))
	}
	if m.ji != nil && m.ji.CXField == "" {
		b.WriteString("\n" + dim.Render("complexidade indisponível — campo não encontrado no Jira;"+
			" defina complexity_field no config.toml") + "\n")
	}
	return b.String()
}

func renderMR(mr gitlab.MR) string {
	s := dim.Render(shortRef(mr)) + " " + mr.ShortTitle()
	if k := mr.JiraKey(); k != "" {
		s += " " + warnStyle.Render("#"+k)
	}
	if t := mr.Kind(); t != "" {
		s += dim.Render(" [" + t + "]")
	}
	return s
}

// humanAge formata o tempo desde t de forma curta (agora, 12min, 5h, 3d) —
// usado para mostrar há quanto um MR espera review.
func humanAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "agora"
	case d < time.Hour:
		return fmt.Sprintf("%dmin", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// renderReviewMR é a linha de um MR na fila de review: a do MR mais o tempo
// sem atividade, que aproxima a espera.
func renderReviewMR(mr gitlab.MR) string {
	s := renderMR(mr)
	if age := humanAge(mr.UpdatedAt); age != "" {
		s += dim.Render(" · parado há " + age)
	}
	return s
}

// groupByStatus agrupa as issues pelo nome do status, na ordem definida em
// jira.status_order do config; status fora da lista vão para o final, na
// ordem em que aparecem.
func groupByStatus(issues []jira.Issue, order []string) ([]string, map[string][]jira.Issue) {
	groups := map[string][]jira.Issue{}
	var names []string
	for _, is := range issues {
		if _, ok := groups[is.Status]; !ok {
			names = append(names, is.Status)
		}
		groups[is.Status] = append(groups[is.Status], is)
	}
	rank := func(name string) int {
		for i, o := range order {
			if strings.EqualFold(o, name) {
				return i
			}
		}
		return len(order) + 1
	}
	sort.SliceStable(names, func(i, j int) bool { return rank(names[i]) < rank(names[j]) })
	return names, groups
}

func (m Model) viewTarefas() string {
	var b strings.Builder
	today := time.Now().Format("2006-01-02")

	if len(m.store.Tasks) == 0 && m.mode != modeAdding {
		b.WriteString(dim.Render("nenhuma tarefa — pressione 'a' para adicionar") + "\n")
	}
	for i, t := range m.store.Tasks {
		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("> ")
		}
		check := "[ ]"
		text := t.Text
		if t.Done {
			check = okStyle.Render("[x]")
			text = dim.Render(text)
		}
		due := ""
		if t.Due != "" {
			label := t.Due
			if t.DueTime != "" {
				label += " " + t.DueTime
			}
			switch {
			case t.Done:
				due = dim.Render(" (" + label + ")")
			case t.Due < today:
				due = errStyle.Render(" (atrasada: " + label + ")")
			case t.Due == today:
				hoje := "hoje"
				if t.DueTime != "" {
					hoje += " " + t.DueTime
				}
				due = warnStyle.Render(" (" + hoje + ")")
			default:
				due = dim.Render(" (" + label + ")")
			}
		}
		badge := ""
		if !t.Done {
			badge = taskPrioBadge(t.Priority)
		}
		b.WriteString(cursor + check + " " + text + badge + due + "\n")
	}
	if m.mode == modeAdding {
		b.WriteString("\n" + section.Render("Nova tarefa:") + "\n" + m.input.View() + "\n")
		if m.addErr != "" {
			b.WriteString(errStyle.Render("⚠ "+m.addErr) + "\n")
		}
	}
	return b.String()
}
