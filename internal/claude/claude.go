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

// Kill encerra a árvore de processos da sessão — o claude e o que ele
// disparou (builds, git…) — ou impede que comece, se ainda não começou.
// É seguro chamar de outra goroutine.
func (h *Handle) Kill() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.killed = true
	if h.cmd != nil && h.cmd.Process != nil {
		killTree(h.cmd.Process)
	}
}

// Killed informa se a execução foi cancelada via Kill.
func (h *Handle) Killed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.killed
}

// Opts descreve uma execução headless do Claude Code.
type Opts struct {
	Bin     string // binário do Claude Code
	Dir     string // diretório de trabalho (worktree da sessão)
	Prompt  string // prompt da fase — vai por stdin, sem limite de argv
	LogFile string // onde gravar stdout (stream-json) e stderr
	Model   string // alias ou id; vazio = default do CLI
	Resume  string // session_id para retomar uma conversa; vazio = nova
	// PermissionMode é o --permission-mode da execução. Em modo headless
	// não há quem aprove ferramenta — sem isso edições e bash são negados
	// e o pipeline trava. Vazio = default do CLI.
	PermissionMode string
}

// Run executa o Claude Code conforme o, gravando a saída em o.LogFile, e
// espera terminar. O prompt entra por stdin: linha de comando tem limite
// de tamanho (especialmente no Windows) e vaza na lista de processos.
// h (opcional) permite cancelar pelo Kill. Bloqueia até o fim.
func Run(o Opts, h *Handle) error {
	if err := os.MkdirAll(filepath.Dir(o.LogFile), 0o755); err != nil {
		return err
	}
	f, err := os.Create(o.LogFile)
	if err != nil {
		return err
	}
	defer f.Close()

	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	}
	if o.PermissionMode != "" {
		args = append(args, "--permission-mode", o.PermissionMode)
	}
	cmd := exec.Command(o.Bin, args...)
	cmd.Dir = o.Dir
	cmd.Stdin = strings.NewReader(o.Prompt)
	cmd.Stdout = f
	cmd.Stderr = f
	setSysProcAttr(cmd)
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
