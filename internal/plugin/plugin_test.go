package plugin

import (
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/timmers/wmonit/internal/config"
)

func TestManifest(t *testing.T) {
	cfg := config.Config{Jira: config.Jira{URL: "https://jira.exemplo.com/"}, GitLab: config.GitLab{URL: "https://git.exemplo.com"}}
	text := ManifestText("/usr/bin/wmonit", cfg)
	var m struct {
		ID    string `toml:"id"`
		Panes []struct {
			ID        string   `toml:"id"`
			Placement string   `toml:"placement"`
			Command   []string `toml:"command"`
		} `toml:"panes"`
		Actions []struct {
			ID      string   `toml:"id"`
			Command []string `toml:"command"`
		} `toml:"actions"`
		Links []struct {
			Pattern string `toml:"pattern"`
			Action  string `toml:"action"`
		} `toml:"link_handlers"`
		Startup []struct {
			Command []string `toml:"command"`
		} `toml:"startup"`
	}
	if _, err := toml.Decode(text, &m); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if m.ID != ID || len(m.Panes) != 1 || m.Panes[0].Placement != "popup" || m.Panes[0].Command[0] != "/usr/bin/wmonit" {
		t.Fatalf("manifesto = %+v", m)
	}
	if len(m.Actions) != 3 || strings.Join(m.Actions[1].Command, " ") != "/usr/bin/wmonit herdr-action link" || len(m.Startup) != 1 {
		t.Fatalf("ações = %+v", m.Actions)
	}
	if len(m.Links) != 2 {
		t.Fatalf("links = %+v", m.Links)
	}
	jira := regexp.MustCompile(m.Links[0].Pattern)
	if !jira.MatchString("https://jira.exemplo.com/browse/ROT-512") || jira.MatchString("https://jira.exemplo.com/browse/rot") {
		t.Errorf("padrão do Jira = %s", m.Links[0].Pattern)
	}
	mr := regexp.MustCompile(m.Links[1].Pattern)
	if !mr.MatchString("https://git.exemplo.com/Roteamento/hades/-/merge_requests/9470") {
		t.Errorf("padrão do GitLab = %s", m.Links[1].Pattern)
	}
	if len(regexp.MustCompile(`link_handlers`).FindAllString(ManifestText("/x", config.Config{}), -1)) != 0 {
		t.Error("sem URLs configuradas não deveria haver link handlers")
	}
}
