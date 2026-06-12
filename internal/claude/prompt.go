package claude

import (
	"fmt"
	"strings"
)

// PlanFile é onde o agente de planejamento grava o plano dentro do
// worktree; o agente de desenvolvimento lê dali. Não deve ser commitado.
const PlanFile = "WMONIT_PLAN.md"

// TaskContext reúne tudo o que se sabe sobre a tarefa para compor os
// prompts do pipeline (plan → dev → review).
type TaskContext struct {
	Key         string
	Title       string
	URL         string
	UserNote    string   // explicação digitada pelo usuário no wmonit
	Description string   // descrição da issue/MR
	Comments    []string // "autor: texto", em ordem cronológica
	MRInfo      string   // contexto do MR ligado (descrição/comentários)
	Template    string   // instruções específicas do serviço (config)
	HasBranch   bool     // branch já existia (MR aberto) — pode haver código feito
}

// header monta o bloco de contexto comum a todas as fases.
func (c TaskContext) header() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tarefa %s: %s\n", c.Key, c.Title)
	if c.URL != "" {
		fmt.Fprintf(&b, "Link: %s\n", c.URL)
	}
	b.WriteString("\n")
	if n := strings.TrimSpace(c.UserNote); n != "" {
		b.WriteString("Explicação de quem pediu (a fonte mais importante — em conflito com o resto, vale esta):\n")
		b.WriteString(n + "\n\n")
	}
	if d := strings.TrimSpace(c.Description); d != "" {
		b.WriteString("Descrição da tarefa:\n" + d + "\n\n")
	}
	if len(c.Comments) > 0 {
		b.WriteString("Comentários da tarefa:\n")
		for _, cm := range c.Comments {
			b.WriteString("- " + cm + "\n")
		}
		b.WriteString("\n")
	}
	if mi := strings.TrimSpace(c.MRInfo); mi != "" {
		b.WriteString("Merge request relacionado:\n" + mi + "\n\n")
	}
	if t := strings.TrimSpace(c.Template); t != "" {
		b.WriteString("Instruções específicas deste serviço:\n" + t + "\n\n")
	}
	return b.String()
}

// PlanPrompt: agente 1 — compila a descrição e escreve o plano em PlanFile.
func PlanPrompt(c TaskContext) string {
	var b strings.Builder
	b.WriteString("Você é o agente de PLANEJAMENTO. Ainda não implemente nada.\n\n")
	b.WriteString(c.header())
	b.WriteString("Sua missão:\n")
	b.WriteString("- Explore o repositório para entender a arquitetura e onde a tarefa encosta.\n")
	if c.HasBranch {
		b.WriteString("- Esta branch já existia e pode conter trabalho em andamento: revise os commits e o diff contra a branch base antes de planejar o que falta.\n")
	}
	b.WriteString("- Compile o entendimento da tarefa (objetivo, requisitos, dúvidas/assunções) e um plano de implementação passo a passo.\n")
	fmt.Fprintf(&b, "- Grave tudo no arquivo %s na raiz do repositório, em markdown, com as seções: Objetivo, Contexto, Assunções, Plano (passos numerados com arquivos envolvidos), Validação (como buildar/testar).\n", PlanFile)
	b.WriteString("- NÃO modifique nenhum outro arquivo, NÃO faça commit nem push.\n")
	b.WriteString("- Ao final, responda com um resumo do plano em poucas linhas.\n")
	return b.String()
}

// DevPrompt: agente 2 — implementa seguindo o plano gravado pelo agente 1.
func DevPrompt(c TaskContext) string {
	var b strings.Builder
	b.WriteString("Você é o agente de DESENVOLVIMENTO.\n\n")
	b.WriteString(c.header())
	fmt.Fprintf(&b, "Um agente anterior analisou a tarefa e deixou o plano em %s na raiz do repositório. Leia-o antes de começar e siga-o.\n\n", PlanFile)
	b.WriteString("Instruções:\n")
	b.WriteString("- Implemente o plano nesta branch (você já está nela).\n")
	b.WriteString("- Siga as convenções do código existente.\n")
	b.WriteString("- Rode build e testes para validar antes de terminar.\n")
	fmt.Fprintf(&b, "- Faça commits pequenos com mensagens claras; NÃO faça push e NÃO commite o %s.\n", PlanFile)
	b.WriteString("- Se algum passo do plano se mostrar inviável, adapte e registre o desvio no resumo final.\n")
	b.WriteString("- Ao final, resuma o que foi feito e o que ficou pendente.\n")
	return b.String()
}

// ResumePrompt retoma uma conversa interrompida da fase.
func ResumePrompt() string {
	return "Continue a tarefa de onde parou, seguindo as instruções anteriores. Revise o estado atual do repositório antes de prosseguir."
}

// FixPrompt retoma a conversa do agente de desenvolvimento para aplicar
// as correções apontadas pelo review.
func FixPrompt(verdict string) string {
	var b strings.Builder
	b.WriteString("Um agente revisou o seu trabalho nesta branch e apontou ajustes. Veredito do review:\n\n")
	b.WriteString(strings.TrimSpace(verdict))
	b.WriteString("\n\nAplique as correções necessárias:\n")
	b.WriteString("- Revise o estado atual do repositório antes de mexer.\n")
	b.WriteString("- Corrija os problemas apontados; se discordar de algum, justifique no resumo final.\n")
	b.WriteString("- Rode build e testes para validar.\n")
	fmt.Fprintf(&b, "- Faça commits pequenos com mensagens claras; NÃO faça push e NÃO commite o %s.\n", PlanFile)
	b.WriteString("- Ao final, resuma o que foi corrigido.\n")
	return b.String()
}

// ReviewPrompt: agente 3 — revisa o que foi desenvolvido e só reporta.
func ReviewPrompt(c TaskContext) string {
	var b strings.Builder
	b.WriteString("Você é o agente de REVISÃO. NÃO modifique código — apenas analise e reporte.\n\n")
	b.WriteString(c.header())
	fmt.Fprintf(&b, "Um agente implementou a tarefa nesta branch seguindo o plano em %s. Revise o trabalho:\n", PlanFile)
	b.WriteString("- Use git (log/diff contra a branch base) para ver todas as mudanças feitas, incluindo o que não foi commitado.\n")
	b.WriteString("- Verifique se o plano e a tarefa foram atendidos, procure bugs, casos de borda, problemas de segurança e desvios das convenções do código.\n")
	b.WriteString("- Rode build e testes e reporte o resultado.\n")
	b.WriteString("- Responda com um veredito: APROVADO ou AJUSTES NECESSÁRIOS, seguido da lista de problemas encontrados (arquivo:linha) por gravidade. Não corrija nada.\n")
	return b.String()
}
