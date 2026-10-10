package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/demo"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/history"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/paths"
	"github.com/timmers/wmonit/internal/plugin"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
	"github.com/timmers/wmonit/internal/ui"
)

func main() {
	demoMode := flag.Bool("demo", false, "roda com dados fictícios, sem tocar na config/dados reais")
	open := flag.String("open", os.Getenv(plugin.OpenEnv), "abre direto a criação de workspace para uma issue/MR (chave, link do Jira ou do MR)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		die(err)
	}

	switch flag.Arg(0) {
	case "herdr-plugin":
		herdrPlugin(cfg, flag.Arg(1))
		return
	case "herdr-action":
		if err := plugin.Action(cfg, flag.Arg(1)); err != nil {
			die(err)
		}
		return
	case "":
	default:
		die(fmt.Errorf("comando desconhecido %q (use herdr-plugin install|uninstall|manifest)", flag.Arg(0)))
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
		m = ui.New(cfg, store, hist, sess, demo.GitLabSource{}, demo.JiraSource{}, demo.NewHerdr(), "🧪 DEMO")
	} else {
		gl := gitlab.New(cfg.GitLab.URL, cfg.GitLab.Token)
		ji := jira.New(cfg.Jira.URL, cfg.Jira.Auth, cfg.Jira.Email, cfg.Jira.Token, cfg.Jira.ComplexityField)
		hc := herdr.New(cfg.Herdr.Bin)
		hc.Session = cfg.Herdr.Session
		m = ui.New(cfg, store, hist, sess, gl, ji, hc, "")
	}

	m = m.WithOpen(*open)
	if _, err := tea.NewProgram(m).Run(); err != nil {
		die(err)
	}
}

func herdrPlugin(cfg config.Config, cmd string) {
	switch cmd {
	case "install":
		dir, err := plugin.Install(cfg)
		if err != nil {
			die(err)
		}
		fmt.Println("plugin ligado ao herdr a partir de", dir)
		fmt.Println(pluginHelp)
	case "uninstall":
		if err := plugin.Uninstall(cfg); err != nil {
			die(err)
		}
		fmt.Println("plugin desligado do herdr")
	case "manifest":
		exe, _ := os.Executable()
		fmt.Print(plugin.ManifestText(exe, cfg))
	default:
		die(fmt.Errorf("uso: wmonit herdr-plugin install|uninstall|manifest"))
	}
}

const pluginHelp = `
Para abrir o wmonit num popup com uma tecla, adicione ao ~/.config/herdr/config.toml:

  [[keys.command]]
  key = "prefix+m"
  type = "plugin_action"
  command = "wmonit.open"
  description = "wmonit"

E para ver Jira/MR/CI de cada tarefa na sidebar:

  [ui.sidebar.spaces]
  rows = [["state_icon", "workspace"], ["branch", "git_status"], ["$wm_jira", "$wm_mr", "$wm_ci"], ["$wm_todo"]]

Depois: herdr server reload-config`

func die(err error) {
	fmt.Fprintln(os.Stderr, "wmonit:", err)
	os.Exit(1)
}
