package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelinePrompts(t *testing.T) {
	c := TaskContext{
		Key:         "ABC-123",
		Title:       "corrigir rota",
		URL:         "http://jira/ABC-123",
		UserNote:    "foque no módulo de rotas",
		Description: "descrição aqui",
		Comments:    []string{"Fulano: cuidado com o cache"},
		MRInfo:      "MR hades!9470: já tem um esqueleto",
		Template:    "rode make test",
		HasBranch:   true,
	}
	common := []string{"ABC-123", "corrigir rota", "http://jira/ABC-123", "foque no módulo de rotas",
		"descrição aqui", "cuidado com o cache", "hades!9470", "rode make test",
		"git submodule update --init --force", "\"app\" no nome"}
	for name, p := range map[string]string{"plan": PlanPrompt(c), "dev": DevPrompt(c), "review": ReviewPrompt(c)} {
		for _, want := range append(common, PlanFile) {
			if !strings.Contains(p, want) {
				t.Errorf("%s sem %q:\n%s", name, want, p)
			}
		}
	}
	if p := PlanPrompt(c); !strings.Contains(p, "trabalho em andamento") {
		t.Errorf("plan sem aviso de branch existente:\n%s", p)
	}
	if p := PlanPrompt(TaskContext{Key: "X-1", Title: "t"}); strings.Contains(p, "Descrição da tarefa") ||
		strings.Contains(p, "específicas") || strings.Contains(p, "trabalho em andamento") {
		t.Errorf("plan com seções vazias:\n%s", p)
	}
	if p := ReviewPrompt(c); !strings.Contains(p, "NÃO modifique código") {
		t.Errorf("review deveria ser só leitura:\n%s", p)
	}
	if p := DevPrompt(c); !strings.Contains(p, "NÃO faça push") {
		t.Errorf("dev sem guarda de push:\n%s", p)
	}
}

func TestRunCapturesOutput(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "logs", "x.jsonl")
	// Usa um binário simples no lugar do claude para validar dir/argumentos/log.
	if err := Run(Opts{Bin: "pwd", Dir: dir, Prompt: "ignored", LogFile: log}, nil); err == nil {
		// pwd ignora os argumentos e imprime o diretório — Run deve gravar isso.
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), dir) {
			t.Errorf("log sem o cwd: %q", data)
		}
	}
}

func TestResumeAndFixPrompts(t *testing.T) {
	if p := ResumePrompt(); !strings.Contains(p, "Continue a tarefa") {
		t.Errorf("ResumePrompt inesperado: %s", p)
	}
	p := FixPrompt("AJUSTES NECESSÁRIOS\n- bug em x.go:10")
	for _, want := range []string{"bug em x.go:10", "NÃO faça push", PlanFile, "git submodule update --init --force"} {
		if !strings.Contains(p, want) {
			t.Errorf("FixPrompt sem %q:\n%s", want, p)
		}
	}
}
