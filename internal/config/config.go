package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

type GitLab struct {
	URL   string `toml:"url"`
	Token string `toml:"token"`
}

type Jira struct {
	URL             string   `toml:"url"`
	Auth            string   `toml:"auth"` // "bearer" (Server/DC com PAT) ou "basic" (Cloud com email+token)
	Email           string   `toml:"email"`
	Token           string   `toml:"token"`
	StatusOrder     []string `toml:"status_order"`
	ComplexityField string   `toml:"complexity_field"`
}

// TODO: mudar para tasks finalizadas e mr abertos
type Goals struct {
	WeeklyMRs    int `toml:"weekly_mrs"`
	WeeklyIssues int `toml:"weekly_issues"`
}

type Claude struct {
	SourcesDir     string            `toml:"sources_dir"`
	WorktreesDir   string            `toml:"worktrees_dir"`
	Bin            string            `toml:"bin"`
	BranchPrefix   string            `toml:"branch_prefix"`
	Templates      map[string]string `toml:"templates"`       // TODO: Seria legal adicionar um template geral e não por serviço
	Model          string            `toml:"model"`           // modelo dos agents; vazio = default do usuário
	PermissionMode string            `toml:"permission_mode"` // vazio = default do usuário (settings.json)
}

type Editor struct {
	Bin       string `toml:"bin"`       // editor da explicação (ctrl+e); vazio = $VISUAL/$EDITOR/nvim
	Workspace string `toml:"workspace"` // abre a pasta de um agent (tecla e); default "code"
	Terminal  string `toml:"terminal"`  // terminal externo da conversa (tecla a); "wt" no Windows, vazio = na própria TUI
}

type Config struct {
	GitLab GitLab `toml:"gitlab"`
	Jira   Jira   `toml:"jira"`
	Goals  Goals  `toml:"goals"`
	Claude Claude `toml:"claude"`
	Editor Editor `toml:"editor"`
}

func defaultSourcesDir() string {
	if runtime.GOOS == "windows" {
		return "c:/Fontes"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Projects")
}

func Path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "wmonit", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wmonit", "config.toml")
}

func Load() (Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(Path(), &cfg); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cfg, fmt.Errorf("lendo %s: %w", Path(), err)
	}
	if v := os.Getenv("WMONIT_GITLAB_URL"); v != "" {
		cfg.GitLab.URL = v
	}
	if v := os.Getenv("WMONIT_GITLAB_TOKEN"); v != "" {
		cfg.GitLab.Token = v
	}
	if v := os.Getenv("WMONIT_JIRA_URL"); v != "" {
		cfg.Jira.URL = v
	}
	if v := os.Getenv("WMONIT_JIRA_TOKEN"); v != "" {
		cfg.Jira.Token = v
	}
	if v := os.Getenv("WMONIT_JIRA_EMAIL"); v != "" {
		cfg.Jira.Email = v
	}
	if cfg.Jira.Auth == "" {
		cfg.Jira.Auth = "bearer"
	}
	if cfg.Claude.SourcesDir == "" {
		cfg.Claude.SourcesDir = defaultSourcesDir()
	}
	if cfg.Claude.WorktreesDir == "" {
		cfg.Claude.WorktreesDir = filepath.Join(cfg.Claude.SourcesDir, ".worktrees")
	}
	if cfg.Claude.Bin == "" {
		cfg.Claude.Bin = "claude"
	}
	if cfg.Editor.Workspace == "" {
		cfg.Editor.Workspace = "code"
	}
	if cfg.Editor.Terminal == "" && runtime.GOOS == "windows" {
		cfg.Editor.Terminal = "wt"
	}
	return cfg, nil
}
