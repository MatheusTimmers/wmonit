package ui

import (
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
