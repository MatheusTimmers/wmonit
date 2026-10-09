package agents

import (
	"context"
	"strings"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
)

// GitLabSource e JiraSource são o mínimo que a montagem do contexto usa das
// fontes de dados; os clients reais e as fontes do modo demo os satisfazem.
type GitLabSource interface {
	MRNotes(ctx context.Context, projectID, iid int) ([]gitlab.Note, error)
}

type JiraSource interface {
	IssueDetail(ctx context.Context, key string) (*jira.IssueDetail, error)
}

// BuildContext compõe o contexto completo da tarefa (Jira + GitLab) que vai
// no prompt do agent. Erros de rede degradam para o contexto parcial. mrs é
// a lista de MRs em memória, usada para achar o MR ligado.
func BuildContext(gl GitLabSource, ji JiraSource, template string, sess session.Session, mrs []gitlab.MR) claude.TaskContext {
	isIssue := sess.IsIssue()
	mr := mrFor(sess, mrs)
	ctx := claude.TaskContext{
		Key:       sess.Key,
		Title:     sess.Title,
		URL:       sess.URL,
		UserNote:  sess.UserNote,
		Template:  template,
		HasBranch: !isIssue, // sessão de MR usa branch existente
	}
	var notes []string
	if mr != nil {
		raw, _ := gl.MRNotes(context.Background(), mr.projectID, mr.iid)
		notes = noteLines(raw)
	}
	if isIssue {
		// Issue: descrição/comentários do Jira; o MR ligado vira contexto extra.
		if det, err := ji.IssueDetail(context.Background(), sess.Key); err == nil {
			ctx.Description = det.Description
			for _, c := range det.Comments {
				ctx.Comments = append(ctx.Comments, c.Author+": "+c.Body)
			}
		}
		if mr != nil {
			var b strings.Builder
			b.WriteString(mr.ref)
			if mr.desc != "" {
				b.WriteString("\n" + mr.desc)
			}
			for _, n := range notes {
				b.WriteString("\n- " + n)
			}
			ctx.MRInfo = b.String()
		}
	} else if mr != nil {
		// Sessão do próprio MR: descrição e comentários do MR são a tarefa.
		ctx.Description = mr.desc
		ctx.Comments = notes
	}
	return ctx
}

// mrRef aponta o MR da sessão com o que já está em memória; os comentários
// são buscados depois.
type mrRef struct {
	projectID, iid int
	ref, desc      string
}

func newMRRef(mr gitlab.MR) *mrRef {
	return &mrRef{projectID: mr.ProjectID, iid: mr.IID, ref: mr.ShortRef(), desc: mr.Description}
}

// mrFor acha o MR da sessão: pela ref exata (sessão de MR) ou pela #TAG no
// título (sessão de issue), preferindo o MR do mesmo serviço quando a issue
// tem MRs em mais de um repositório.
func mrFor(sess session.Session, mrs []gitlab.MR) *mrRef {
	for i := range mrs {
		if mrs[i].ShortRef() == sess.Key {
			return newMRRef(mrs[i])
		}
	}
	if !sess.IsIssue() {
		return nil
	}
	var fallback *mrRef
	for i := range mrs {
		if mrs[i].JiraKey() != sess.Key {
			continue
		}
		if strings.EqualFold(mrs[i].Project(), sess.Service) {
			return newMRRef(mrs[i])
		}
		if fallback == nil {
			fallback = newMRRef(mrs[i])
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
