// Package pipeline executa as fases (plan → dev → review) de uma sessão de
// trabalho do Claude Code. Concentra a lógica de negócio e o acesso a
// rede/processos que antes viviam em internal/ui, sem depender do bubbletea:
// o chamador embrulha RunPhase num tea.Cmd.
package pipeline

import (
	"context"
	"strings"
	"sync"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
)

// gitlabSource e jiraSource são o mínimo que o pipeline usa das fontes de
// dados; os clients reais e as fontes do modo demo os satisfazem.
type gitlabSource interface {
	MRNotes(ctx context.Context, projectID, iid int) ([]gitlab.Note, error)
}

type jiraSource interface {
	IssueDetail(ctx context.Context, key string) (*jira.IssueDetail, error)
}

// Runner executa fases do pipeline e guarda o estado de runtime: os handles
// dos processos vivos e o contexto de tarefa cacheado por sessão. Os mapas
// são tocados tanto pela goroutine do RunPhase quanto pela thread da UI
// (Cancel/Forget), por isso o mutex.
type Runner struct {
	cfg    config.Claude
	gitlab gitlabSource
	jira   jiraSource

	mu      sync.Mutex
	handles map[string]*claude.Handle
	taskCtx map[string]*claude.TaskContext
}

func New(cfg config.Claude, gl gitlabSource, ji jiraSource) *Runner {
	return &Runner{
		cfg:     cfg,
		gitlab:  gl,
		jira:    ji,
		handles: map[string]*claude.Handle{},
		taskCtx: map[string]*claude.TaskContext{},
	}
}

// RunPhase roda a Action da sessão (bloqueante) e devolve o prompt usado.
// Com act.ResumeID retoma aquela conversa do Claude — com o prompt de
// correção quando act.UseFix. mrs é a lista de MRs em memória usada para
// montar o contexto no primeiro fetch da sessão.
func (r *Runner) RunPhase(sess session.Session, act session.Action, mrs []gitlab.MR) (string, error) {
	opts := claude.Opts{
		Bin:            r.cfg.Bin,
		Dir:            sess.Worktree,
		LogFile:        sess.LogFile,
		Model:          r.cfg.Models[act.Phase],
		PermissionMode: r.cfg.PermissionMode,
	}
	h := &claude.Handle{}
	r.mu.Lock()
	r.handles[sess.ID] = h
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.handles, sess.ID)
		r.mu.Unlock()
	}()

	if act.ResumeID != "" {
		if act.UseFix {
			opts.Prompt = claude.FixPrompt(sess.Results[session.PhaseReview])
		} else {
			opts.Prompt = claude.ResumePrompt()
		}
		opts.Resume = act.ResumeID
		return opts.Prompt, claude.Run(opts, h)
	}

	ctx := r.context(sess, mrs)
	switch act.Phase {
	case session.PhaseDev:
		opts.Prompt = claude.DevPrompt(*ctx)
	case session.PhaseReview:
		if sess.IsReview() {
			opts.Prompt = claude.ReviewMRPrompt(*ctx)
		} else {
			opts.Prompt = claude.ReviewPrompt(*ctx)
		}
	default:
		opts.Prompt = claude.PlanPrompt(*ctx)
	}
	return opts.Prompt, claude.Run(opts, h)
}

// Cancel mata o processo da sessão, se houver um vivo.
func (r *Runner) Cancel(id string) {
	r.mu.Lock()
	h := r.handles[id]
	delete(r.handles, id)
	r.mu.Unlock()
	if h != nil {
		h.Kill()
	}
}

// Forget descarta o contexto cacheado de uma sessão que saiu de cena.
func (r *Runner) Forget(id string) {
	r.mu.Lock()
	delete(r.taskCtx, id)
	r.mu.Unlock()
}

// context devolve o contexto da tarefa, montando-o (e cacheando) no primeiro
// uso da sessão.
func (r *Runner) context(sess session.Session, mrs []gitlab.MR) *claude.TaskContext {
	r.mu.Lock()
	ctx := r.taskCtx[sess.ID]
	r.mu.Unlock()
	if ctx != nil {
		return ctx
	}
	c := r.BuildContext(sess, mrs)
	r.mu.Lock()
	r.taskCtx[sess.ID] = &c
	r.mu.Unlock()
	return &c
}

// BuildContext compõe o contexto completo da tarefa (Jira + GitLab). Erros
// de rede degradam para o contexto parcial.
func (r *Runner) BuildContext(sess session.Session, mrs []gitlab.MR) claude.TaskContext {
	isIssue := sess.IsIssue()
	mr := mrFor(sess, mrs)
	ctx := claude.TaskContext{
		Key:       sess.Key,
		Title:     sess.Title,
		URL:       sess.URL,
		UserNote:  sess.UserNote,
		Template:  r.cfg.Templates[sess.Service],
		HasBranch: !isIssue, // sessão de MR usa branch existente
	}
	var notes []string
	if mr != nil {
		raw, _ := r.gitlab.MRNotes(context.Background(), mr.projectID, mr.iid)
		notes = noteLines(raw)
	}
	if isIssue {
		// Sessão de issue: descrição/comentários do Jira; o MR ligado vira contexto extra.
		if det, err := r.jira.IssueDetail(context.Background(), sess.Key); err == nil {
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
