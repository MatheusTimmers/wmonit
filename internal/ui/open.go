package ui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/config"
)

// editorClosedMsg chega quando o editor aberto sobre um diretório/arquivo
// (sem retorno de conteúdo, como abrir o worktree) fechou e a TUI voltou.
type editorClosedMsg struct{ err error }

// descEditedMsg chega quando o editor da explicação da sessão fechou,
// trazendo o texto editado de volta para o textbox.
type descEditedMsg struct {
	text string
	err  error
}

// openURLCmd abre uma URL no navegador padrão sem bloquear a UI.
func openURLCmd(u string) tea.Cmd {
	return func() tea.Msg {
		if u != "" {
			_ = openURL(u)
		}
		return nil
	}
}

// editorCommand resolve o editor (config tem prioridade, depois $VISUAL,
// $EDITOR e por fim nvim) e monta o exec.Command para abrir os caminhos.
// O editor pode vir com flags (ex.: "code -w"), por isso o split.
func editorCommand(cfg config.Config, paths ...string) *exec.Cmd {
	bin := cfg.Editor.Bin
	if bin == "" {
		bin = os.Getenv("VISUAL")
	}
	if bin == "" {
		bin = os.Getenv("EDITOR")
	}
	if bin == "" {
		bin = "nvim"
	}
	fields := strings.Fields(bin)
	args := append(fields[1:], paths...)
	return exec.Command(fields[0], args...)
}

// openWorkspaceCmd abre a pasta no editor de workspace (VS Code por padrão)
// sem suspender a TUI: o editor é gráfico e segue sozinho.
func openWorkspaceCmd(cfg config.Config, dir string) tea.Cmd {
	return func() tea.Msg {
		fields := strings.Fields(cfg.Editor.Workspace)
		if len(fields) == 0 {
			return editorClosedMsg{err: fmt.Errorf("editor.workspace vazio no config.toml")}
		}
		cmd := exec.Command(fields[0], append(fields[1:], dir)...)
		if err := cmd.Start(); err != nil {
			return editorClosedMsg{err: err}
		}
		go func() { _ = cmd.Wait() }() // o launcher do code sai logo; não deixa zumbi
		return nil
	}
}

// externalTerminal monta o comando que abre cmd numa aba nova do terminal
// externo (Windows Terminal: `wt -w 0 new-tab`, na janela atual). nil quando
// não há terminal configurado.
func externalTerminal(cfg config.Config, title string, cmd *exec.Cmd) *exec.Cmd {
	if cfg.Editor.Terminal == "" || cmd == nil {
		return nil
	}
	// O wt lê ';' como separador de comandos: não pode aparecer no título.
	title = strings.NewReplacer(";", ",", `"`, "'").Replace(title)
	args := []string{"-w", "0", "new-tab", "--title", title}
	if cmd.Dir != "" {
		args = append(args, "-d", cmd.Dir)
	}
	return exec.Command(cfg.Editor.Terminal, append(args, cmd.Args...)...)
}

// editNoteCmd escreve o texto atual num arquivo temporário, abre o editor
// sobre ele (suspendendo a TUI) e relê o conteúdo ao fechar — a ida e volta
// de leitura/escrita da explicação no nvim.
func editNoteCmd(cfg config.Config, initial string) tea.Cmd {
	f, err := os.CreateTemp("", "wmonit-note-*.md")
	if err != nil {
		return func() tea.Msg { return descEditedMsg{err: err} }
	}
	name := f.Name()
	_, werr := f.WriteString(initial)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		err := werr
		if err == nil {
			err = cerr
		}
		return func() tea.Msg { return descEditedMsg{err: err} }
	}
	cmd := editorCommand(cfg, name)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(name)
		if err != nil {
			return descEditedMsg{err: err}
		}
		b, rerr := os.ReadFile(name)
		return descEditedMsg{text: string(b), err: rerr}
	})
}

func openURL(u string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		return exec.Command("open", u).Start()
	default:
		return exec.Command("xdg-open", u).Start()
	}
}
