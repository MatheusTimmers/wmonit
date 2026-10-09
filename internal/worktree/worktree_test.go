package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo cria um repositório git com um commit inicial para os testes.
func newRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		if _, err := git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
}

func TestDetectServices(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "hades")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	newRepo(t, repo)
	// Pastas sem .git e ocultas não contam.
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.MkdirAll(filepath.Join(root, ".worktrees"), 0o755)

	names, err := DetectServices(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "hades" {
		t.Fatalf("DetectServices = %v, esperado [hades]", names)
	}
}

func TestAddListRemove(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	os.MkdirAll(repo, 0o755)
	newRepo(t, repo)

	wt := filepath.Join(root, ".worktrees", "ABC-123")
	if err := Add(repo, wt, "feature/ABC-123", true); err != nil {
		t.Fatal(err)
	}
	wts, err := List(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("List = %d worktrees, esperado 2", len(wts))
	}
	found := false
	for _, w := range wts {
		if w.Branch == "feature/ABC-123" {
			found = true
		}
	}
	if !found {
		t.Fatalf("branch feature/ABC-123 não encontrada em %v", wts)
	}

	// Com mudanças não commitadas, Remove sem force deve recusar.
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := HasChanges(wt)
	if err != nil || !dirty {
		t.Fatalf("HasChanges = %v, %v; esperado true", dirty, err)
	}
	if err := Remove(repo, wt, false); err == nil {
		t.Fatal("Remove sem force deveria recusar worktree sujo")
	}
	if err := Remove(repo, wt, true); err != nil {
		t.Fatal(err)
	}
}

// Pedir create para uma branch que já existe (sessão anterior da mesma
// issue) deve reaproveitá-la em vez de falhar no -b.
func TestAddCreateReusesExistingBranch(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	os.MkdirAll(repo, 0o755)
	newRepo(t, repo)
	if _, err := git(repo, "branch", "feature/ABC-1"); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(root, ".worktrees", "ABC-1")
	if err := Add(repo, wt, "feature/ABC-1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "a.txt")); err != nil {
		t.Fatal("worktree sem os arquivos do repo:", err)
	}
}

func TestAddExistingBranch(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	os.MkdirAll(repo, 0o755)
	newRepo(t, repo)
	if _, err := git(repo, "branch", "feature/X-1"); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(root, ".worktrees", "X-1")
	if err := Add(repo, wt, "feature/X-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "a.txt")); err != nil {
		t.Fatal("worktree sem os arquivos do repo:", err)
	}
}

func TestExcludeFiles(t *testing.T) {
	repo := t.TempDir()
	newRepo(t, repo)
	wt := filepath.Join(t.TempDir(), "wt")
	if err := Add(repo, wt, "feature/x", true); err != nil {
		t.Fatal(err)
	}
	if err := ExcludeFiles(wt, "WMONIT_PLAN.md"); err != nil {
		t.Fatal(err)
	}
	// Idempotente: segunda chamada não duplica.
	if err := ExcludeFiles(wt, "WMONIT_PLAN.md"); err != nil {
		t.Fatal(err)
	}
	// O arquivo excluído não suja o status do worktree.
	if err := os.WriteFile(filepath.Join(wt, "WMONIT_PLAN.md"), []byte("plano\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := HasChanges(wt)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Errorf("worktree sujo: o arquivo de plano deveria estar excluído do status")
	}
	// E um arquivo normal continua contando como mudança.
	if err := os.WriteFile(filepath.Join(wt, "novo.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, _ = HasChanges(wt); !dirty {
		t.Errorf("mudança real não detectada após o exclude")
	}
}

// O diff mostra o que a branch traz desde a base — commits e o que ainda não
// foi commitado —, não só o que está fora do último commit.
func TestDiffAgainstBase(t *testing.T) {
	repo := t.TempDir()
	newRepo(t, repo)
	for _, args := range [][]string{{"checkout", "-q", "-b", "ROT-1"}} {
		if _, err := git(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("commitado\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(repo, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := git(repo, "commit", "-q", "-m", "adiciona b"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\nnao commitado\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Diff(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Comparando com main", "adiciona b", "+commitado", "+nao commitado"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff sem %q:\n%s", want, d)
		}
	}
}

func TestInspect(t *testing.T) {
	repo := t.TempDir()
	newRepo(t, repo)
	if _, err := git(repo, "checkout", "-q", "-b", "ROT-2"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(repo, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := git(repo, "commit", "-q", "-m", "adiciona c"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "d.txt"), []byte("d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(repo)
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "ROT-2" || info.Base != "main" {
		t.Fatalf("branch/base = %q/%q", info.Branch, info.Base)
	}
	if len(info.Commits) != 1 || !strings.Contains(info.Commits[0], "adiciona c") {
		t.Errorf("commits = %v", info.Commits)
	}
	if len(info.Dirty) != 1 || !strings.Contains(info.Dirty[0], "d.txt") {
		t.Errorf("dirty = %v", info.Dirty)
	}
}
