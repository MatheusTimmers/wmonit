package agents

import (
	"fmt"
	"sort"
	"strings"
)

// Member é o mínimo de uma sessão para checar colisões entre agents.
type Member struct {
	Name   string
	Ticket string
	Repo   string
	Root   string // worktree em que trabalha
	Files  []string
}

// Collisions aponta o que dois agents fazem em cima um do outro: a mesma
// tarefa em dois worktrees, dois agents no mesmo worktree e o mesmo arquivo
// editado por mais de um (no mesmo repo).
func Collisions(ms []Member) []string {
	var out []string

	byTicket := map[string][]Member{}
	byRoot := map[string][]Member{}
	byFile := map[string][]Member{}
	for _, m := range ms {
		if m.Ticket != "" {
			byTicket[m.Ticket] = append(byTicket[m.Ticket], m)
		}
		if m.Root != "" {
			root := strings.ToLower(strings.ReplaceAll(m.Root, `\`, "/"))
			byRoot[root] = append(byRoot[root], m)
		}
		for _, f := range m.Files {
			key := m.Repo + ":" + f
			byFile[key] = append(byFile[key], m)
		}
	}

	for _, ticket := range sortedKeys(byTicket) {
		roots := map[string]bool{}
		for _, m := range byTicket[ticket] {
			roots[strings.ToLower(m.Root)] = true
		}
		if len(roots) > 1 {
			out = append(out, fmt.Sprintf("%s aberta em %d worktrees: %s", ticket, len(roots), names(byTicket[ticket])))
		}
	}
	for _, root := range sortedKeys(byRoot) {
		if g := byRoot[root]; len(g) > 1 {
			out = append(out, fmt.Sprintf("%d agents no mesmo worktree %s: %s", len(g), g[0].Root, names(g)))
		}
	}
	for _, key := range sortedKeys(byFile) {
		if g := byFile[key]; len(g) > 1 {
			out = append(out, fmt.Sprintf("%s editado por %s", key, names(g)))
		}
	}
	return out
}

func names(ms []Member) string {
	var n []string
	for _, m := range ms {
		n = append(n, m.Name)
	}
	return strings.Join(n, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
