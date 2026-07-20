package ui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// openDetail entra no modo detalhe e dispara o carregamento do item.
func (m Model) openDetail(it *focusItem) (tea.Model, tea.Cmd) {
	m.mode = modeDetail
	m.detail.loading = true
	m.detail.body = ""
	m.detail.url = m.itemURL(it)
	if it.mr != nil {
		m.detail.title = "MR " + it.mr.ShortRef()
	} else {
		m.detail.title = it.issue.Key
	}
	m.vp.GotoTop()
	return m, m.fetchDetail(it)
}

// updateDetail trata as teclas enquanto o painel de detalhes está aberto.
func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "enter":
		m.mode = modeNormal
		m.vp.GotoTop()
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "o":
		return m, openURLCmd(m.detail.url)
	}
	m.vp.SetContent(m.content())
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// wrapText quebra o texto na largura do viewport para descrições/comentários
// longos não saírem da tela.
func (m Model) wrapText(s string) string {
	w := m.vp.Width - 2
	if w < 20 {
		w = 20
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}

// linkedMRsText lista os MRs ligados a uma issue para o painel de detalhes.
func (m Model) linkedMRsText(key string) string {
	parts := m.linkedMRs(key)
	if len(parts) == 0 {
		return ""
	}
	return section.Render("🔗 MRs ligados") + "\n  " + strings.Join(parts, ", ")
}

// fetchDetail devolve um comando que busca descrição e comentários do item.
func (m Model) fetchDetail(it *focusItem) tea.Cmd {
	gl, ji := m.glSrc, m.jiSrc
	if it.mr != nil {
		mr := *it.mr
		wrap := m.wrapText
		return func() tea.Msg {
			var b strings.Builder
			b.WriteString(section.Render(mr.ShortTitle()) + "\n")
			if k := mr.JiraKey(); k != "" {
				b.WriteString(warnStyle.Render("#"+k) + "\n")
			}
			b.WriteString("\n")
			if d := strings.TrimSpace(mr.Description); d != "" {
				b.WriteString(wrap(d) + "\n\n")
			} else {
				b.WriteString(dim.Render("(sem descrição)") + "\n\n")
			}
			notes, err := gl.MRNotes(context.Background(), mr.ProjectID, mr.IID)
			if err != nil {
				return detailMsg{err: err}
			}
			b.WriteString(section.Render("💬 Comentários") + "\n")
			n := 0
			for _, nt := range notes {
				if nt.System {
					continue
				}
				b.WriteString(dim.Render(nt.Author.Name+" · "+nt.CreatedAt.Format("02/01 15:04")) + "\n")
				b.WriteString(wrap(strings.TrimSpace(nt.Body)) + "\n\n")
				n++
			}
			if n == 0 {
				b.WriteString(dim.Render("  nenhum") + "\n")
			}
			return detailMsg{body: b.String()}
		}
	}

	is := *it.issue
	linked := m.linkedMRsText(is.Key)
	wrap := m.wrapText
	return func() tea.Msg {
		d, err := ji.IssueDetail(context.Background(), is.Key)
		if err != nil {
			return detailMsg{err: err}
		}
		var b strings.Builder
		b.WriteString(section.Render(d.Summary) + "\n")
		b.WriteString(dim.Render("["+d.Status+"]") + "\n\n")
		if linked != "" {
			b.WriteString(linked + "\n\n")
		}
		if dd := strings.TrimSpace(d.Description); dd != "" {
			b.WriteString(wrap(dd) + "\n\n")
		} else {
			b.WriteString(dim.Render("(sem descrição)") + "\n\n")
		}
		b.WriteString(section.Render("💬 Comentários") + "\n")
		if len(d.Comments) == 0 {
			b.WriteString(dim.Render("  nenhum") + "\n")
		}
		for _, c := range d.Comments {
			b.WriteString(dim.Render(c.Author+" · "+c.Created) + "\n")
			b.WriteString(wrap(strings.TrimSpace(c.Body)) + "\n\n")
		}
		return detailMsg{body: b.String()}
	}
}
