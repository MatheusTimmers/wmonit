package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type GitLab struct {
	URL   string `toml:"url"`
	Token string `toml:"token"`
}

type Jira struct {
	URL   string `toml:"url"`
	Auth  string `toml:"auth"` // "bearer" (Server/DC com PAT) ou "basic" (Cloud com email+token)
	Email string `toml:"email"`
	Token string `toml:"token"`
	// Ordem de exibição dos grupos de status na aba Jira; status fora
	// da lista aparecem depois, na ordem em que a API devolver.
	StatusOrder []string `toml:"status_order"`
	// Id do campo de complexidade da issue (ex.: "customfield_10106").
	// Vazio = detectar automaticamente pelo nome do campo.
	ComplexityField string `toml:"complexity_field"`
}

// Goals define metas semanais opcionais; 0 desativa a barra de progresso.
type Goals struct {
	WeeklyMRs    int `toml:"weekly_mrs"`
	WeeklyIssues int `toml:"weekly_issues"`
}

// Claude configura as sessões de trabalho com o Claude Code.
type Claude struct {
	// Pasta com os serviços (cada subpasta é um repositório git).
	SourcesDir string `toml:"sources_dir"`
	// Onde criar os worktrees; vazio = <sources_dir>/.worktrees.
	WorktreesDir string `toml:"worktrees_dir"`
	// Binário do Claude Code; vazio = "claude" no PATH.
	Bin string `toml:"bin"`
	// Instruções extras por serviço, anexadas ao prompt da sessão.
	Templates map[string]string `toml:"templates"`
	// Modelo por fase do pipeline (plan/dev/review), passado via --model.
	// Aceita alias ("opus", "sonnet", "haiku") ou id completo; vazio usa
	// o default do Claude Code.
	Models map[string]string `toml:"models"`
}

type Config struct {
	GitLab GitLab `toml:"gitlab"`
	Jira   Jira   `toml:"jira"`
	Goals  Goals  `toml:"goals"`
	Claude Claude `toml:"claude"`
}

func Path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "wmonit", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wmonit", "config.toml")
}

// Load lê o config.toml (se existir) e aplica overrides das variáveis
// de ambiente WMONIT_*, que têm precedência sobre o arquivo.
func Load() (Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(Path(), &cfg); err != nil && !os.IsNotExist(err) {
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
		cfg.Claude.SourcesDir = "c:/Fontes"
	}
	if cfg.Claude.WorktreesDir == "" {
		cfg.Claude.WorktreesDir = filepath.Join(cfg.Claude.SourcesDir, ".worktrees")
	}
	if cfg.Claude.Bin == "" {
		cfg.Claude.Bin = "claude"
	}
	// Defaults do pipeline: opus para planejar e revisar (raciocínio e
	// precisão), sonnet para desenvolver (velocidade/custo com plano pronto).
	// Só preenche chave AUSENTE — valor vazio explícito significa "use o
	// default do próprio Claude Code" (sem --model).
	if cfg.Claude.Models == nil {
		cfg.Claude.Models = map[string]string{}
	}
	for phase, model := range map[string]string{"plan": "opus", "dev": "sonnet", "review": "opus"} {
		if _, ok := cfg.Claude.Models[phase]; !ok {
			cfg.Claude.Models[phase] = model
		}
	}
	return cfg, nil
}
