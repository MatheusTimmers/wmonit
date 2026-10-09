// Package agents gerencia agents do Claude Code rodando em background
// (`claude --bg`): lista o roster do agent view, cria, retoma, para e remove
// sessões, e lê o que cada uma deixou em disco (heartbeat do mod fleet e
// transcript). Não depende do bubbletea: a UI embrulha as chamadas em tea.Cmd.
package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Live é uma sessão viva como `claude agents --json` a lista: interativa
// (aberta num terminal) ou em background.
type Live struct {
	PID       int    `json:"pid"`
	ID        string `json:"id"` // id curto (só background): o que attach/logs/stop/rm aceitam
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"` // "background" ou "interactive"
	Name      string `json:"name"`
	Status    string `json:"status"` // "busy" ou "idle"
	State     string `json:"state"`  // background: "working", "done", ...
	StartedAt int64  `json:"startedAt"`
}

// IsRunning informa se o processo da sessão está vivo; as de background já
// encerradas (listadas por --all) vêm sem pid.
func (l Live) IsRunning() bool { return l.PID != 0 }

// IsBusy informa se a sessão está trabalhando agora.
func (l Live) IsBusy() bool { return l.IsRunning() && (l.Status == "busy" || l.State == "working") }

// IsBackground informa se a sessão roda em background (attach/stop valem).
func (l Live) IsBackground() bool { return l.Kind == "background" }

// Ref é o que os subcomandos do CLI aceitam para a sessão: o id curto, ou o
// session id quando o roster não deu um.
func (l Live) Ref() string {
	if l.ID != "" {
		return l.ID
	}
	return l.SessionID
}

// LaunchOpts descreve um agent novo em background.
type LaunchOpts struct {
	Dir            string // onde o agent trabalha (worktree ou repo)
	Name           string // nome mostrado no agent view
	Prompt         string // vai por stdin: sem limite de linha de comando
	Model          string // vazio = default do usuário
	PermissionMode string // vazio = default do usuário (settings.json)
}

// CLI fala com o binário do Claude Code.
type CLI struct {
	Bin string
}

const cliTimeout = 30 * time.Second

func (c CLI) run(dir, stdin string, args ...string) (string, error) {
	cmd := exec.Command(c.Bin, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		text := StripANSI(out.String())
		if err != nil {
			return text, fmt.Errorf("claude %s: %w: %s", args[0], err, firstLine(text))
		}
		return text, nil
	case <-time.After(cliTimeout):
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("claude %s: sem resposta em %s", args[0], cliTimeout)
	}
}

// List devolve as sessões vivas e as de background já encerradas (--all).
func (c CLI) List() ([]Live, error) {
	out, err := c.run("", "", "agents", "--json", "--all")
	if err != nil {
		return nil, err
	}
	return ParseList(out)
}

// ParseList lê a saída de `claude agents --json`.
func ParseList(out string) ([]Live, error) {
	var list []Live
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &list); err != nil {
		return nil, fmt.Errorf("claude agents --json: %w", err)
	}
	return list, nil
}

// Launch cria o agent em background e devolve o id curto que o CLI imprimiu.
func (c CLI) Launch(o LaunchOpts) (string, error) {
	args := []string{"--bg", "--name", o.Name}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.PermissionMode != "" {
		args = append(args, "--permission-mode", o.PermissionMode)
	}
	out, err := c.run(o.Dir, o.Prompt, args...)
	if err != nil {
		return "", err
	}
	return ParseLaunched(out)
}

// Resume volta a rodar em background uma conversa parada, sob o mesmo id.
func (c CLI) Resume(dir, sessionID string) (string, error) {
	out, err := c.run(dir, "", "--bg", "--resume", sessionID)
	if err != nil {
		return "", err
	}
	return ParseLaunched(out)
}

// Stop para a sessão; a conversa fica (Resume/attach a retomam).
func (c CLI) Stop(ref string) error {
	_, err := c.run("", "", "stop", ref)
	return err
}

// Remove apaga a sessão de background do agent view.
func (c CLI) Remove(ref string) error {
	_, err := c.run("", "", "rm", ref)
	return err
}

// AttachCmd abre a sessão de background neste terminal.
func (c CLI) AttachCmd(ref string) *exec.Cmd {
	return exec.Command(c.Bin, "attach", ref)
}

// InteractiveCmd abre o Claude interativo na pasta, retomando a conversa
// quando houver uma.
func (c CLI) InteractiveCmd(dir, sessionID string) *exec.Cmd {
	var args []string
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	cmd := exec.Command(c.Bin, args...)
	cmd.Dir = dir
	return cmd
}

// "backgrounded · ef43e5db · nome"
var launchedPattern = regexp.MustCompile(`backgrounded\s*·\s*([0-9a-f]{6,})`)

// ParseLaunched extrai o id curto da saída de `claude --bg`.
func ParseLaunched(out string) (string, error) {
	m := launchedPattern.FindStringSubmatch(StripANSI(out))
	if m == nil {
		return "", fmt.Errorf("claude --bg não devolveu um id: %s", firstLine(out))
	}
	return m[1], nil
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)

// StripANSI tira cores e controles de terminal da saída do CLI.
func StripANSI(s string) string { return ansiPattern.ReplaceAllString(s, "") }

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
