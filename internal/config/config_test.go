package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestLoadDefaults cobre os valores padrão aplicados quando não há
// config.toml nem env vars — usados na primeira execução do wmonit.
func TestLoadDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // sem config.toml dentro: Load não deve falhar

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Jira.Auth != "bearer" {
		t.Errorf("Jira.Auth = %q, esperado %q", cfg.Jira.Auth, "bearer")
	}
	if cfg.Claude.PermissionMode != "" || cfg.Claude.Model != "" {
		t.Errorf("Claude.PermissionMode/Model deveriam ficar vazios: %q %q", cfg.Claude.PermissionMode, cfg.Claude.Model)
	}
	if cfg.Herdr.Bin != "herdr" || cfg.Herdr.Editor != "nvim ." {
		t.Errorf("Herdr = %+v", cfg.Herdr)
	}
}

// TestLoadEnvOverridesFile garante que as env vars WMONIT_* prevalecem sobre
// o que está no config.toml — é como o usuário sobrescreve segredos (token)
// sem editar o arquivo, ex.: em CI.
func TestLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "wmonit"), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := `
[gitlab]
url = "https://file.example/gitlab"
token = "file-gitlab-token"

[jira]
url = "https://file.example/jira"
token = "file-jira-token"
email = "file@example.com"
`
	if err := os.WriteFile(filepath.Join(dir, "wmonit", "config.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("WMONIT_GITLAB_URL", "https://env.example/gitlab")
	t.Setenv("WMONIT_GITLAB_TOKEN", "env-gitlab-token")
	t.Setenv("WMONIT_JIRA_URL", "https://env.example/jira")
	t.Setenv("WMONIT_JIRA_TOKEN", "env-jira-token")
	t.Setenv("WMONIT_JIRA_EMAIL", "env@example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitLab.URL != "https://env.example/gitlab" {
		t.Errorf("GitLab.URL = %q, esperado env var", cfg.GitLab.URL)
	}
	if cfg.GitLab.Token != "env-gitlab-token" {
		t.Errorf("GitLab.Token = %q, esperado env var", cfg.GitLab.Token)
	}
	if cfg.Jira.URL != "https://env.example/jira" {
		t.Errorf("Jira.URL = %q, esperado env var", cfg.Jira.URL)
	}
	if cfg.Jira.Token != "env-jira-token" {
		t.Errorf("Jira.Token = %q, esperado env var", cfg.Jira.Token)
	}
	if cfg.Jira.Email != "env@example.com" {
		t.Errorf("Jira.Email = %q, esperado env var", cfg.Jira.Email)
	}
}

func TestExampleConfigMatchesStruct(t *testing.T) {
	t.Setenv("WMONIT_CONFIG", filepath.Join("..", "..", "config.example.toml"))
	t.Setenv("WMONIT_GITLAB_URL", "")
	t.Setenv("WMONIT_JIRA_URL", "")
	md, err := toml.DecodeFile(Path(), &Config{})
	if err != nil {
		t.Fatal(err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		t.Fatalf("chaves do exemplo que a config não conhece: %v", u)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Herdr
	if !h.EventsOn() || !h.MetadataOn() || !h.NotifyDesktop() || h.NotifyHerdr() || len(cfg.Claude.Models) != 3 {
		t.Fatalf("padrões = %+v %v", h, cfg.Claude.Models)
	}
	off := false
	h = Herdr{Events: &off, Metadata: &off, Notify: "herdr"}
	if h.EventsOn() || h.MetadataOn() || h.NotifyDesktop() || !h.NotifyHerdr() {
		t.Fatalf("desligados = %+v", h)
	}
}
