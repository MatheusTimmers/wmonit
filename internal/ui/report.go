package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/timmers/wmonit/internal/gitlab"
)

// viewReport monta o relatório do dia: o que foi concluído hoje (MRs
// mergeados e issues resolvidas) e as tarefas marcadas como feitas hoje.
func (m Model) viewReport() string {
	var b strings.Builder
	now := time.Now()
	today := now.Format("2006-01-02")
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	dayEnd := dayStart.AddDate(0, 0, 1)

	b.WriteString(section.Render("📋 Relatório do dia — "+now.Format("02/01/2006")) + "\n\n")

	// MRs abertos (criados) e mergeados hoje.
	m.writeMRSection(&b, "📬 MRs abertos hoje", func() []gitlab.MR { return createdIn(m.myMRs(), dayStart, dayEnd) })
	m.writeMRSection(&b, "✅ MRs mergeados hoje", func() []gitlab.MR { return mergedIn(m.gl.Merged, dayStart, dayEnd) })

	// Issues resolvidas hoje.
	b.WriteString(section.Render("✅ Issues resolvidas hoje") + "\n")
	switch {
	case m.jiErr != nil:
		b.WriteString(errStyle.Render("  "+m.jiErr.Error()) + "\n")
	case m.ji == nil:
		b.WriteString(dim.Render("  carregando…") + "\n")
	default:
		resolved := resolvedIn(m.ji.Resolved, today, today)
		if len(resolved) == 0 {
			b.WriteString(dim.Render("  nenhuma") + "\n")
		}
		for _, is := range resolved {
			line := "  " + okStyle.Render(is.Key) + " " + is.Summary
			if is.Complexity != "" {
				line += dim.Render(" · cx " + is.Complexity)
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n")

	// Tarefas concluídas hoje.
	b.WriteString(section.Render("✅ Tarefas concluídas hoje") + "\n")
	n := 0
	for _, t := range m.store.Tasks {
		if t.Done && t.DoneAt != nil && t.DoneAt.Format("2006-01-02") == today {
			b.WriteString("  " + okStyle.Render("[x]") + " " + t.Text + "\n")
			n++
		}
	}
	if n == 0 {
		b.WriteString(dim.Render("  nenhuma") + "\n")
	}

	return b.String()
}

// writeMRSection desenha uma seção de MRs do relatório com o tratamento
// padrão de erro/carregando; mrs só é avaliado quando há dados.
func (m Model) writeMRSection(b *strings.Builder, title string, mrs func() []gitlab.MR) {
	b.WriteString(section.Render(title) + "\n")
	switch {
	case m.glErr != nil:
		b.WriteString(errStyle.Render("  "+m.glErr.Error()) + "\n")
	case m.gl == nil:
		b.WriteString(dim.Render("  carregando…") + "\n")
	default:
		list := mrs()
		if len(list) == 0 {
			b.WriteString(dim.Render("  nenhum") + "\n")
		}
		for _, mr := range list {
			line := "  " + dim.Render(shortRef(mr)) + " " + mr.ShortTitle()
			if k := mr.JiraKey(); k != "" {
				line += dim.Render(" #" + k)
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n")
}

// reportSummary conta os itens concluídos hoje para o subtítulo do relatório.
func (m Model) reportSummary() string {
	now := time.Now()
	today := now.Format("2006-01-02")
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	opened, mrs, iss, tsk := 0, 0, 0, 0
	if m.gl != nil {
		opened = len(createdIn(m.myMRs(), dayStart, dayStart.AddDate(0, 0, 1)))
		mrs = len(mergedIn(m.gl.Merged, dayStart, dayStart.AddDate(0, 0, 1)))
	}
	if m.ji != nil {
		iss = len(resolvedIn(m.ji.Resolved, today, today))
	}
	for _, t := range m.store.Tasks {
		if t.Done && t.DoneAt != nil && t.DoneAt.Format("2006-01-02") == today {
			tsk++
		}
	}
	return fmt.Sprintf("%d MRs abertos · %d mergeados · %d issues · %d tarefas", opened, mrs, iss, tsk)
}
