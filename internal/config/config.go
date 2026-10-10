package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"

	"github.com/timmers/wmonit/internal/claude"
)

type GitLab struct {
	URL     string `toml:"url"`
	Token   string `toml:"token"`
	DraftMR bool   `toml:"draft_mr"`
}

type Jira struct {
	URL              string   `toml:"url"`
	Auth             string   `toml:"auth"`
	Email            string   `toml:"email"`
	Token            string   `toml:"token"`
	StatusOrder      []string `toml:"status_order"`
	ComplexityField  string   `toml:"complexity_field"`
	StartTransition  string   `toml:"start_transition"`
	ReviewTransition string   `toml:"review_transition"`
}

// TODO: mudar para tasks finalizadas e mr abertos
type Goals struct {
	WeeklyMRs    int `toml:"weekly_mrs"`
	WeeklyIssues int `toml:"weekly_issues"`
}

type Claude struct {
	SourcesDir     string                  `toml:"sources_dir"`
	WorktreesDir   string                  `toml:"worktrees_dir"`
	Bin            string                  `toml:"bin"`
	BranchPrefix   string                  `toml:"branch_prefix"`
	Template       string                  `toml:"template"`
	Templates      map[string]string       `toml:"templates"`
	Model          string                  `toml:"model"`
	Models         []string                `toml:"models"`
	PermissionMode string                  `toml:"permission_mode"`
	Prices         map[string]claude.Price `toml:"prices"`
}

type Herdr struct {
	Bin      string `toml:"bin"`
	Session  string `toml:"session"`
	Editor   string `toml:"editor"`
	Events   *bool  `toml:"events"`
	Metadata *bool  `toml:"metadata"`
	Notify   string `toml:"notify"`
}

func (h Herdr) EventsOn() bool   { return h.Events == nil || *h.Events }
func (h Herdr) MetadataOn() bool { return h.Metadata == nil || *h.Metadata }
func (h Herdr) NotifyDesktop() bool {
	return h.Notify == "" || h.Notify == "desktop" || h.Notify == "both"
}
func (h Herdr) NotifyHerdr() bool { return h.Notify == "herdr" || h.Notify == "both" }

type Editor struct {
	Bin string `toml:"bin"`
}

type Config struct {
	GitLab GitLab `toml:"gitlab"`
	Jira   Jira   `toml:"jira"`
	Goals  Goals  `toml:"goals"`
	Claude Claude `toml:"claude"`
	Herdr  Herdr  `toml:"herdr"`
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
	if p := os.Getenv("WMONIT_CONFIG"); p != "" {
		return p
	}
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
	if cfg.Herdr.Bin == "" {
		cfg.Herdr.Bin = "herdr"
	}
	if cfg.Herdr.Editor == "" {
		cfg.Herdr.Editor = "nvim ."
	}
	if len(cfg.Claude.Models) == 0 {
		cfg.Claude.Models = []string{"opus", "sonnet", "haiku"}
	}
	return cfg, nil
}
