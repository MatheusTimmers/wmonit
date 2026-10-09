package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
	"github.com/timmers/wmonit/internal/worktree"
)

const (
	HubLabel = "wmonit"
	HubAgent = "pendencias"
	HubTab   = "pendencias"
)

func HubDir(dataDir string) string { return filepath.Join(dataDir, "pendencias") }

func EnsureHub(h Herdr, snap Snapshot, dir string, args []string) (ready bool, err error) {
	if a, ok := snap.Agent(HubAgent); ok {
		return a.Ready(), nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	var pane herdr.Pane
	if ws, ok := snap.WorkspaceByLabel(HubLabel); ok {
		_, pane, err = h.CreateTab(ws.ID, dir, HubTab)
	} else {
		var tab herdr.Tab
		_, tab, pane, err = h.CreateWorkspace(HubLabel, dir)
		if err == nil {
			_ = h.RenameTab(tab.ID, HubTab)
		}
	}
	if err != nil {
		return false, err
	}
	args = append(append([]string{}, args...), "--permission-mode", "acceptEdits")
	if err := h.StartAgent(HubAgent, "claude", pane.ID, args...); err != nil && !herdr.IsCode(err, herdr.CodeAgentNotReady) {
		return false, err
	}
	return false, nil
}

type Item struct {
	Session    session.Session
	Agent      *herdr.Agent
	Screen     string
	Git        *worktree.Info
	JiraStatus string
	MR         *gitlab.MR
	MRReview   bool
}

type DigestInput struct {
	Now    time.Time
	Items  []Item
	Others []herdr.Workspace
	Jira   *jira.Summary
	GitLab *gitlab.Summary
	Tasks  []tasks.Task
}

func Collect(h Herdr, snap Snapshot, sessions []session.Session, ji *jira.Summary, gl *gitlab.Summary, screenLines int) []Item {
	var mrs, review []gitlab.MR
	if gl != nil {
		mrs = append(append(mrs, gl.OpenMRs...), gl.ReviewPending...)
		review = gl.ReviewPending
	}
	var items []Item
	for _, s := range sessions {
		if !s.Active() {
			continue
		}
		it := Item{Session: s}
		if s.Agent != "" {
			if a, ok := snap.Agent(s.Agent); ok {
				it.Agent = &a
				if screen, err := h.Read(a.Name, screenLines); err == nil {
					it.Screen = screen
				}
			}
		}
		dir := s.Worktree
		if dir == "" {
			dir = s.Repo
		}
		if dir != "" {
			if info, err := worktree.Inspect(dir); err == nil {
				it.Git = &info
			}
		}
		if key := s.IssueKey(); key != "" && ji != nil {
			for _, is := range ji.Open {
				if is.Key == key {
					it.JiraStatus = is.Status
				}
			}
		}
		it.MR = FindMR(s, mrs)
		if it.MR != nil {
			for _, r := range review {
				if r.ProjectID == it.MR.ProjectID && r.IID == it.MR.IID {
					it.MRReview = true
				}
			}
		}
		items = append(items, it)
	}
	return items
}

func Others(snap Snapshot, sessions []session.Session) []herdr.Workspace {
	var out []herdr.Workspace
	for _, w := range snap.Workspaces {
		if strings.EqualFold(w.Label, HubLabel) {
			continue
		}
		linked := false
		for _, s := range sessions {
			if s.Active() && (s.Workspace == w.ID || strings.EqualFold(s.Label, w.Label)) {
				linked = true
				break
			}
		}
		if !linked {
			out = append(out, w)
		}
	}
	return out
}

func Digest(in DigestInput) string {
	var b strings.Builder
	b.WriteString(claude.DigestHeader())
	fmt.Fprintf(&b, "Gerado em %s.\n\n", in.Now.Format("02/01/2006 15:04"))

	linkedIssues := map[string]bool{}
	linkedMRs := map[string]bool{}
	b.WriteString("## Workspaces com tarefa\n\n")
	if len(in.Items) == 0 {
		b.WriteString("(nenhum)\n\n")
	}
	for _, it := range in.Items {
		s := it.Session
		if k := s.IssueKey(); k != "" {
			linkedIssues[k] = true
		}
		if it.MR != nil {
			linkedMRs[it.MR.ShortRef()] = true
		}
		title := s.Title
		if title == "" {
			title = s.Service
		}
		fmt.Fprintf(&b, "### %s — %s\n\n", s.Name(), title)
		fmt.Fprintf(&b, "- Workspace herdr: %s", s.Label)
		if s.Workspace == "" {
			b.WriteString(" (fechado)")
		}
		b.WriteString("\n")
		if s.Service != "" {
			fmt.Fprintf(&b, "- Repositório: %s\n", s.Service)
		}
		dir := s.Worktree
		if dir == "" {
			dir = s.Repo
		}
		if dir != "" {
			fmt.Fprintf(&b, "- Pasta: %s\n", dir)
		}
		if s.IsReview() {
			b.WriteString("- Modo: revisão de MR de outra pessoa\n")
		}
		if k := s.IssueKey(); k != "" {
			status := it.JiraStatus
			if status == "" {
				status = "fora das minhas abertas (resolvida ou de outra pessoa)"
			}
			fmt.Fprintf(&b, "- Jira: %s — %s\n", k, status)
		}
		if it.MR != nil {
			fmt.Fprintf(&b, "- MR: %s — %s", it.MR.ShortRef(), it.MR.Title)
			if it.MRReview {
				b.WriteString(" (aguardando a minha revisão)")
			}
			b.WriteString("\n")
		} else if ref := s.MRRef(); ref != "" {
			fmt.Fprintf(&b, "- MR: %s (não está entre os abertos)\n", ref)
		}
		switch {
		case it.Agent != nil:
			fmt.Fprintf(&b, "- Claude: agent `%s`, estado %s\n", it.Agent.Name, describeStatus(it.Agent.Status))
		case s.Workspace != "":
			b.WriteString("- Claude: nenhum agent rodando no workspace\n")
		}
		if s.PendingPrompt != "" {
			b.WriteString("- Há um pedido esperando o Claude ficar livre para ser enviado\n")
		}
		if s.Err != "" {
			fmt.Fprintf(&b, "- Último erro: %s\n", s.Err)
		}
		if g := it.Git; g != nil {
			fmt.Fprintf(&b, "- Git: branch %s", g.Branch)
			if g.Base != "" {
				fmt.Fprintf(&b, ", %d commit(s) desde %s", len(g.Commits), g.Base)
			}
			fmt.Fprintf(&b, ", %d arquivo(s) não commitado(s)\n", len(g.Dirty))
			for _, c := range g.Commits {
				fmt.Fprintf(&b, "  - %s\n", c)
			}
		}
		if n := strings.TrimSpace(s.UserNote); n != "" {
			fmt.Fprintf(&b, "\nMinha explicação ao criar:\n\n%s\n", quote(n))
		}
		if sc := strings.TrimSpace(it.Screen); sc != "" {
			fmt.Fprintf(&b, "\nÚltimas linhas da tela do Claude:\n\n```\n%s\n```\n", sc)
		}
		b.WriteString("\n")
	}

	if len(in.Others) > 0 {
		b.WriteString("## Outros workspaces do herdr (sem tarefa ligada)\n\n")
		for _, w := range in.Others {
			fmt.Fprintf(&b, "- %s (%s)\n", w.Label, describeStatus(w.AgentStatus))
		}
		b.WriteString("\n")
	}

	today := in.Now.Format("2006-01-02")
	if in.Jira != nil {
		var open []jira.Issue
		for _, is := range in.Jira.Open {
			if !linkedIssues[is.Key] {
				open = append(open, is)
			}
		}
		sort.SliceStable(open, func(i, j int) bool { return open[i].PrioRank < open[j].PrioRank })
		b.WriteString("## Issues do Jira sem workspace\n\n")
		if len(open) == 0 {
			b.WriteString("(nenhuma)\n")
		}
		for _, is := range open {
			fmt.Fprintf(&b, "- %s [%s] %s", is.Key, is.Status, is.Summary)
			if is.Priority != "" {
				fmt.Fprintf(&b, " · prioridade %s", is.Priority)
			}
			if is.Due != "" {
				fmt.Fprintf(&b, " · prazo %s", is.Due)
				if is.Due < today {
					b.WriteString(" (ATRASADA)")
				} else if is.Due == today {
					b.WriteString(" (HOJE)")
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if in.GitLab != nil {
		b.WriteString("## MRs\n\n")
		wrote := false
		for _, mr := range in.GitLab.ReviewPending {
			if !linkedMRs[mr.ShortRef()] {
				fmt.Fprintf(&b, "- %s aguardando a minha revisão: %s (atualizado %s)\n", mr.ShortRef(), mr.Title, mr.UpdatedAt.Format("02/01"))
				wrote = true
			}
		}
		for _, mr := range in.GitLab.Mine() {
			if !linkedMRs[mr.ShortRef()] {
				fmt.Fprintf(&b, "- %s meu, aberto: %s (atualizado %s)\n", mr.ShortRef(), mr.Title, mr.UpdatedAt.Format("02/01"))
				wrote = true
			}
		}
		if !wrote {
			b.WriteString("(nenhum fora dos workspaces)\n")
		}
		b.WriteString("\n")
	}

	var pending []tasks.Task
	for _, t := range in.Tasks {
		if !t.Done {
			pending = append(pending, t)
		}
	}
	if len(pending) > 0 {
		b.WriteString("## Tarefas locais\n\n")
		for _, t := range pending {
			fmt.Fprintf(&b, "- %s", t.Text)
			if t.Priority != "" {
				fmt.Fprintf(&b, " · prioridade %s", t.Priority)
			}
			if t.Due != "" {
				fmt.Fprintf(&b, " · prazo %s %s", t.Due, t.DueTime)
				if t.Due < today {
					b.WriteString(" (ATRASADA)")
				}
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func describeStatus(s string) string {
	switch s {
	case herdr.StatusWorking:
		return "trabalhando"
	case herdr.StatusBlocked:
		return "travado esperando por mim (permissão ou pergunta)"
	case herdr.StatusDone:
		return "terminou, resposta não lida"
	case herdr.StatusIdle:
		return "parado, pronto para receber pedido"
	}
	return "desconhecido"
}

func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

func WriteDigest(dir, digest string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, claude.DigestFile), []byte(digest), 0o644)
}

func ReadReply(dir string, after time.Time) (string, time.Time, bool) {
	path := filepath.Join(dir, claude.ReplyFile)
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().After(after) {
		return "", time.Time{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return "", time.Time{}, false
	}
	return string(data), info.ModTime(), true
}
