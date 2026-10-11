package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

type stepInput struct {
	sess        session.Session
	agent       *herdr.Agent
	trust       bool
	mr          *gitlab.MRStatus
	staleReason string
	git         *worktree.Info
}

func nextStep(in stepInput) string {
	s := in.sess
	switch {
	case !s.Active():
		return ""
	case s.Workspace == "":
		return "workspace fechado — w reabre e retoma a conversa"
	case in.trust:
		return "o Claude pede para confiar na pasta — t confirma"
	case in.agent != nil && in.agent.Status == herdr.StatusBlocked:
		return "o Claude espera você (permissão ou pergunta) — a abre no herdr"
	case s.PendingPrompt != "":
		return "a tarefa vai para o Claude assim que ele ficar livre"
	case in.staleReason != "":
		return in.staleReason + " — f conclui"
	case in.agent != nil && in.agent.Status == herdr.StatusWorking:
		return "o Claude está trabalhando — a acompanha no herdr"
	}
	if st := in.mr; st != nil {
		switch {
		case st.CI() == "failed":
			return "CI quebrado — p pede ao Claude para corrigir"
		case st.HasConflicts:
			return "conflito com a branch de destino — p pede ao Claude o rebase"
		case !st.BlockingDiscussionsResolved:
			return "threads abertas no MR — p pede ao Claude para responder"
		case st.Ready():
			return "MR pronto para merge — o abre no navegador"
		}
	}
	if g := in.git; g != nil {
		switch {
		case len(g.Dirty) > 0:
			return "mudanças não commitadas — p pede o commit ao Claude, depois s entrega"
		case g.Ahead > 0 || (g.Upstream == "" && len(g.Commits) > 0):
			return "commits ainda não enviados — s faz o push e abre o MR"
		}
	}
	if in.agent == nil {
		return "sem Claude no workspace — p sobe um e manda a mensagem"
	}
	return ""
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case t.IsZero():
		return ""
	case d < time.Minute:
		return "agora"
	case d < time.Hour:
		return fmt.Sprintf("há %d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("há %dh", int(d.Hours()))
	case d < 48*time.Hour:
		return "ontem"
	}
	return fmt.Sprintf("há %d dias", int(d.Hours()/24))
}

