package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSummary(t *testing.T) {
	// O CLAUDE.md global às vezes faz o modelo abrir com outro texto e
	// markdown: as duas linhas valem onde estiverem.
	out := "> ✓ Correct.\n\n**NOME:** Handler Rota Hades\n**FALTA:** registrar a rota e revisar o diff\n"
	s, ok := ParseSummary(out)
	if !ok || s.Name != "Handler Rota Hades" || s.Missing != "registrar a rota e revisar o diff" {
		t.Fatalf("ParseSummary = %+v, %v", s, ok)
	}
	if _, ok := ParseSummary("NOME: só nome"); ok {
		t.Error("sem FALTA não deveria valer")
	}
}

func TestSummaryPrompt(t *testing.T) {
	long := strings.Repeat("x", replyLimit+50)
	p := SummaryPrompt(SummaryInput{Task: "ROT-1 corrige rota", Recent: []string{"fiz o handler", long}, Running: true}, "branch ROT-1\n")
	for _, want := range []string{"ROT-1 corrige rota", "trabalhando agora", "branch ROT-1", "fiz o handler", "NOME:", "FALTA:"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt sem %q", want)
		}
	}
	if strings.Contains(p, long) {
		t.Error("resposta longa deveria ser cortada")
	}
}

func TestWithoutEnv(t *testing.T) {
	got := withoutEnv([]string{"A=1", "CLAUDE_CODE_PLUGIN_DIRS=x", "claude_code_plugin_dirs=y", "B=2"}, "CLAUDE_CODE_PLUGIN_DIRS")
	if strings.Join(got, ",") != "A=1,B=2" {
		t.Fatalf("withoutEnv = %v", got)
	}
}

func TestRequestRename(t *testing.T) {
	dir := t.TempDir()
	if err := RequestRename(dir, "uuid-1", "ROT-1 · Handler"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "fleet", "uuid-1.rename"))
	if err != nil || string(data) != "ROT-1 · Handler" {
		t.Fatalf("pedido de rename = %q, %v", data, err)
	}
}
