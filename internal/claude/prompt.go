// Package claude compõe os prompts dos agents que o wmonit cria. Executar,
// listar e parar os agents é com o pacote agents.
package claude

import (
	"fmt"
	"strings"
)

// TaskContext reúne tudo o que se sabe sobre a tarefa para compor o prompt
// do agent.
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

// header monta o bloco de contexto comum aos prompts.
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
	b.WriteString(buildNotes())
	return b.String()
}

// buildNotes avisa que o worktree não builda — submodules e dependências
// não resolvem nele —; sem isso o agent queima tempo/tokens tentando.
func buildNotes() string {
	var b strings.Builder
	b.WriteString("Build e testes neste worktree:\n")
	b.WriteString("- NÃO rode build nem testes: este é um git worktree isolado e o build NÃO funciona aqui. Não gaste turnos tentando, nem com alternativas.\n")
	b.WriteString("- Valide por leitura: releia o diff, confira assinaturas/chamadas e as convenções do código existente.\n")
	b.WriteString("- Registre no resumo final que build e testes ficam por conta do usuário.\n\n")
	return b.String()
}

// AgentPrompt: o agent de desenvolvimento — entende, planeja e implementa
// numa conversa só. Roda em background; o usuário entra com attach quando
// quiser acompanhar, responder ou corrigir o rumo.
func AgentPrompt(c TaskContext) string {
	var b strings.Builder
	b.WriteString("Você está trabalhando em background numa tarefa; vou acompanhar pelo agent view e entrar na conversa quando precisar.\n\n")
	b.WriteString(c.header())
	b.WriteString("Como trabalhar:\n")
	b.WriteString("- Explore o repositório para entender a arquitetura e onde a tarefa encosta.\n")
	if c.HasBranch {
		b.WriteString("- Esta branch já existia e pode conter trabalho em andamento: revise os commits e o diff contra a branch base antes de mexer.\n")
	}
	b.WriteString("- Escreva um plano curto (passos numerados, arquivos envolvidos) e então implemente nesta branch (você já está nela).\n")
	b.WriteString("- Se uma dúvida bloquear a implementação, pare e pergunte em vez de assumir.\n")
	b.WriteString("- Siga as convenções do código existente e as do CLAUDE.md.\n")
	b.WriteString("- Antes de terminar, releia o diff completo procurando erros óbvios.\n")
	b.WriteString("- Faça commit seguindo a convenção de commit do CLAUDE.md; NÃO faça push.\n")
	b.WriteString("- Ao final, resuma o que foi feito, os desvios do plano e o que ficou pendente.\n")
	return b.String()
}

// ReviewMRPrompt: revisar o merge request de outra pessoa (você é o revisor
// humano e o Claude te ajuda). O worktree já está na branch do MR; o
// trabalho a revisar é o diff do MR contra a branch base.
func ReviewMRPrompt(c TaskContext) string {
	var b strings.Builder
	b.WriteString("Você está me ajudando a REVISAR um merge request que outra pessoa abriu. NÃO modifique código — apenas analise e me dê um parecer de review.\n\n")
	b.WriteString(c.header())
	b.WriteString("O worktree já está na branch deste MR. Sua missão:\n")
	b.WriteString("- Descubra a branch base (geralmente a default do remoto, ex.: origin/HEAD) e use git (log/diff contra a base) para ver TODAS as mudanças do MR.\n")
	b.WriteString("- Entenda o objetivo da mudança (use a descrição/comentários acima) e leia o código alterado e o redor que ele afeta.\n")
	b.WriteString("- Procure bugs, casos de borda, condições de corrida, problemas de segurança, regressões e desvios das convenções do código existente.\n")
	b.WriteString("- A revisão é por leitura: build e testes ficam comigo.\n")
	b.WriteString("- Entregue: (1) um resumo do que o MR faz; (2) os pontos encontrados como comentários de review prontos para eu colar no GitLab, cada um com arquivo:linha e a sugestão; (3) uma recomendação final — APROVAR ou PEDIR AJUSTES — justificada.\n")
	return b.String()
}

// FreePrompt: agent livre, sem issue/MR — só a explicação do usuário.
func FreePrompt(service, note string) string {
	var b strings.Builder
	b.WriteString("Você está trabalhando em background; vou acompanhar pelo agent view e entrar na conversa quando precisar.\n\n")
	fmt.Fprintf(&b, "Serviço: %s\n\n", service)
	b.WriteString("Tarefa:\n" + strings.TrimSpace(note) + "\n\n")
	b.WriteString("Siga as convenções do CLAUDE.md. Se uma dúvida bloquear, pare e pergunte. Ao final, resuma o que foi feito.\n")
	return b.String()
}
