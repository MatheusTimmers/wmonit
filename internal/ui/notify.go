package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gen2brain/beeep"
)

// notifyCmd dispara uma notificação de desktop sem bloquear a UI. O beeep
// usa o toast nativo no Windows e o notify-send no Linux; falhas são
// silenciosas (ambiente sem suporte a notificação não deve quebrar o app).
func notifyCmd(title, body string) tea.Cmd {
	return func() tea.Msg {
		_ = beeep.Notify(title, body, "")
		return nil
	}
}
