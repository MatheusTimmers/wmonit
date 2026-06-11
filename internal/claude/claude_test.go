package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPrompt(t *testing.T) {
	p := BuildPrompt("ABC-123", "corrigir rota", "http://jira/ABC-123", "descrição aqui", "rode make test")
	for _, want := range []string{"ABC-123", "corrigir rota", "http://jira/ABC-123", "descrição aqui", "rode make test", "NÃO faça push"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt sem %q:\n%s", want, p)
		}
	}
	p = BuildPrompt("X-1", "t", "", "", "")
	if strings.Contains(p, "Descrição da tarefa") || strings.Contains(p, "específicas") {
		t.Errorf("prompt com seções vazias:\n%s", p)
	}
}

func TestRunCapturesOutput(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "logs", "x.jsonl")
	// Usa um binário simples no lugar do claude para validar dir/argumentos/log.
	if err := Run("pwd", dir, "ignored", log, "", nil); err == nil {
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
