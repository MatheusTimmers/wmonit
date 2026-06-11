// Package claude monta o prompt e executa o Claude Code em modo headless
// (claude -p --output-format stream-json) dentro de um worktree, gravando
// a saída num arquivo de log que a UI acompanha.
package claude

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Handle permite cancelar uma execução em andamento a partir da UI,
// enquanto o Run bloqueia em outra goroutine.
type Handle struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	killed bool
}

func (h *Handle) set(cmd *exec.Cmd) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.killed {
		return false
	}
	h.cmd = cmd
	return true
}

// Kill encerra o processo da sessão (ou impede que comece, se ainda não
// começou). É seguro chamar de outra goroutine.
func (h *Handle) Kill() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.killed = true
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
}

// Killed informa se a execução foi cancelada via Kill.
func (h *Handle) Killed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.killed
}

// BuildPrompt monta o prompt da sessão a partir da task e do template do
// serviço (instruções extras por repositório, do config).
func BuildPrompt(key, title, url, description, template string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Você está trabalhando na tarefa %s: %s\n", key, title)
	if url != "" {
		fmt.Fprintf(&b, "Link: %s\n", url)
	}
	b.WriteString("\n")
	if d := strings.TrimSpace(description); d != "" {
		b.WriteString("Descrição da tarefa:\n")
		b.WriteString(d)
		b.WriteString("\n\n")
	}
	b.WriteString("Instruções:\n")
	b.WriteString("- Implemente o que a tarefa pede neste repositório (você já está na branch correta).\n")
	b.WriteString("- Siga as convenções do código existente.\n")
	b.WriteString("- Rode build e testes para validar antes de terminar.\n")
	b.WriteString("- Faça commits pequenos com mensagens claras; NÃO faça push.\n")
	b.WriteString("- Ao final, resuma o que foi feito e o que ficou pendente.\n")
	if t := strings.TrimSpace(template); t != "" {
		b.WriteString("\nInstruções específicas deste serviço:\n")
		b.WriteString(t)
		b.WriteString("\n")
	}
	return b.String()
}

// Run executa o Claude Code em dir com o prompt, gravando stdout
// (stream-json) e stderr em logFile, e espera terminar. resume, se não
// vazio, retoma uma sessão anterior do Claude. h (opcional) permite
// cancelar pelo Kill. Bloqueia até o fim.
func Run(bin, dir, prompt, logFile, resume string, h *Handle) error {
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return err
	}
	f, err := os.Create(logFile)
	if err != nil {
		return err
	}
	defer f.Close()

	args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}
	if resume != "" {
		args = append(args, "--resume", resume)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout = f
	cmd.Stderr = f
	if h != nil && !h.set(cmd) {
		return fmt.Errorf("claude: cancelado antes de iniciar")
	}
	if err := cmd.Run(); err != nil {
		if h != nil && h.Killed() {
			return fmt.Errorf("claude: cancelado")
		}
		return fmt.Errorf("claude: %w", err)
	}
	return nil
}
