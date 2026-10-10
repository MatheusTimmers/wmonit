package hub

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

const MetaSource = "wmonit"

var TokenNames = []string{"wm_task", "wm_jira", "wm_mr", "wm_ci", "wm_git", "wm_todo"}

type MetaInput struct {
	Session    session.Session
	JiraStatus string
	MR         *gitlab.MR
	MRStatus   *gitlab.MRStatus
	Git        *worktree.Info
}

func Tokens(in MetaInput) map[string]string {
	t := map[string]string{}
	for _, n := range TokenNames {
		t[n] = ""
	}
	s := in.Session
	if s.Key != "" {
		t["wm_task"] = s.Key
	}
	if k := s.IssueKey(); k != "" && in.JiraStatus != "" {
		t["wm_jira"] = in.JiraStatus
	}
	ref := s.MRRef()
	if in.MR != nil {
		ref = in.MR.ShortRef()
	}
	if ref != "" {
		t["wm_mr"] = ref
	}
	if ci := in.MRStatus.CI(); ci != "" {
		t["wm_ci"] = ciLabel(ci)
	}
	if g := in.Git; g != nil {
		var parts []string
		if len(g.Commits) > 0 {
			parts = append(parts, fmt.Sprintf("+%d", len(g.Commits)))
		}
		if len(g.Dirty) > 0 {
			parts = append(parts, fmt.Sprintf("●%d", len(g.Dirty)))
		}
		t["wm_git"] = strings.Join(parts, " ")
	}
	if p := in.MRStatus.Problems(); len(p) > 0 {
		t["wm_todo"] = clip(strings.Join(p, " · "), 80)
	} else if in.MRStatus.Ready() {
		t["wm_todo"] = "pronto para merge"
	}
	return t
}

func ciLabel(status string) string {
	switch status {
	case "success":
		return "✓ CI"
	case "failed":
		return "✗ CI"
	case "running", "pending", "created", "preparing", "waiting_for_resource":
		return "… CI"
	}
	return status
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func TokensChanged(old, next map[string]string) map[string]string {
	diff := map[string]string{}
	for k, v := range next {
		if old[k] != v {
			diff[k] = v
		}
	}
	return diff
}

func CurrentTokens(ws map[string]string) map[string]string {
	cur := map[string]string{}
	for _, n := range TokenNames {
		cur[n] = ws[n]
	}
	return cur
}
