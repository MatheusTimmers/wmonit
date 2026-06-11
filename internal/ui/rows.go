package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
)

// focusItem é um MR ou uma issue selecionável nas abas GitLab/Jira.
// Exatamente um dos campos é não-nulo.
type focusItem struct {
	mr    *gitlab.MR
	issue *jira.Issue
}

// row é uma linha já renderizada. Quando item != nil ela é selecionável
// (recebe o cursor e responde a enter/o).
type row struct {
	text string
	item *focusItem
}

// renderRows desenha as linhas marcando a selecionada de índice cursor e
// devolve também o número da linha do item selecionado (para rolagem).
func renderRows(rows []row, cursor int) (string, int) {
	var b strings.Builder
	line, sel, i := 0, 0, 0
	for _, r := range rows {
		if r.item == nil {
			b.WriteString(r.text + "\n")
			line++
			continue
		}
		marker := "  "
		if i == cursor {
			marker = cursorStyle.Render("▌ ")
			sel = line
		}
		b.WriteString(marker + r.text + "\n")
		line++
		i++
	}
	return b.String(), sel
}

// currentRows devolve as linhas da aba ativa (só GitLab e Jira têm seleção).
func (m Model) currentRows() []row {
	switch m.tab {
	case tabGitLab:
		return m.gitlabRows()
	case tabJira:
		return m.jiraRows()
	}
	return nil
}

func (m Model) focusCount() int {
	n := 0
	for _, r := range m.currentRows() {
		if r.item != nil {
			n++
		}
	}
	return n
}

// selectedItem devolve o item sob o cursor, ou nil se não houver.
func (m Model) selectedItem() *focusItem {
	i := 0
	for _, r := range m.currentRows() {
		if r.item == nil {
			continue
		}
		if i == m.cursor {
			return r.item
		}
		i++
	}
	return nil
}

func (m Model) gitlabRows() []row {
	var rows []row
	hdr := func(s string) { rows = append(rows, row{text: s}) }
	if m.glErr != nil {
		hdr(errStyle.Render(m.glErr.Error()))
		return rows
	}
	if m.gl == nil {
		hdr(dim.Render("carregando…"))
		return rows
	}
	if q := strings.ToLower(strings.TrimSpace(m.filter)); q != "" {
		var items []row
		seen := map[string]bool{}
		add := func(mrs []gitlab.MR) {
			for i := range mrs {
				id := fmt.Sprintf("%d-%d", mrs[i].ProjectID, mrs[i].IID)
				if seen[id] || !matchMR(mrs[i], q) {
					continue
				}
				seen[id] = true
				items = append(items, row{text: renderMR(mrs[i]), item: &focusItem{mr: &mrs[i]}})
			}
		}
		add(m.gl.OpenMRs)
		add(m.gl.ReviewPending)
		add(m.gl.Merged)
		hdr(section.Render(fmt.Sprintf("🔎 filtro: %q (%d)", m.filter, len(items))))
		rows = append(rows, items...)
		if len(items) == 0 {
			hdr(dim.Render("  nenhum resultado"))
		}
		return rows
	}

	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	merged := mergedIn(m.gl.Merged, startOfWeek(now), now)

	hdr(section.Render("📊 @" + m.gl.Username))
	hdr(fmt.Sprintf("  MRs mergeados — hoje: %d · semana: %d · mês: %d",
		len(mergedIn(m.gl.Merged, dayStart, now)), len(merged), len(mergedIn(m.gl.Merged, monthStart, now))))
	hdr(fmt.Sprintf("  MRs abertos: %d · reviews pendentes: %d", len(m.gl.OpenMRs), len(m.gl.ReviewPending)))
	hdr("")

	addMRs := func(title string, mrs []gitlab.MR) {
		hdr(section.Render(title))
		if len(mrs) == 0 {
			hdr(dim.Render("  nenhum"))
			return
		}
		for i := range mrs {
			rows = append(rows, row{text: renderMR(mrs[i]), item: &focusItem{mr: &mrs[i]}})
		}
	}
	addMRs("📬 MRs abertos", m.gl.OpenMRs)
	hdr("")
	addMRs("⏳ Aguardando seu review", m.gl.ReviewPending)
	hdr("")
	addMRs("✅ Mergeados nesta semana", merged)
	return rows
}

