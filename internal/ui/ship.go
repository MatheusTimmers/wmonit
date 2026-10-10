package ui

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/repocfg"
	"github.com/timmers/wmonit/internal/session"
)

type shippedMsg struct {
	sess session.Session
	info string
	err  error
}

func (m Model) shipCmd(s session.Session) tea.Cmd {
	gl, _ := m.glSrc.(hub.MRCreator)
	ji, _ := m.jiSrc.(hub.IssueMover)
	existing, _ := m.sessionMR(s)
	opts := hub.ShipOpts{
		GitLab:           gl,
		Jira:             ji,
		ReviewTransition: m.cfg.Jira.ReviewTransition,
		Draft:            m.cfg.GitLab.DraftMR,
		ExistingMR:       existing,
	}
	return func() tea.Msg {
		rc, _ := repocfg.Load(s.Repo)
		opts.TargetBranch = rc.TargetBranch
		res, err := hub.Ship(context.Background(), s, opts)
		if err != nil {
			return shippedMsg{err: err}
		}
		parts := []string{"branch " + res.Branch + " enviada"}
		if res.MR != nil {
			s.MR = res.MR.ShortRef()
			if s.URL == "" {
				s.URL = res.MR.WebURL
			}
			if res.Created {
				parts = append(parts, "MR "+s.MR+" criado")
			} else {
				parts = append(parts, "MR "+s.MR+" atualizado")
			}
		}
		if res.MovedTo != "" {
			parts = append(parts, s.IssueKey()+" → "+res.MovedTo)
		}
		info := okStyle.Render(strings.Join(parts, " · "))
		if len(res.Warnings) > 0 {
			info += " " + warnStyle.Render(strings.Join(res.Warnings, " · "))
		}
		return shippedMsg{sess: s, info: info}
	}
}

func (m Model) updateShip(msg shippedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.sui.info = errStyle.Render("entrega: " + msg.err.Error())
		return m, nil
	}
	m.sess.Update(msg.sess.ID, func(s *session.Session) { s.MR, s.URL = msg.sess.MR, msg.sess.URL })
	m.save(m.sess.Save())
	m.sui.info = msg.info
	return m, m.refresh()
}

func (m Model) staleItems() []hub.StaleItem {
	return hub.Stale(m.sess.Sessions, m.fetch.gl, m.fetch.ji, m.live.mrStatus)
}

func (m Model) cleanStale() (tea.Model, tea.Cmd) {
	items := m.staleItems()
	if len(items) == 0 {
		m.sui.info = dim.Render("nada para limpar — nenhum workspace com MR mergeado ou issue resolvida")
		m.sui.confirmClean = false
		return m, nil
	}
	if !m.sui.confirmClean {
		m.sui.confirmClean = true
		var names []string
		for _, it := range items {
			names = append(names, it.Session.Name()+" ("+it.Reason+")")
		}
		m.sui.info = warnStyle.Render("C de novo conclui: " + strings.Join(names, ", ") + " — fecha o workspace e remove o worktree (sem mudanças pendentes)")
		return m, nil
	}
	m.sui.confirmClean = false
	var cmds []tea.Cmd
	for _, it := range items {
		cmds = append(cmds, m.finishCmd(it.Session, false, false))
	}
	m.sui.info = dim.Render("concluindo " + strconv.Itoa(len(items)) + " workspace(s)…")
	return m, tea.Sequence(cmds...)
}

func (m Model) WithOpen(target string) Model {
	m.open = strings.TrimSpace(target)
	return m
}

var openKey = regexp.MustCompile(`[A-Z][A-Z0-9]*-\d+`)

func (m Model) resolveOpen() (tea.Model, tea.Cmd) {
	target := strings.TrimRight(m.open, "/")
	m.open = ""
	for _, mr := range m.allMRs() {
		if strings.TrimRight(mr.WebURL, "/") == target || mr.ShortRef() == target || mr.References.Full == target {
			mr := mr
			return m.startSession(&focusItem{mr: &mr})
		}
	}
	if strings.Contains(target, "merge_requests") {
		m.tab = tabSessoes
		m.sui.info = warnStyle.Render("MR " + target + " não está entre os seus abertos nem nas revisões pendentes")
		return m, nil
	}
	key := openKey.FindString(target)
	if key == "" {
		m.tab = tabSessoes
		m.sui.info = warnStyle.Render("não reconheci " + target + " como issue ou MR")
		return m, nil
	}
	issue := jira.Issue{Key: key, Summary: key}
	if m.fetch.ji != nil {
		for _, is := range m.fetch.ji.Open {
			if is.Key == key {
				issue = is
			}
		}
	}
	return m.startSession(&focusItem{issue: &issue})
}
