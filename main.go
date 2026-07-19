package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/tasks"
	"github.com/timmers/wmonit/internal/ui"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wmonit:", err)
		os.Exit(1)
	}

	store, err := tasks.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wmonit:", err)
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(cfg, store), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "wmonit:", err)
		os.Exit(1)
	}
}
