package worktree

import (
	"os"
	"path/filepath"
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
