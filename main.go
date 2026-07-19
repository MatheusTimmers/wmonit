package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/demo"
	"github.com/timmers/wmonit/internal/tasks"
	"github.com/timmers/wmonit/internal/ui"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wmonit:", err)
		os.Exit(1)
	}

	demoMode := isDemo()
	if demoMode {
		// Isola os dados do demo numa pasta temporária e os semeia, para não
		// tocar nas tarefas/sessões reais; o GitLab/Jira vêm inventados.
		os.Setenv("XDG_DATA_HOME", filepath.Join(os.TempDir(), "wmonit-demo"))
		demo.SeedData()
	}

	store, err := tasks.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wmonit:", err)
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(cfg, store, demoMode), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "wmonit:", err)
		os.Exit(1)
	}
}

// isDemo informa se o modo demo foi pedido via flag --demo ou WMONIT_DEMO.
func isDemo() bool {
	if os.Getenv("WMONIT_DEMO") != "" {
		return true
	}
	for _, a := range os.Args[1:] {
		if a == "--demo" || a == "-demo" || a == "demo" {
			return true
		}
	}
	return false
}
