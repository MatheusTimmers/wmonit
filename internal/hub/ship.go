package hub

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

type MRCreator interface {
	CreateMR(ctx context.Context, n gitlab.NewMR) (*gitlab.MR, error)
}

type IssueMover interface {
	TransitionTo(ctx context.Context, key, target string) (string, error)
}

type ShipOpts struct {
	GitLab           MRCreator
	Jira             IssueMover
	ReviewTransition string
	TargetBranch     string
	Draft            bool
	ExistingMR       *gitlab.MR
}

type ShipResult struct {
	Branch   string
	MR       *gitlab.MR
	Created  bool
	MovedTo  string
	Warnings []string
}

func Dir(s session.Session) string {
	if s.Worktree != "" {
		return s.Worktree
	}
	return s.Repo
}

func Ship(ctx context.Context, sess session.Session, o ShipOpts) (ShipResult, error) {
	var res ShipResult
	if sess.IsReview() {
		return res, errors.New("workspace de revisão não entrega MR")
	}
	dir := Dir(sess)
	info, err := worktree.Inspect(dir)
	if err != nil {
		return res, err
	}
	if info.Branch == "" {
		return res, errors.New("o worktree não está numa branch (HEAD destacado)")
	}
	if len(info.Dirty) > 0 {
		return res, fmt.Errorf("%d arquivo(s) não commitado(s) — commite antes (p pede ao Claude)", len(info.Dirty))
	}
	if info.Base != "" && len(info.Commits) == 0 {
		return res, fmt.Errorf("nenhum commit novo em %s desde %s", info.Branch, info.Base)
	}
	res.Branch = info.Branch
	if err := worktree.Push(dir, info.Branch); err != nil {
		return res, err
	}

	switch {
	case o.ExistingMR != nil:
		res.MR = o.ExistingMR
	case o.GitLab != nil:
		remote, _ := worktree.RemoteURL(dir)
		project := gitlab.ProjectPath(remote)
		if project == "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("branch enviada, mas não entendi o projeto do remoto %q para abrir o MR", remote))
			break
		}
		mr, err := o.GitLab.CreateMR(ctx, gitlab.NewMR{
			Project:      project,
			SourceBranch: info.Branch,
			TargetBranch: o.TargetBranch,
			Title:        MRTitle(sess),
			Description:  MRDescription(sess),
			Draft:        o.Draft,
		})
		if err != nil {
			res.Warnings = append(res.Warnings, "branch enviada, mas o MR não foi criado: "+err.Error())
		} else {
			res.MR, res.Created = mr, true
		}
	}

	if key := sess.IssueKey(); key != "" && o.Jira != nil && o.ReviewTransition != "" {
		to, err := o.Jira.TransitionTo(ctx, key, o.ReviewTransition)
		if err != nil {
			res.Warnings = append(res.Warnings, err.Error())
		} else {
			res.MovedTo = to
		}
	}
	return res, nil
}

func MRTitle(s session.Session) string {
	title := strings.TrimSpace(s.Title)
	if title == "" {
		title = s.Branch
	}
	if key := s.IssueKey(); key != "" && !strings.Contains(title, "#"+key) {
		title += " #" + key
	}
	return title
}

func MRDescription(s session.Session) string {
	var parts []string
	if s.URL != "" && s.IssueKey() != "" {
		parts = append(parts, "Issue: "+s.URL)
	}
	if n := strings.TrimSpace(s.UserNote); n != "" {
		parts = append(parts, n)
	}
	return strings.Join(parts, "\n\n")
}

type StaleItem struct {
	Session session.Session
	Reason  string
}

func Stale(sessions []session.Session, gl *gitlab.Summary, ji *jira.Summary, statuses map[string]*gitlab.MRStatus) []StaleItem {
	var out []StaleItem
	for _, s := range sessions {
		if !s.Active() {
			continue
		}
		if reason := staleReason(s, gl, ji, statuses); reason != "" {
			out = append(out, StaleItem{Session: s, Reason: reason})
		}
	}
	return out
}

func staleReason(s session.Session, gl *gitlab.Summary, ji *jira.Summary, statuses map[string]*gitlab.MRStatus) string {
	if ref := s.MRRef(); ref != "" {
		if st := statuses[ref]; st != nil {
			switch st.State {
			case "merged":
				return "MR " + ref + " mergeado"
			case "closed":
				return "MR " + ref + " fechado"
			}
		}
		if gl != nil {
			for _, mr := range gl.Merged {
				if mr.ShortRef() == ref {
					return "MR " + ref + " mergeado"
				}
			}
		}
	}
	key := s.IssueKey()
	if key == "" || ji == nil {
		return ""
	}
	if gl != nil {
		for _, mr := range gl.OpenMRs {
			if mr.JiraKey() == key {
				return ""
			}
		}
	}
	for _, is := range ji.Resolved {
		if is.Key == key {
			return "issue " + key + " resolvida"
		}
	}
	return ""
}
