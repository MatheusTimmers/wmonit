package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/paths"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

const (
	ID       = "wmonit"
	Pane     = "hub"
	OpenEnv  = "WMONIT_OPEN"
	Manifest = "herdr-plugin.toml"
)

func Dir() string { return filepath.Join(paths.DataDir(), "herdr-plugin") }

func ManifestText(exe string, cfg config.Config) string {
	q := func(s string) string { return fmt.Sprintf("%q", s) }
	cmd := func(args ...string) string {
		parts := []string{q(exe)}
		for _, a := range args {
			parts = append(parts, q(a))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `id = %q
name = "wmonit"
version = "0.1.0"
min_herdr_version = "0.9.0"
description = "Hub de tarefas do Jira/GitLab dentro do herdr"
platforms = ["linux", "macos", "windows"]

[[panes]]
id = %q
title = "wmonit"
placement = "popup"
width = "90%%"
height = "90%%"
command = %s

[[actions]]
id = "open"
title = "Abrir o wmonit"
contexts = ["workspace"]
command = %s

[[actions]]
id = "link"
title = "Abrir a tarefa do link no wmonit"
contexts = ["workspace"]
command = %s

[[actions]]
id = "sync"
title = "Atualizar a sidebar com Jira/MR/git"
contexts = ["workspace"]
command = %s

[[startup]]
command = %s
`, ID, Pane, cmd(), cmd("herdr-action", "open"), cmd("herdr-action", "link"), cmd("herdr-action", "sync"), cmd("herdr-action", "sync"))
	if p := urlPattern(cfg.Jira.URL, `/browse/[A-Z][A-Z0-9]*-[0-9]+`); p != "" {
		fmt.Fprintf(&b, `
[[link_handlers]]
id = "jira-issue"
title = "Criar workspace para a issue"
pattern = %q
action = "link"
`, p)
	}
	if p := urlPattern(cfg.GitLab.URL, `/.+/-/merge_requests/[0-9]+`); p != "" {
		fmt.Fprintf(&b, `
[[link_handlers]]
id = "gitlab-mr"
title = "Criar workspace para o MR"
pattern = %q
action = "link"
`, p)
	}
	return b.String()
}

func urlPattern(base, suffix string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	return "^" + regexp.QuoteMeta(base) + suffix + "/?$"
}

func herdrBin(cfg config.Config) string {
	if b := os.Getenv("HERDR_BIN_PATH"); b != "" {
		return b
	}
	return cfg.Herdr.Bin
}

func Install(cfg config.Config) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, Manifest), []byte(ManifestText(exe, cfg)), 0o644); err != nil {
		return "", err
	}
	bin := herdrBin(cfg)
	_ = exec.Command(bin, "plugin", "unlink", ID).Run()
	if out, err := exec.Command(bin, "plugin", "link", dir).CombinedOutput(); err != nil {
		return "", fmt.Errorf("herdr plugin link: %s", strings.TrimSpace(string(out)))
	}
	return dir, nil
}

func Uninstall(cfg config.Config) error {
	if out, err := exec.Command(herdrBin(cfg), "plugin", "unlink", ID).CombinedOutput(); err != nil {
		return fmt.Errorf("herdr plugin unlink: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func Action(cfg config.Config, name string) error {
	bin := herdrBin(cfg)
	switch name {
	case "open":
		return openPane(bin, "")
	case "link":
		target := os.Getenv("HERDR_PLUGIN_CLICKED_URL")
		if target == "" {
			return fmt.Errorf("sem HERDR_PLUGIN_CLICKED_URL")
		}
		return openPane(bin, target)
	case "sync":
		hc := herdr.New(bin)
		hc.Session = cfg.Herdr.Session
		return Sync(cfg, hc)
	}
	return fmt.Errorf("ação desconhecida: %s", name)
}

func openPane(bin, target string) error {
	args := []string{"plugin", "pane", "open", "--plugin", ID, "--entrypoint", Pane}
	if target != "" {
		args = append(args, "--env", OpenEnv+"="+target)
	}
	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("herdr plugin pane open: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func Sync(cfg config.Config, h *herdr.Client) error {
	store, err := session.Load()
	if err != nil {
		return err
	}
	snap := hub.Poll(h)
	if !snap.Online() {
		return snap.Err
	}
	for i := range store.Sessions {
		s := &store.Sessions[i]
		hub.Reconcile(s, snap)
		if !s.Active() || s.Workspace == "" {
			continue
		}
		ws, ok := snap.Workspace(s.Workspace)
		if !ok {
			continue
		}
		in := hub.MetaInput{Session: *s}
		if info, err := worktree.Inspect(hub.Dir(*s)); err == nil {
			in.Git = &info
		}
		if diff := hub.TokensChanged(hub.CurrentTokens(ws.Tokens), hub.Tokens(in)); len(diff) > 0 {
			if err := h.ReportMetadata(ws.ID, hub.MetaSource, diff); err != nil {
				return err
			}
		}
	}
	return store.Save()
}
