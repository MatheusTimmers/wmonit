package claude

import (
	"strings"
	"testing"
)

func TestAgentPrompts(t *testing.T) {
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
		"descrição aqui", "cuidado com o cache", "hades!9470", "rode make test", "NÃO rode build nem testes"}
	for name, p := range map[string]string{"agent": AgentPrompt(c), "review": ReviewMRPrompt(c), "context": ContextOnly(c)} {
		for _, want := range common {
			if !strings.Contains(p, want) {
				t.Errorf("%s sem %q:\n%s", name, want, p)
			}
		}
	}
	if p := AgentPrompt(c); !strings.Contains(p, "trabalho em andamento") || !strings.Contains(p, "NÃO faça push") || !strings.Contains(p, TaskFile) {
		t.Errorf("agent sem guardas:\n%s", p)
	}
	if p := AgentPrompt(TaskContext{Key: "X-1", Title: "t"}); strings.Contains(p, "Descrição da tarefa") || strings.Contains(p, "trabalho em andamento") {
		t.Errorf("agent com seções vazias:\n%s", p)
	}
	if p := ReviewMRPrompt(c); !strings.Contains(p, "NÃO modifique código") {
		t.Errorf("review deveria ser só leitura:\n%s", p)
	}
}

func TestKickoffs(t *testing.T) {
	if !strings.Contains(Kickoff(), TaskFile) {
		t.Error("Kickoff deveria apontar o arquivo da tarefa")
	}
	if k := DigestKickoff(); !strings.Contains(k, DigestFile) || !strings.Contains(k, ReplyFile) {
		t.Errorf("DigestKickoff = %q", k)
	}
	if h := DigestHeader(); !strings.Contains(h, ReplyFile) || !strings.Contains(h, "NÃO mande prompts") {
		t.Errorf("DigestHeader sem instruções:\n%s", h)
	}
	if p := FreePrompt("wmonit", "  ajustar o rodapé "); !strings.Contains(p, "wmonit") || !strings.Contains(p, "ajustar o rodapé") {
		t.Errorf("FreePrompt = %q", p)
	}
}
