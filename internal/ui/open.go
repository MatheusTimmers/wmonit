package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/config"
)

// descEditedMsg chega quando o editor do texto (explicação de um workspace
// novo ou mensagem ao Claude) fechou, trazendo o texto de volta ao textbox.
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
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