func (m Model) jiraRows() []row {
	var rows []row
	hdr := func(s string) { rows = append(rows, row{text: s}) }
	if m.jiErr != nil {
		hdr(errStyle.Render(m.jiErr.Error()))
		return rows
	}
	if m.ji == nil {
		hdr(dim.Render("carregando…"))
		return rows
	}
	if q := strings.ToLower(strings.TrimSpace(m.filter)); q != "" {
		var items []row
		seen := map[string]bool{}
		add := func(issues []jira.Issue) {
			for i := range issues {
				if seen[issues[i].Key] || !matchIssue(issues[i], q) {
					continue
				}
				seen[issues[i].Key] = true
				items = append(items, row{text: m.issueLine(issues[i]), item: &focusItem{issue: &issues[i]}})
			}
		}
		add(m.ji.Open)
		add(m.ji.Resolved)
		hdr(section.Render(fmt.Sprintf("🔎 filtro: %q (%d)", m.filter, len(items))))
		rows = append(rows, items...)
		if len(items) == 0 {
			hdr(dim.Render("  nenhum resultado"))
		}
		return rows
	}

	now := time.Now()
	today := now.Format("2006-01-02")
	weekStartD := startOfWeek(now).Format("2006-01-02")
	monthStartD := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).Format("2006-01-02")
	resolvedWeek := resolvedIn(m.ji.Resolved, weekStartD, today)

	hdr(section.Render("📊 Resumo"))
	hdr(fmt.Sprintf("  resolvidas — hoje: %d · semana: %d · mês: %d",
		len(resolvedIn(m.ji.Resolved, today, today)), len(resolvedWeek), len(resolvedIn(m.ji.Resolved, monthStartD, today))))
	hdr(fmt.Sprintf("  abertas: %d", len(m.ji.Open)))
	hdr("")

	if len(m.ji.Open) == 0 {
		hdr(section.Render("📋 Suas issues abertas"))
		hdr(dim.Render("  nenhuma"))
	} else {
		names, groups := groupByStatus(m.ji.Open, m.cfg.Jira.StatusOrder)
		for _, name := range names {
			issues := groups[name]
			hdr(statusHead.Render(fmt.Sprintf("▍%s (%d)", name, len(issues))))
			for i := range issues {
				rows = append(rows, row{text: m.issueLine(issues[i]), item: &focusItem{issue: &issues[i]}})
			}
			hdr("")
		}
	}

	hdr(section.Render("✅ Resolvidas nesta semana"))
	if len(resolvedWeek) == 0 {
		hdr(dim.Render("  nenhuma"))
	}
	for i := range resolvedWeek {
		rows = append(rows, row{text: resolvedLine(resolvedWeek[i]), item: &focusItem{issue: &resolvedWeek[i]}})
	}
	return rows
}

// issueLine renderiza uma issue aberta (prioridade, complexidade, prazo e
// MRs ligados).
func (m Model) issueLine(is jira.Issue) string {
	line := warnStyle.Render(is.Key) + " " + is.Summary + prioBadge(is.Priority)
	if is.Complexity != "" {
		line += dim.Render(" · cx " + is.Complexity)
	}
	if is.Due != "" {
		line += dim.Render(" (vence " + is.Due + ")")
	}
	return line + m.mrBadge(is.Key)
}

func resolvedLine(is jira.Issue) string {
	line := okStyle.Render(is.Key) + " " + is.Summary
	if is.Complexity != "" {
		line += dim.Render(" · cx " + is.Complexity)
	}
	return line
}

// matchMR e matchIssue testam se o item bate com a busca (q já minúsculo).
func matchMR(mr gitlab.MR, q string) bool {
	return strings.Contains(strings.ToLower(mr.Title), q) ||
		strings.Contains(strings.ToLower(shortRef(mr)), q) ||
		strings.Contains(strings.ToLower(mr.JiraKey()), q)
}

func matchIssue(is jira.Issue, q string) bool {
	return strings.Contains(strings.ToLower(is.Key), q) ||
		strings.Contains(strings.ToLower(is.Summary), q) ||
		strings.Contains(strings.ToLower(is.Status), q)
}

// itemURL devolve o link para abrir o item no navegador.
func (m Model) itemURL(it *focusItem) string {
	if it.mr != nil {
		return it.mr.WebURL
	}
	return strings.TrimRight(m.cfg.Jira.URL, "/") + "/browse/" + it.issue.Key
}
