package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/gitlab"
)

// Alertas proativos: a cada refresh comparamos os dados novos com o que já
// foi visto e disparamos notificações de desktop para as novidades —
// review pedido, comentário/menção, build quebrado, issue atribuída ou
// mudança de status. A primeira leitura de cada fonte só estabelece a linha
// de base, senão tudo viraria alerta ao abrir o app (ou a cada reinício).

// todoFace dá o ícone e o rótulo curto de um todo do GitLab pelo action_name.
func todoFace(action string) (icon, label string) {
	switch action {
	case "build_failed":
		return "🔴", "Build falhou"
	case "review_requested":
		return "👀", "Review pedido"
	case "approval_required":
		return "👀", "Aprovação pedida"
	case "mentioned":
		return "💬", "Você foi mencionado"
	case "directly_addressed":
		return "💬", "Você foi citado"
	case "assigned":
		return "📌", "Atribuído a você"
	case "marked":
		return "📌", "Marcado para você"
	case "unmergeable":
		return "⚠", "MR não mescla"
	case "merge_train_removed":
		return "⚠", "Saiu do merge train"
	case "review_submitted":
		return "✅", "Review enviado"
	}
	return "🔔", action
}

// todoIsReviewish diz se o todo já é coberto pela fila de review — esses não
// repetem no bloco "Novidades" (mas ainda geram notificação).
func todoIsReviewish(action string) bool {
	return action == "review_requested" || action == "approval_required"
}

// todoTarget devolve a referência ("grupo/projeto!42") ou, na falta, o
// título do alvo do todo.
func todoTarget(t gitlab.Todo) string {
	if t.Target.References.Full != "" {
		return t.Target.References.Full
	}
	return truncate(t.Target.Title, 60)
}

// notifyAll envolve cada texto numa notificação de desktop; nil se vazio.
func notifyAll(title string, bodies []string) tea.Cmd {
	if len(bodies) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(bodies))
	for _, b := range bodies {
		cmds = append(cmds, notifyCmd(title, b))
	}
	return tea.Batch(cmds...)
}

// newGitlabAlerts atualiza o estado visto e devolve os textos dos todos
// novos desde a última leitura (vazio na primeira, que só fixa a base).
func (m *Model) newGitlabAlerts() []string {
	if m.fetch.gl == nil {
		return nil
	}
	if !m.alert.glBaseline {
		for _, t := range m.fetch.gl.Todos {
			m.alert.seenTodos[t.ID] = true
		}
		m.alert.glBaseline = true
		return nil
	}
	var out []string
	for _, t := range m.fetch.gl.Todos {
		if m.alert.seenTodos[t.ID] {
			continue
		}
		m.alert.seenTodos[t.ID] = true
		_, label := todoFace(t.ActionName)
		out = append(out, label+": "+todoTarget(t))
	}
	return out
}

// gitlabAlerts notifica os todos novos desde a última leitura.
func (m *Model) gitlabAlerts() tea.Cmd {
	return notifyAll("wmonit — GitLab", m.newGitlabAlerts())
}

// newJiraAlerts atualiza o estado visto e devolve os textos das issues
// recém-atribuídas e das que mudaram de status.
func (m *Model) newJiraAlerts() []string {
	if m.fetch.ji == nil {
		return nil
	}
	if !m.alert.jiBaseline {
		for _, is := range m.fetch.ji.Open {
			m.alert.issueStatus[is.Key] = is.Status
		}
		m.alert.jiBaseline = true
		return nil
	}
	var out []string
	current := map[string]bool{}
	for _, is := range m.fetch.ji.Open {
		current[is.Key] = true
		switch prev, known := m.alert.issueStatus[is.Key]; {
		case !known:
			out = append(out, "Nova issue atribuída: "+is.Key+" "+truncate(is.Summary, 80))
		case prev != is.Status:
			out = append(out, is.Key+" → "+is.Status)
		}
		m.alert.issueStatus[is.Key] = is.Status
	}
	// Issues que saíram da lista (resolvidas/reatribuídas) são esquecidas,
	// para uma reatribuição futura voltar a alertar.
	for k := range m.alert.issueStatus {
		if !current[k] {
			delete(m.alert.issueStatus, k)
		}
	}
	return out
}

// jiraAlerts notifica issues recém-atribuídas a você e mudanças de status
// das que já estavam na sua lista.
func (m *Model) jiraAlerts() tea.Cmd {
	return notifyAll("wmonit — Jira", m.newJiraAlerts())
}

// viewNovidades é o bloco compacto de novidades do GitLab na aba Hoje —
// builds, menções e atribuições; reviews ficam na seção própria. "" quando
// não há nada.
func (m Model) viewNovidades() string {
	if m.fetch.gl == nil {
		return ""
	}
	var lines []string
	for _, t := range m.fetch.gl.Todos {
		if todoIsReviewish(t.ActionName) {
			continue
		}
		icon, label := todoFace(t.ActionName)
		line := "  " + icon + " " + label + " " + dim.Render(todoTarget(t))
		if age := humanAge(t.CreatedAt); age != "" {
			line += dim.Render(" · há " + age)
		}
		lines = append(lines, line)
		if len(lines) == 4 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	out := section.Render("🔔 Novidades") + "\n"
	for _, l := range lines {
		out += l + "\n"
	}
	return out + "\n"
}