func (m Model) sessionDetailCmd(r wsRow) tea.Cmd {
	s := r.sess
	h := m.hr
	gl, ji := m.glSrc, m.jiSrc
	wrap := m.wrapText
	usage := m.usageText
	state := m.stateLabel(r)
	var agent *herdr.Agent
	if a, ok := m.rowAgent(r); ok {
		agent = &a
	}
	_, trust := m.sui.trust[s.Agent]
	mr, ref := m.sessionMR(s)
	st := m.live.mrStatus[ref]
	var issue *jira.Issue
	if k := s.IssueKey(); k != "" && m.fetch.ji != nil {
		for _, is := range m.fetch.ji.Open {
			if is.Key == k {
				is := is
				issue = &is
			}
		}
	}
	reviewing := false
	if mr != nil && m.fetch.gl != nil {
		for _, x := range m.fetch.gl.ReviewPending {
			if x.ProjectID == mr.ProjectID && x.IID == mr.IID {
				reviewing = true
			}
		}
	}
	stale := ""
	for _, it := range m.staleItems() {
		if it.Session.ID == s.ID {
			stale = it.Reason
		}
	}
	ws, _ := m.sui.snap.Workspace(s.Workspace)
	today := time.Now().Format("2006-01-02")

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dir := hub.Dir(s)
		var info *worktree.Info
		if i, err := worktree.Inspect(dir); err == nil {
			info = &i
		}

		var b strings.Builder
		head := func(title string) { b.WriteString(section.Render(title) + "\n") }
		row := func(label, value string) {
			if strings.TrimSpace(value) != "" {
				b.WriteString("  " + dim.Render(fmt.Sprintf("%-11s", label)) + value + "\n")
			}
		}

		b.WriteString(state + "  " + warnStyle.Render(s.Name()) + " " + s.Title + "\n")
		meta := []string{s.Service}
		switch {
		case s.Kind == session.KindFree:
			meta = append(meta, "livre")
		case s.IsReview():
			meta = append(meta, "revisão de MR")
		default:
			meta = append(meta, "desenvolvimento")
		}
		if !s.Created.IsZero() {
			meta = append(meta, "criado "+ago(s.Created))
		}
		if s.Model != "" {
			meta = append(meta, "modelo "+s.Model)
		}
		b.WriteString(dim.Render(strings.Join(meta, " · ")) + "\n")
		b.WriteString(dim.Render(dir) + "\n\n")

		if step := nextStep(stepInput{sess: s, agent: agent, trust: trust, mr: st, staleReason: stale, git: info}); step != "" {
			b.WriteString(section.Render("➡ Próximo passo") + "  " + step + "\n\n")
		}
		if s.Err != "" {
			b.WriteString(errStyle.Render("último erro: "+s.Err) + "\n\n")
		}
		if p := strings.TrimSpace(s.PendingPrompt); p != "" {
			head("⏳ Pedido esperando o Claude")
			b.WriteString(wrap(truncate(p, 400)) + "\n\n")
		}
		if n := strings.TrimSpace(s.UserNote); n != "" {
			head("📝 Sua explicação")
			b.WriteString(wrap(n) + "\n\n")
		}

		if k := s.IssueKey(); k != "" {
			head("🔗 Jira " + k)
			if issue != nil {
				row("título", issue.Summary)
				row("status", issue.Status)
				row("tipo", issue.Type)
				row("prioridade", issue.Priority)
				due := issue.Due
				switch {
				case due != "" && due < today:
					due = errStyle.Render(due + " (atrasada)")
				case due == today:
					due = warnStyle.Render(due + " (hoje)")
				}
				row("prazo", due)
			} else {
				row("status", dim.Render("fora das suas abertas (resolvida ou de outra pessoa)"))
			}
			if ji != nil {
				if det, err := ji.IssueDetail(ctx, k); err == nil {
					if d := strings.TrimSpace(det.Description); d != "" {
						b.WriteString(indent(wrap(truncateLines(d, 12))) + "\n")
					}
					if c := det.Comments; len(c) > 0 {
						b.WriteString(dim.Render(fmt.Sprintf("  %d comentário(s); os últimos:", len(c))) + "\n")
						for _, cm := range c[max(0, len(c)-3):] {
							b.WriteString("  " + okStyle.Render(cm.Author) + dim.Render(" "+cm.Created) + "  " + truncate(cm.Body, 300) + "\n")
						}
					}
				} else {
					b.WriteString(dim.Render("  (não deu para buscar a descrição: "+err.Error()+")") + "\n")
				}
			}
			b.WriteString("\n")
		}

		if ref != "" {
			head("🔀 MR " + ref)
			if mr != nil {
				row("título", mr.Title)
				branches := mr.SourceBranch
				if mr.TargetBranch != "" {
					branches += " → " + mr.TargetBranch
				}
				row("branches", branches)
				row("atualizado", ago(mr.UpdatedAt))
				if reviewing {
					row("revisão", warnStyle.Render("aguarda a sua revisão"))
				}
				row("link", mr.WebURL)
			}
			if st != nil {
				if p := st.Problems(); len(p) > 0 {
					row("situação", warnStyle.Render(strings.Join(p, " · ")))
				} else if st.Ready() {
					row("situação", okStyle.Render("pronto para merge"))
				}
				if st.HeadPipeline != nil {
					row("pipeline", st.HeadPipeline.Status+"  "+dim.Render(st.HeadPipeline.WebURL))
				}
			} else if mr == nil {
				row("situação", dim.Render("não está entre os abertos"))
			}
			if mr != nil && gl != nil {
				if notes, err := gl.MRNotes(ctx, mr.ProjectID, mr.IID); err == nil {
					var human []gitlab.Note
					for _, n := range notes {
						if !n.System {
							human = append(human, n)
						}
					}
					if len(human) > 0 {
						b.WriteString(dim.Render(fmt.Sprintf("  %d comentário(s); os últimos:", len(human))) + "\n")
						for _, n := range human[max(0, len(human)-3):] {
							b.WriteString("  " + okStyle.Render(n.Author.Name) + dim.Render(" "+ago(n.CreatedAt)) + "  " + truncate(n.Body, 300) + "\n")
						}
					}
				}
			}
			b.WriteString("\n")
		}

		if g := info; g != nil {
			head("🌿 Git")
			row("branch", g.Branch)
			if g.Base != "" {
				row("base", fmt.Sprintf("%s · %d commit(s) novo(s)", g.Base, len(g.Commits)))
			}
			switch {
			case g.Upstream == "":
				row("remoto", warnStyle.Render("branch ainda não enviada"))
			case g.Ahead > 0 || g.Behind > 0:
				row("remoto", fmt.Sprintf("%s · %d para enviar · %d para trazer", g.Upstream, g.Ahead, g.Behind))
			default:
				row("remoto", okStyle.Render(g.Upstream+" em dia"))
			}
			if g.LastCommit != "" {
				row("último", g.LastCommit+dim.Render(" · "+ago(g.LastAt)))
			}
			for _, c := range g.Commits {
				b.WriteString("    " + c + "\n")
			}
			if len(g.Dirty) > 0 {
				b.WriteString(dim.Render(fmt.Sprintf("  %d arquivo(s) não commitado(s):", len(g.Dirty))) + "\n")
				for _, f := range g.Dirty[:min(len(g.Dirty), 15)] {
					b.WriteString("    " + warnStyle.Render(f) + "\n")
				}
				if len(g.Dirty) > 15 {
					b.WriteString(dim.Render(fmt.Sprintf("    … e mais %d", len(g.Dirty)-15)) + "\n")
				}
			}
			b.WriteString("\n")
		}

		if s.Workspace != "" {
			head("🗂 herdr")
			row("workspace", s.Label+dim.Render(" ("+s.Workspace+")"))
			if tabs, err := h.Tabs(s.Workspace); err == nil && len(tabs) > 0 {
				var names []string
				for _, t := range tabs {
					names = append(names, t.Label)
				}
				row("abas", strings.Join(names, " · "))
			}
			if agent != nil {
				row("agent", agent.Name+dim.Render(" · "+agent.Status))
			}
			var toks []string
			for _, n := range hub.TokenNames {
				if v := ws.Tokens[n]; v != "" {
					toks = append(toks, v)
				}
			}
			row("sidebar", strings.Join(toks, " · "))
			b.WriteString("\n")
		}

		if path := claude.TranscriptPath(s.ClaudeSession); path != "" {
			if u, err := claude.ReadUsage(path); err == nil {
				head("💬 Conversa do Claude")
				row("uso", usage(u))
				row("título", u.Title)
				row("modelo", u.Model)
				row("início", u.Started.Local().Format("02/01 15:04"))
				row("ferramenta", u.LastTool)
				if p := strings.TrimSpace(u.LastPrompt); p != "" {
					row("você pediu", truncate(p, 300))
				}
				if t := strings.TrimSpace(u.LastText); t != "" {
					b.WriteString(dim.Render("  última resposta:") + "\n" + indent(wrap(truncate(t, 800))) + "\n")
				}
				b.WriteString(dim.Render("  sessão "+s.ClaudeSession) + "\n\n")
			}
		}

		if agent != nil {
			if screen, err := h.Read(agent.Name, screenLines); err == nil {
				head("🖥 Tela do Claude (" + agent.Name + ")")
				b.WriteString(screen + "\n")
			}
		}
		return detailMsg{body: strings.TrimRight(b.String(), "\n")}
	}
}

func truncateLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n…"
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}
