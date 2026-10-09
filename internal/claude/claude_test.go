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
		"descrição aqui", "cuidado com o cache", "hades!9470", "rode make test",
		"NÃO rode build nem testes", "por conta do usuário"}
	for name, p := range map[string]string{"agent": AgentPrompt(c), "review": ReviewMRPrompt(c)} {
		for _, want := range common {
			if !strings.Contains(p, want) {
				t.Errorf("%s sem %q:\n%s", name, want, p)
			}
		}
	}
	if p := AgentPrompt(c); !strings.Contains(p, "trabalho em andamento") || !strings.Contains(p, "NÃO faça push") {
		t.Errorf("agent sem aviso de branch existente ou guarda de push:\n%s", p)
	}
	if p := AgentPrompt(TaskContext{Key: "X-1", Title: "t"}); strings.Contains(p, "Descrição da tarefa") ||
		strings.Contains(p, "específicas") || strings.Contains(p, "trabalho em andamento") {
		t.Errorf("agent com seções vazias:\n%s", p)
	}
	if p := ReviewMRPrompt(c); !strings.Contains(p, "NÃO modifique código") {
		t.Errorf("review deveria ser só leitura:\n%s", p)
	}
	if p := FreePrompt("hades", "investigar o leak"); !strings.Contains(p, "hades") || !strings.Contains(p, "investigar o leak") {
		t.Errorf("FreePrompt sem serviço/tarefa:\n%s", p)
	}
}
