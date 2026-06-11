// Package worktree encapsula as operações de git worktree usadas pelas
// sessões de trabalho: criar (em branch nova ou existente), listar e
// remover com guarda de mudanças não commitadas, além de detectar os
// serviços (repositórios git) na pasta de fontes.
package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Worktree é uma entrada de `git worktree list`.
type Worktree struct {
	Path   string
	Branch string // vazio em detached HEAD
}

// git roda um comando git em dir e devolve a saída; em erro, inclui o
// stderr na mensagem para o usuário entender o que o git reclamou.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}

// DetectServices lista os repositórios git diretamente dentro de
// sourcesDir (cada subpasta com .git é um serviço), em ordem alfabética.
func DetectServices(sourcesDir string) ([]string, error) {
	entries, err := os.ReadDir(sourcesDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(sourcesDir, e.Name(), ".git")); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Add cria um worktree de repo em path. Com create=true cria a branch;
// senão usa a branch existente (local ou de origin, após um fetch).
func Add(repo, path, branch string, create bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if create {
		_, err := git(repo, "worktree", "add", "-b", branch, path)
		return err
	}
	// Branch existente: tenta atualizar do remoto, mas não falha se não der
	// (repo sem remoto, offline…) — a branch local pode bastar.
	_, _ = git(repo, "fetch", "origin", branch)
	if _, err := git(repo, "worktree", "add", path, branch); err == nil {
		return nil
	}
	// Branch só existe no remoto: cria a local rastreando origin/branch.
	_, err := git(repo, "worktree", "add", "--track", "-b", branch, path, "origin/"+branch)
	return err
}

// List devolve os worktrees de repo (inclui o principal).
func List(repo string) ([]Worktree, error) {
	out, err := git(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	var cur Worktree
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur = Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "":
			if cur.Path != "" {
				wts = append(wts, cur)
				cur = Worktree{}
			}
		}
	}
	if cur.Path != "" {
		wts = append(wts, cur)
	}
	return wts, nil
}

// HasChanges informa se o worktree tem mudanças não commitadas
// (staged, unstaged ou arquivos novos).
func HasChanges(path string) (bool, error) {
	out, err := git(path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Remove apaga o worktree em path. Sem force, recusa se houver mudanças
// não commitadas — a guarda para não perder trabalho.
func Remove(repo, path string, force bool) error {
	if !force {
		dirty, err := HasChanges(path)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("worktree %s tem mudanças não commitadas — confirme para forçar", path)
		}
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := git(repo, args...)
	return err
}

// Diff devolve o diff do worktree contra o HEAD (inclui arquivos novos
// via -N/intent-to-add não persistente: usa diff de stat + patch).
func Diff(path string) (string, error) {
	stat, err := git(path, "diff", "HEAD", "--stat")
	if err != nil {
		return "", err
	}
	patch, err := git(path, "diff", "HEAD")
	if err != nil {
		return "", err
	}
	untracked, _ := git(path, "ls-files", "--others", "--exclude-standard")
	var b strings.Builder
	if u := strings.TrimSpace(untracked); u != "" {
		b.WriteString("Arquivos novos (untracked):\n")
		for _, f := range strings.Split(u, "\n") {
			b.WriteString("  " + f + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(stat)
	b.WriteString("\n")
	b.WriteString(patch)
	return b.String(), nil
}
