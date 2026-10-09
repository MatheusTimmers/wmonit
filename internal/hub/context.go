package hub

import (
	"context"
	"strings"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
)

type GitLabSource interface {
	MRNotes(ctx context.Context, projectID, iid int) ([]gitlab.Note, error)
}

type JiraSource interface {
	IssueDetail(ctx context.Context, key string) (*jira.IssueDetail, error)
}

func BuildContext(gl GitLabSource, ji JiraSource, template string, sess session.Session, mrs []gitlab.MR) claude.TaskContext {
	issue := sess.IssueKey()
	mr := FindMR(sess, mrs)
	ctx := claude.TaskContext{
		Key:       sess.Key,
		Title:     sess.Title,
		URL:       sess.URL,
		UserNote:  sess.UserNote,
		Template:  template,
		HasBranch: !sess.IsIssue() && sess.Kind != session.KindFree,
	}
	var notes []string
	if mr != nil && gl != nil {
		raw, _ := gl.MRNotes(context.Background(), mr.ProjectID, mr.IID)
		notes = noteLines(raw)
	}
	if issue != "" && ji != nil {
		if det, err := ji.IssueDetail(context.Background(), issue); err == nil {
			ctx.Description = det.Description
			for _, c := range det.Comments {
				ctx.Comments = append(ctx.Comments, c.Author+": "+c.Body)
			}
		}
	}
	if mr == nil {
		return ctx
	}
	if issue != "" {
		var b strings.Builder
		b.WriteString(mr.ShortRef())
		if mr.Description != "" {
			b.WriteString("\n" + mr.Description)
		}
		for _, n := range notes {
			b.WriteString("\n- " + n)
		}
		ctx.MRInfo = b.String()
		return ctx
	}
	ctx.Description = mr.Description
	ctx.Comments = notes
	return ctx
}

func FindMR(sess session.Session, mrs []gitlab.MR) *gitlab.MR {
	if ref := sess.MRRef(); ref != "" {
		for i := range mrs {
			if mrs[i].ShortRef() == ref {
				return &mrs[i]
			}
		}
	}
	issue := sess.IssueKey()
	if issue == "" {
		return nil
	}
	var fallback *gitlab.MR
	for i := range mrs {
		if mrs[i].JiraKey() != issue {
			continue
		}
		if strings.EqualFold(mrs[i].Project(), sess.Service) {
			return &mrs[i]
		}
		if fallback == nil {
			fallback = &mrs[i]
		}
	}
	return fallback
}

func noteLines(notes []gitlab.Note) []string {
	var out []string
	for _, n := range notes {
		if !n.System {
			out = append(out, n.Author.Name+": "+strings.TrimSpace(n.Body))
		}
	}
	return out
}

func TaskText(ctx claude.TaskContext, sess session.Session) string {
	switch {
	case sess.Kind == session.KindFree:
		return claude.FreePrompt(sess.Service, sess.UserNote)
	case sess.IsReview():
		return claude.ReviewMRPrompt(ctx)
	}
	return claude.AgentPrompt(ctx)
}
