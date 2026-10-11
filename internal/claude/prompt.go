package claude

import (
	"fmt"
	"path/filepath"
	"strings"
)

const TaskFile = "WMONIT_TASK.md"

const (
	DigestFile = "pendencias.md"
	ReplyFile  = "resposta.md"
)

type TaskContext struct {
	Key         string
	Title       string
	URL         string
	UserNote    string
	Description string
	Comments    []string
	MRInfo      string
	Template    string
	HasBranch   bool
}

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

func buildNotes() string {
	var b strings.Builder
	b.WriteString("Build e testes neste worktree:\n")
	b.WriteString("- NÃO rode build nem testes: o worktree isolado não builda (submodules e dependências não resolvem nele).\n")
	b.WriteString("- A validação (build/testes) fica por conta do usuário depois do merge da branch.\n\n")
	return b.String()
}

func AgentPrompt(c TaskContext) string {
	var b strings.Builder
	b.WriteString("Você está num workspace do herdr dedicado a esta tarefa; eu acompanho pelo wmonit e entro na conversa quando precisar.\n\n")
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
	fmt.Fprintf(&b, "- NÃO commite o %s.\n", TaskFile)
	b.WriteString("- Ao final, resuma o que foi feito, os desvios do plano e o que ficou pendente.\n")
	return b.String()
}

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

func FreePrompt(service, note string) string {
	var b strings.Builder
	b.WriteString("Você está num workspace do herdr; eu acompanho pelo wmonit e entro na conversa quando precisar.\n\n")
	fmt.Fprintf(&b, "Serviço: %s\n\n", service)
	b.WriteString("Tarefa:\n" + strings.TrimSpace(note) + "\n\n")
	b.WriteString("Siga as convenções do CLAUDE.md. Se uma dúvida bloquear, pare e pergunte. Ao final, resuma o que foi feito.\n")
	return b.String()
}

func ContextOnly(c TaskContext) string {
	var b strings.Builder
	b.WriteString("Contexto da tarefa deste workspace (ainda sem pedido de trabalho — espere minhas instruções).\n\n")
	b.WriteString(c.header())
	return b.String()
}

func Kickoff() string {
	return fmt.Sprintf("Leia o arquivo %s na raiz do projeto: ele traz a tarefa e as instruções. Siga-as.", TaskFile)
}

func DigestHeader(dir string) string {
	var b strings.Builder
	b.WriteString("# Pendências\n\n")
	b.WriteString("Você é meu assistente de pendências. Abaixo está o estado de tudo o que eu tenho em aberto: os workspaces do herdr (cada um é uma tarefa, com um Claude trabalhando nela), as issues do Jira, os MRs do GitLab e as minhas tarefas locais.\n\n")
	fmt.Fprintf(&b, "Escreva em %s (sobrescreva o arquivo inteiro), em markdown, com títulos curtos:\n\n", filepath.Join(dir, ReplyFile))
	b.WriteString("1. **Travado** — cada pendência que está parada ou esperando algo: o que trava e como destravar, em passos concretos (quem eu preciso acionar, o que responder ao agent, o que revisar).\n")
	b.WriteString("2. **Próximos passos** — em ordem de prioridade: o que eu faço agora, o que posso delegar a um agent (diga o workspace e o pedido exato) e o que pode esperar.\n")
	b.WriteString("3. **Riscos** — prazos vencendo, MRs parados, trabalho duplicado entre workspaces.\n\n")
	b.WriteString("Seja direto: o que está abaixo já basta na maioria dos casos. Se precisar investigar, use só leitura: `herdr agent read <nome> --source recent-unwrapped --lines 120` para ver a tela de um agent e, nos repositórios, `git status`, `git log`, `git diff` ou `git show` dentro da pasta. Esses comandos e a leitura de arquivos já estão liberados. NÃO mande prompts para outros agents, NÃO altere código e NÃO mexa em workspaces sem eu pedir.\n\n")
	return b.String()
}

func DigestKickoff(dir string) string {
	return fmt.Sprintf("Atualizei o %s. Leia o arquivo inteiro e siga as instruções do topo, escrevendo a resposta em %s.", filepath.Join(dir, DigestFile), filepath.Join(dir, ReplyFile))
}
