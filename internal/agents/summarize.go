package agents

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SummaryInput é o que o analisador lê de um agent: a tarefa, onde trabalha
// e as últimas respostas.
type SummaryInput struct {
	Task    string   // chave/título/explicação, ou o nome no agent view
	Dir     string   // pasta do agent (git status/log saem daqui)
	Recent  []string // últimas respostas do agent, a mais nova no fim
	Running bool     // agent trabalhando agora
}

// Summary é a leitura rápida do haiku: um nome para o agent e o que falta.
type Summary struct {
	Name    string
	Missing string
	At      time.Time
}

const (
	summaryModel   = "haiku"
	summaryTimeout = 90 * time.Second
	replyLimit     = 1200
)

// Summarize pede ao haiku uma leitura por cima do agent. Roda o CLI enxuto:
// sem ferramentas, sem settings do usuário (hooks/plugins) e sem gravar a
// sessão, fora de qualquer repositório.
func (c CLI) Summarize(in SummaryInput) (Summary, error) {
	cmd := exec.Command(c.Bin, "-p", "--model", summaryModel, "--tools", "",
		"--setting-sources", "", "--no-session-persistence")
	cmd.Dir = os.TempDir()
	cmd.Env = withoutEnv(os.Environ(), "CLAUDE_CODE_PLUGIN_DIRS")
	cmd.Stdin = strings.NewReader(SummaryPrompt(in, gitState(in.Dir)))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return Summary{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return Summary{}, fmt.Errorf("haiku: %w: %s", err, firstLine(StripANSI(out.String())))
		}
	case <-time.After(summaryTimeout):
		_ = cmd.Process.Kill()
		return Summary{}, fmt.Errorf("haiku: sem resposta em %s", summaryTimeout)
	}
	s, ok := ParseSummary(out.String())
	if !ok {
		return Summary{}, fmt.Errorf("haiku: resposta fora do formato: %s", firstLine(out.String()))
	}
	s.At = time.Now()
	return s, nil
}

// SummaryPrompt monta o pedido ao haiku.
func SummaryPrompt(in SummaryInput, git string) string {
	var b strings.Builder
	b.WriteString("Analise BEM por cima o estado de um agent de código que trabalha numa tarefa.\n\n")
	b.WriteString("Tarefa: " + strings.TrimSpace(in.Task) + "\n")
	if in.Running {
		b.WriteString("Estado: trabalhando agora.\n")
	} else {
		b.WriteString("Estado: parado (terminou ou aguarda o usuário).\n")
	}
	if g := strings.TrimSpace(git); g != "" {
		b.WriteString("\nGit:\n" + g + "\n")
	}
	if len(in.Recent) > 0 {
		b.WriteString("\nÚltimas respostas do agent (a mais nova por último):\n")
		for _, r := range in.Recent {
			r = strings.TrimSpace(r)
			if len([]rune(r)) > replyLimit {
				r = string([]rune(r)[:replyLimit]) + "…"
			}
			b.WriteString("---\n" + r + "\n")
		}
	}
	b.WriteString("\nResponda SÓ estas duas linhas, sem mais nada:\n")
	b.WriteString("NOME: <nome curto e específico do trabalho, até 5 palavras, sem o número do ticket>\n")
	b.WriteString("FALTA: <o que falta para concluir, até 20 palavras; \"nada\" se terminou>\n")
	return b.String()
}

// ParseSummary acha as linhas NOME:/FALTA: em qualquer ponto da resposta
// (o modelo às vezes enfeita com markdown ou texto antes).
func ParseSummary(out string) (Summary, bool) {
	var s Summary
	for _, line := range strings.Split(StripANSI(out), "\n") {
		l := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "*_`>-"))
		l = strings.ReplaceAll(l, "**", "")
		upper := strings.ToUpper(l)
		switch {
		case strings.HasPrefix(upper, "NOME:"):
			s.Name = strings.TrimSpace(l[len("NOME:"):])
		case strings.HasPrefix(upper, "FALTA:"):
			s.Missing = strings.TrimSpace(l[len("FALTA:"):])
		}
	}
	s.Name = strings.Trim(s.Name, `"'`)
	return s, s.Name != "" && s.Missing != ""
}

// gitState resume branch, mudanças e últimos commits da pasta; vazio fora de
// um repositório.
func gitState(dir string) string {
	if dir == "" {
		return ""
	}
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	var b strings.Builder
	if br := run("branch", "--show-current"); br != "" {
		b.WriteString("branch " + br + "\n")
	}
	if st := run("status", "--short"); st != "" {
		lines := strings.Split(st, "\n")
		if len(lines) > 30 {
			lines = append(lines[:30], fmt.Sprintf("… +%d arquivos", len(lines)-30))
		}
		b.WriteString("mudanças não commitadas:\n" + strings.Join(lines, "\n") + "\n")
	}
	if lg := run("log", "--oneline", "-5"); lg != "" {
		b.WriteString("últimos commits:\n" + lg + "\n")
	}
	return b.String()
}

func withoutEnv(env []string, name string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(strings.ToUpper(kv), strings.ToUpper(name)+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// RequestRename deixa um pedido de nome para a sessão; o mod fleet, dentro
// dela, o aplica com /rename (o CLI não renomeia sessões de fora).
func RequestRename(claudeDir, sessionID, name string) error {
	dir := filepath.Join(claudeDir, "fleet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sessionID+".rename"), []byte(name), 0o644)
}
