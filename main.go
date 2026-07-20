package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/demo"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/history"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/paths"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
	"github.com/timmers/wmonit/internal/ui"
)

func main() {
	demoMode := flag.Bool("demo", false, "roda com dados fictícios, sem tocar na config/dados reais")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		die(err)
	}

	// Composition root: o modo demo troca as fontes de dados por dados
	// fictícios e usa uma pasta temporária, sem tocar nos dados reais.
	dir := paths.DataDir()
	if *demoMode {
		// Pasta por execução: um caminho fixo colidiria entre usuários da
		// mesma máquina.
		if dir, err = os.MkdirTemp("", "wmonit-demo-"); err != nil {
			die(err)
		}
		demo.SeedData(dir)
	}

	store, err := tasks.LoadFrom(dir)
	if err != nil {
		die(err)
	}
	hist, _ := history.LoadFrom(dir)
	sess, _ := session.LoadFrom(dir)

	var m ui.Model
	if *demoMode {
		m = ui.New(cfg, store, hist, sess, demo.GitLabSource{}, demo.JiraSource{}, "🧪 DEMO")
	} else {
		gl := gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token)
		ji := jira.New(cfg.Jira.URL, cfg.Jira.Auth, cfg.Jira.Email, cfg.Jira.Token, cfg.Jira.ComplexityField)
		m = ui.New(cfg, store, hist, sess, gl, ji, "")
	}

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		die(err)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "wmonit:", err)
	os.Exit(1)
}
