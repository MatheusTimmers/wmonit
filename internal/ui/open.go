package ui

import (
	"os"
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
)

// openURLCmd abre uma URL no navegador padrão sem bloquear a UI.
func openURLCmd(u string) tea.Cmd {
	return func() tea.Msg {
		if u != "" {
			_ = openURL(u)
		}
		return nil
	}
}

// openEditorCmd abre um diretório no editor: $VISUAL/$EDITOR gráfico se
// definido, senão tenta o VS Code e por fim o gerenciador de arquivos.
func openEditorCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		if ed := os.Getenv("VISUAL"); ed != "" {
			if exec.Command(ed, dir).Start() == nil {
				return nil
			}
		}
		if exec.Command("code", dir).Start() == nil {
			return nil
		}
		_ = openURL(dir)
		return nil
	}
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
