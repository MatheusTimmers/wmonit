package hub

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type fakeGitLab struct{ got gitlab.NewMR }

func (f *fakeGitLab) CreateMR(_ context.Context, n gitlab.NewMR) (*gitlab.MR, error) {
	f.got = n
	mr := gitlab.MR{IID: 7, WebURL: "https://git/x/-/merge_requests/7"}
	mr.References.Full = "grupo/svc!7"
	return &mr, nil
}

type fakeJira struct{ key, to string }

func (f *fakeJira) TransitionTo(_ context.Context, key, target string) (string, error) {
	f.key, f.to = key, target
	return target, nil
}

func TestShip(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	run(t, root, "git", "init", "-q", "--bare", "-b", "main", remote)
	repo := filepath.Join(root, "svc")
	run(t, root, "git", "clone", "-q", remote, repo)
	run(t, repo, "git", "config", "user.email", "t@t")
	run(t, repo, "git", "config", "user.name", "t")
	run(t, repo, "git", "commit", "-q", "--allow-empty", "-m", "init")
	run(t, repo, "git", "push", "-q", "origin", "main")
	run(t, repo, "git", "remote", "set-head", "origin", "--auto")
	run(t, repo, "git", "config", "url."+remote+".insteadOf", "git@git.exemplo.com:grupo/svc.git")
	run(t, repo, "git", "remote", "set-url", "origin", "git@git.exemplo.com:grupo/svc.git")
	run(t, repo, "git", "checkout", "-q", "-b", "feature/ABC-1")

	sess := session.Session{Key: "ABC-1", Issue: "ABC-1", Title: "arrumar login", URL: "https://jira/browse/ABC-1", UserNote: "cuidado com o SSO", Repo: repo, Worktree: repo, Kind: session.KindIssue, Status: session.StatusActive}
	gl, ji := &fakeGitLab{}, &fakeJira{}
	opts := ShipOpts{GitLab: gl, Jira: ji, ReviewTransition: "Revisão 1"}

	if _, err := Ship(context.Background(), sess, opts); err == nil || !strings.Contains(err.Error(), "nenhum commit") {
		t.Fatalf("sem commits deveria recusar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Ship(context.Background(), sess, opts); err == nil || !strings.Contains(err.Error(), "não commitado") {
		t.Fatalf("com sujeira deveria recusar: %v", err)
	}
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-q", "-m", "login")

	res, err := Ship(context.Background(), sess, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "feature/ABC-1" || !res.Created || res.MR.ShortRef() != "svc!7" || res.MovedTo != "Revisão 1" || len(res.Warnings) != 0 {
		t.Fatalf("resultado = %+v", res)
	}
	if out := run(t, root, "git", "--git-dir", remote, "branch", "--list", "feature/ABC-1"); !strings.Contains(out, "feature/ABC-1") {
		t.Fatal("a branch não chegou no remoto")
	}
	if gl.got.Project != "grupo/svc" {
		t.Errorf("projeto = %q", gl.got.Project)
	}
	if gl.got.Title != "arrumar login #ABC-1" || !strings.Contains(gl.got.Description, "cuidado com o SSO") || gl.got.SourceBranch != "feature/ABC-1" {
		t.Fatalf("MR pedido = %+v", gl.got)
	}
	if ji.key != "ABC-1" || ji.to != "Revisão 1" {
		t.Fatalf("jira = %+v", ji)
	}

	existing := res.MR
	gl2 := &fakeGitLab{}
	res, err = Ship(context.Background(), sess, ShipOpts{GitLab: gl2, ExistingMR: existing})
	if err != nil || res.Created || gl2.got.Title != "" {
		t.Fatalf("com MR existente não deveria criar outro: %+v %v", res, err)
	}

	review := sess
	review.Mode = session.ModeReview
	if _, err := Ship(context.Background(), review, opts); err == nil {
		t.Fatal("revisão não entrega")
	}
}

func TestStale(t *testing.T) {
	merged := gitlab.MR{IID: 1}
	merged.References.Full = "g/svc!1"
	open := gitlab.MR{IID: 2, Title: "algo #ABC-3"}
	open.References.Full = "g/svc!2"
	gl := &gitlab.Summary{Merged: []gitlab.MR{merged}, OpenMRs: []gitlab.MR{open}}
	ji := &jira.Summary{Resolved: []jira.Issue{{Key: "ABC-2"}, {Key: "ABC-3"}}}
	sessions := []session.Session{
		{ID: "a", MR: "svc!1", Status: session.StatusActive},
		{ID: "b", Issue: "ABC-2", Kind: session.KindIssue, Status: session.StatusActive},
		{ID: "c", Issue: "ABC-3", Kind: session.KindIssue, Status: session.StatusActive},
		{ID: "d", MR: "svc!9", Status: session.StatusActive},
		{ID: "e", MR: "svc!1", Status: session.StatusCompleted},
		{ID: "f", MR: "svc!8", Status: session.StatusActive},
	}
	statuses := map[string]*gitlab.MRStatus{"svc!8": {State: "closed"}}
	got := Stale(sessions, gl, ji, statuses)
	var ids []string
	for _, it := range got {
		ids = append(ids, it.Session.ID+":"+it.Reason)
	}
	want := "a:MR svc!1 mergeado,b:issue ABC-2 resolvida,f:MR svc!8 fechado"
	if strings.Join(ids, ",") != want {
		t.Fatalf("stale = %v", ids)
	}
}

func TestTokens(t *testing.T) {
	mr := gitlab.MR{IID: 5}
	mr.References.Full = "g/svc!5"
	in := MetaInput{
		Session:    session.Session{Key: "ABC-1", Issue: "ABC-1", Kind: session.KindIssue},
		JiraStatus: "Em Andamento",
		MR:         &mr,
		MRStatus:   &gitlab.MRStatus{State: "opened", HeadPipeline: &gitlab.Pipeline{Status: "failed"}, FailedJobs: []string{"lint"}, BlockingDiscussionsResolved: true},
	}
	tok := Tokens(in)
	if tok["wm_task"] != "ABC-1" || tok["wm_jira"] != "Em Andamento" || tok["wm_mr"] != "svc!5" || tok["wm_ci"] != "✗ CI" || tok["wm_todo"] != "CI falhou (lint)" || tok["wm_git"] != "" {
		t.Fatalf("tokens = %v", tok)
	}
	diff := TokensChanged(CurrentTokens(map[string]string{"wm_task": "ABC-1", "wm_git": "+1", "outro": "x"}), tok)
	if _, ok := diff["wm_task"]; ok {
		t.Errorf("wm_task não mudou: %v", diff)
	}
	if v, ok := diff["wm_git"]; !ok || v != "" {
		t.Errorf("wm_git deveria ser limpo: %v", diff)
	}
	if _, ok := diff["outro"]; ok {
		t.Errorf("token de outra fonte não é nosso: %v", diff)
	}
	ready := Tokens(MetaInput{Session: session.Session{MR: "svc!5"}, MRStatus: &gitlab.MRStatus{State: "opened", DetailedMergeStatus: "mergeable", BlockingDiscussionsResolved: true, HeadPipeline: &gitlab.Pipeline{Status: "success"}}})
	if ready["wm_todo"] != "pronto para merge" || ready["wm_ci"] != "✓ CI" {
		t.Fatalf("pronto = %v", ready)
	}
}
