package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
)

type fakeGitLab struct {
	notes []gitlab.Note
}

func (f fakeGitLab) MRNotes(ctx context.Context, projectID, iid int) ([]gitlab.Note, error) {
	return f.notes, nil
}

type fakeJira struct {
	detail *jira.IssueDetail
}

func (f fakeJira) IssueDetail(ctx context.Context, key string) (*jira.IssueDetail, error) {
	return f.detail, nil
}

func note(name, body string, system bool) gitlab.Note {
	n := gitlab.Note{Body: body, System: system}
	n.Author.Name = name
	return n
}

func mr(iid, pid int, title, full string) gitlab.MR {
	m := gitlab.MR{IID: iid, ProjectID: pid, Title: title, Description: "desc de " + full}
	m.References.Full = full
	return m
}

func TestBuildContextIssue(t *testing.T) {
	gl := fakeGitLab{notes: []gitlab.Note{
		note("Ana", "olha o caso vazio", false),
		note("gitlab", "merge status changed", true), // sistema: ignorado
	}}
	ji := fakeJira{detail: &jira.IssueDetail{
		Description: "descrição da issue",
		Comments:    []jira.Comment{{Author: "PO", Body: "prioridade alta"}},
	}}
	r := New(config.Claude{Templates: map[string]string{"hades": "regras do hades"}}, gl, ji)

	sess := session.Session{Key: "ROT-501", Title: "faz X", Service: "hades", Kind: session.KindIssue}
	mrs := []gitlab.MR{mr(9471, 1, "algo #ROT-501 [feature]", "Roteamento/hades!9471")}
	ctx := r.BuildContext(sess, mrs)

	if ctx.Description != "descrição da issue" {
		t.Errorf("Description = %q", ctx.Description)
	}
	if ctx.Template != "regras do hades" {
		t.Errorf("Template = %q", ctx.Template)
	}
	if ctx.HasBranch {
		t.Error("issue não deveria ter HasBranch")
	}
	if len(ctx.Comments) != 1 || ctx.Comments[0] != "PO: prioridade alta" {
		t.Errorf("Comments = %v", ctx.Comments)
	}
	if !strings.Contains(ctx.MRInfo, "hades!9471") || !strings.Contains(ctx.MRInfo, "Ana: olha o caso vazio") {
		t.Errorf("MRInfo = %q", ctx.MRInfo)
	}
	if strings.Contains(ctx.MRInfo, "merge status changed") {
		t.Errorf("comentário de sistema vazou para MRInfo: %q", ctx.MRInfo)
	}
}

func TestBuildContextMR(t *testing.T) {
	gl := fakeGitLab{notes: []gitlab.Note{note("Rev", "ajusta isso", false)}}
	r := New(config.Claude{}, gl, fakeJira{})

	sess := session.Session{Key: "hades!9471", Title: "meu MR", Service: "hades", Kind: session.KindMR}
	mrs := []gitlab.MR{mr(9471, 1, "meu MR #ROT-501 [bug]", "Roteamento/hades!9471")}
	ctx := r.BuildContext(sess, mrs)

	if !ctx.HasBranch {
		t.Error("sessão de MR deveria ter HasBranch")
	}
	if ctx.Description != "desc de Roteamento/hades!9471" {
		t.Errorf("Description = %q", ctx.Description)
	}
	if len(ctx.Comments) != 1 || ctx.Comments[0] != "Rev: ajusta isso" {
		t.Errorf("Comments = %v", ctx.Comments)
	}
}

func TestMRForPrefersService(t *testing.T) {
	mrs := []gitlab.MR{
		mr(412, 2, "outro #ROT-512 [feature]", "Roteamento/backoffice!412"),
		mr(9480, 1, "esse #ROT-512 [feature]", "Roteamento/hades!9480"),
	}
	sess := session.Session{Key: "ROT-512", Service: "hades", Kind: session.KindIssue}
	got := mrFor(sess, mrs)
	if got == nil || got.iid != 9480 {
		t.Fatalf("mrFor devia preferir o MR do serviço hades, veio %+v", got)
	}
}

func TestMRForExactRef(t *testing.T) {
	mrs := []gitlab.MR{mr(9471, 1, "algo #ROT-501 [bug]", "Roteamento/hades!9471")}
	sess := session.Session{Key: "hades!9471", Kind: session.KindMR}
	got := mrFor(sess, mrs)
	if got == nil || got.iid != 9471 {
		t.Fatalf("mrFor devia casar pela ref exata, veio %+v", got)
	}
}
