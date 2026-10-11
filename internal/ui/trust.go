package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/hub"
)

const hubStale = 30 * time.Minute

type trustProbeMsg struct {
	agent string
	keys  []string
}

type hubKickedMsg struct{ at time.Time }

func (m Model) trustProbeCmd(agent string) tea.Cmd {
	h := m.hr
	return func() tea.Msg {
		screen, err := h.Read(agent, 40)
		if err != nil {
			return trustProbeMsg{agent: agent}
		}
		keys, _ := hub.TrustKeys(screen)
		return trustProbeMsg{agent: agent, keys: keys}
	}
}

func (m Model) confirmTrustCmd(agent string, keys []string) tea.Cmd {
	h := m.hr
	return func() tea.Msg {
		if err := h.SendKeys(agent, keys...); err != nil {
			return infoMsg{err: err}
		}
		return infoMsg{info: okStyle.Render("pasta confirmada no Claude de " + agent)}
	}
}

func (m Model) updateTrust(msg trustProbeMsg) (tea.Model, tea.Cmd) {
	if len(msg.keys) == 0 {
		delete(m.sui.trust, msg.agent)
		return m, nil
	}
	if msg.agent == hub.HubAgent {
		m.sui.info = dim.Render("confirmando a pasta das pendências no Claude…")
		return m, m.confirmTrustCmd(msg.agent, msg.keys)
	}
	m.sui.trust[msg.agent] = msg.keys
	return m, nil
}

func (m *Model) probeBlocked(a herdr.Agent, old string, seen bool) tea.Cmd {
	if a.Status != herdr.StatusBlocked {
		delete(m.sui.trust, a.Name)
		return nil
	}
	if seen && old == herdr.StatusBlocked {
		return nil
	}
	if a.Name != hub.HubAgent && m.agentOwner(a) == "" {
		return nil
	}
	return m.trustProbeCmd(a.Name)
}

func (m Model) saveHubRequest() {
	_ = hub.SaveRequest(m.hubDir(), hub.Request{Asked: m.sui.hubAsked, Pending: m.sui.hubPending})
}

func (m Model) hubState() string {
	a, ok := m.sui.snap.Agent(hub.HubAgent)
	switch {
	case !m.sui.snap.Online():
		return warnStyle.Render("o herdr está offline — o pedido segue guardado e vai quando ele voltar")
	case !ok:
		return dim.Render("subindo o Claude das pendências…")
	case a.Status == herdr.StatusBlocked:
		return alertStyle.Render(" ⏸ o Claude das pendências está esperando você ") + dim.Render("  a abre no herdr para responder")
	case m.sui.hubPending:
		return dim.Render("esperando o Claude das pendências ficar livre para mandar o pedido…")
	case a.Status == herdr.StatusWorking:
		return dim.Render("o agent está analisando as pendências… a resposta aparece aqui sozinha")
	case time.Since(m.sui.hubAsked) > 2*time.Minute:
		return warnStyle.Render("o Claude parou sem escrever a resposta — a abre o herdr para ver o que houve, i pede de novo")
	}
	return dim.Render("o agent está analisando as pendências… a resposta aparece aqui sozinha")
}
