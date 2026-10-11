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
	"time"
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

// Add cria um worktree de repo em path. Com create=true cria a branch a
// partir do default do remoto (origin/HEAD) atualizado — não do HEAD
// local, que pode estar em qualquer branch/estado; se a branch já existir
// (ex.: sessão anterior da mesma issue), ela é reaproveitada. Senão usa a
// branch existente (local ou de origin, após um fetch).
func Add(repo, path, branch string, create bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if create {
		if _, err := git(repo, "rev-parse", "--verify", "refs/heads/"+branch); err == nil {
			create = false // branch já existe — reaproveita em vez de falhar no -b
		}
	}
	if create {
		base := "HEAD" // repo sem remoto (ou offline): cria do HEAD mesmo
		if _, err := git(repo, "fetch", "origin"); err == nil {
			out, err := git(repo, "rev-parse", "--abbrev-ref", "origin/HEAD")
			if err != nil {
				// origin/HEAD não definido localmente: pergunta ao remoto.
				_, _ = git(repo, "remote", "set-head", "origin", "--auto")
				out, err = git(repo, "rev-parse", "--abbrev-ref", "origin/HEAD")
			}
			if err == nil {
				base = strings.TrimSpace(out)
			}
		}
		_, err := git(repo, "worktree", "add", "-b", branch, path, base)
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

// ExcludeFiles adiciona nomes ao info/exclude do worktree (ignore local,
// não versionado) — usado para o arquivo de plano do pipeline não sujar o
// status nem ir parar num commit.
func ExcludeFiles(path string, names ...string) error {
	out, err := git(path, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	exclude := strings.TrimSpace(out)
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(path, exclude)
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	existing, _ := os.ReadFile(exclude)
	lines := map[string]bool{}
	for _, l := range strings.Split(string(existing), "\n") {
		lines[strings.TrimSpace(l)] = true
	}
	var add []string
	for _, n := range names {
		if !lines[n] {
			add = append(add, n)
		}
	}
	if len(add) == 0 {
		return nil
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.Join(add, "\n") + "\n")
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

// Diff devolve tudo o que a branch do worktree traz: os commits desde que
// saiu da branch base e o diff dela até o estado atual (commitado ou não),
// mais os arquivos novos ainda fora do git. Sem base conhecida, cai no diff
// contra o HEAD (só o que não foi commitado).
func Diff(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("sem pasta")
	}
	from := "HEAD"
	base := baseBranch(path)
	if base != "" {
		if mb, err := git(path, "merge-base", "HEAD", base); err == nil {
			from = strings.TrimSpace(mb)
		}
	}
	stat, err := git(path, "diff", from, "--stat")
	if err != nil {
		return "", err
	}
	patch, err := git(path, "diff", from)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if from != "HEAD" {
		b.WriteString("Comparando com " + base + "\n\n")
		if log, _ := git(path, "log", "--oneline", from+"..HEAD"); strings.TrimSpace(log) != "" {
			b.WriteString("Commits da branch:\n")
			for _, l := range strings.Split(strings.TrimSpace(log), "\n") {
				b.WriteString("  " + l + "\n")
			}
			b.WriteString("\n")
		}
	}
	untracked, _ := git(path, "ls-files", "--others", "--exclude-standard")
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

// baseBranch acha a branch de onde o worktree saiu: o default do remoto e,
// sem ele, as de integração usuais (remota antes da local). A própria branch
// do worktree não serve de base.
func baseBranch(path string) string {
	current, _ := git(path, "branch", "--show-current")
	current = strings.TrimSpace(current)
	if out, err := git(path, "rev-parse", "--abbrev-ref", "origin/HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "" && b != "origin/"+current {
			return b
		}
	}
	for _, b := range []string{"origin/develop", "origin/master", "origin/main", "develop", "master", "main"} {
		if b == current || b == "origin/"+current {
			continue
		}
		if _, err := git(path, "rev-parse", "--verify", "--quiet", b); err == nil {
			return b
		}
	}
	return ""
}

type Info struct {
	Branch     string
	Base       string
	Commits    []string
	Dirty      []string
	Upstream   string
	Ahead      int
	Behind     int
	LastCommit string
	LastAt     time.Time
}

func Inspect(path string) (Info, error) {
	if path == "" {
		return Info{}, fmt.Errorf("sem pasta")
	}
	branch, err := git(path, "branch", "--show-current")
	if err != nil {
		return Info{}, err
	}
	info := Info{Branch: strings.TrimSpace(branch), Base: baseBranch(path)}
	if info.Base != "" {
		if log, err := git(path, "log", "--oneline", "-n", "20", info.Base+"..HEAD"); err == nil {
			info.Commits = nonEmptyLines(log)
		}
	}
	if status, err := git(path, "status", "--porcelain"); err == nil {
		info.Dirty = nonEmptyLines(status)
	}
	if up, err := git(path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		info.Upstream = strings.TrimSpace(up)
		if counts, err := git(path, "rev-list", "--left-right", "--count", "@{u}...HEAD"); err == nil {
			_, _ = fmt.Sscanf(strings.TrimSpace(counts), "%d %d", &info.Behind, &info.Ahead)
		}
	}
	if last, err := git(path, "log", "-1", "--format=%ct %h %s"); err == nil {
		var ts int64
		var rest string
		if n, _ := fmt.Sscanf(strings.TrimSpace(last), "%d", &ts); n == 1 {
			info.LastAt = time.Unix(ts, 0)
			if _, after, ok := strings.Cut(strings.TrimSpace(last), " "); ok {
				rest = after
			}
			info.LastCommit = rest
		}
	}
	return info, nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimRight(l, "\r"))
		}
	}
	return out
}

func Push(path, branch string) error {
	_, err := git(path, "push", "-u", "origin", branch)
	return err
}

func RemoteURL(path string) (string, error) {
	out, err := git(path, "config", "--get", "remote.origin.url")
	return strings.TrimSpace(out), err
}

func BranchExists(repo, branch string) bool {
	_, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}
