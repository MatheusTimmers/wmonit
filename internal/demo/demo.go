// Package demo fornece dados inventados para rodar o wmonit sem conexão
// com o GitLab/Jira — útil para ver as telas e testar a UI offline. Não há
// injeção de dependência: o modo demo é só um flag que o app consulta para
// devolver estes dados em vez de chamar a rede.
package demo

import (
	"time"

	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/jira"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
)

func daysAgo(n int) time.Time  { return time.Now().AddDate(0, 0, -n) }
func hoursAgo(n int) time.Time { return time.Now().Add(time.Duration(-n) * time.Hour) }
func ymd(n int) string         { return daysAgo(n).Format("2006-01-02") }

// mr monta um MR de demonstração (a struct References é anônima, então é
// preenchida depois da construção).
func mr(iid, pid int, title, branch, full string, created, updated time.Time, merged *time.Time) gitlab.MR {
	m := gitlab.MR{
		IID:          iid,
		ProjectID:    pid,
		Title:        title,
		Description:  "Merge request de demonstração.\n\nMudança ilustrativa para o modo demo.",
		SourceBranch: branch,
		WebURL:       "https://gitlab.exemplo/" + full,
		CreatedAt:    created,
		UpdatedAt:    updated,
		MergedAt:     merged,
	}
	m.References.Full = full
	return m
}

func todo(id int, action, targetType, title, full string, created time.Time) gitlab.Todo {
	t := gitlab.Todo{ID: id, ActionName: action, TargetType: targetType, State: "pending", CreatedAt: created}
	t.Target.Title = title
	t.Target.References.Full = full
	t.TargetURL = "https://gitlab.exemplo/" + full
	return t
}

// GitLab devolve um resumo do GitLab inventado, cobrindo MRs abertos,
// mergeados (em várias semanas, para a aba Desempenho), a fila de review e
// os todos que alimentam os alertas.
func GitLab() *gitlab.Summary {
	merged := func(d int) *time.Time { t := daysAgo(d); return &t }
	return &gitlab.Summary{
		Username: "voce",
		OpenMRs: []gitlab.MR{
			mr(9471, 1, "valida book agregado antes do envio #ROT-501 [feature]", "feature/ROT-501", "Roteamento/hades!9471", daysAgo(1), hoursAgo(3), nil),
			mr(9472, 1, "corrige race no cache de cotação #ROT-504 [bug]", "fix/ROT-504", "Roteamento/hades!9472", daysAgo(2), hoursAgo(20), nil),
			mr(412, 2, "endpoint de histórico de ordens #ROT-512 [feature]", "feature/ROT-512", "Roteamento/backoffice!412", daysAgo(4), daysAgo(1), nil),
		},
		Merged: []gitlab.MR{
			mr(9460, 1, "ajusta timeout do roteador #ROT-498 [bug]", "fix/ROT-498", "Roteamento/hades!9460", daysAgo(2), daysAgo(1), merged(1)),
			mr(9455, 1, "novo campo de complexidade no book #ROT-490 [feature]", "feature/ROT-490", "Roteamento/hades!9455", daysAgo(4), daysAgo(3), merged(3)),
			mr(405, 2, "refatora serviço de cotação #ROT-485 [refactor]", "refactor/ROT-485", "Roteamento/backoffice!405", daysAgo(6), daysAgo(5), merged(5)),
			mr(9440, 1, "corrige cálculo de média #ROT-470 [bug]", "fix/ROT-470", "Roteamento/hades!9440", daysAgo(12), daysAgo(11), merged(11)),
			mr(9420, 1, "melhora logs do envio #ROT-455 [chore]", "chore/ROT-455", "Roteamento/hades!9420", daysAgo(20), daysAgo(19), merged(19)),
			mr(390, 2, "valida payload de entrada #ROT-440 [feature]", "feature/ROT-440", "Roteamento/backoffice!390", daysAgo(34), daysAgo(33), merged(33)),
		},
		ReviewPending: []gitlab.MR{
			mr(9469, 1, "ajusta parser de mensagens #ROT-499 [bug]", "fix/ROT-499", "Roteamento/hades!9469", daysAgo(4), daysAgo(3), nil),
			mr(9470, 1, "novo formato de relatório #ROT-500 [feature]", "feature/ROT-500", "Roteamento/hades!9470", daysAgo(2), daysAgo(1), nil),
			mr(411, 2, "corrige paginação do extrato #ROT-510 [bug]", "fix/ROT-510", "Roteamento/backoffice!411", hoursAgo(5), hoursAgo(2), nil),
		},
		Todos: []gitlab.Todo{
			todo(1, "review_requested", "MergeRequest", "ajusta parser de mensagens #ROT-499", "Roteamento/hades!9469", hoursAgo(3)),
			todo(2, "build_failed", "MergeRequest", "valida book agregado antes do envio #ROT-501", "Roteamento/hades!9471", hoursAgo(2)),
			todo(3, "mentioned", "MergeRequest", "endpoint de histórico de ordens #ROT-512", "Roteamento/backoffice!412", hoursAgo(1)),
		},
	}
}

func issue(key, summary, status, cat, typ, prio string, rank int, cx, due string, createdDaysAgo, resolvedDaysAgo int) jira.Issue {
	is := jira.Issue{
		Key: key, Summary: summary, Status: status, Category: cat, Type: typ,
		Priority: prio, PrioRank: rank, Complexity: cx, Due: due,
		Created: ymd(createdDaysAgo),
	}
	if resolvedDaysAgo >= 0 {
		is.Resolved = ymd(resolvedDaysAgo)
	}
	return is
}

// Jira devolve um resumo do Jira inventado: issues abertas em vários status
// (com prioridade, complexidade e prazo) e resolvidas espalhadas no tempo
// para popular a aba Desempenho.
func Jira() *jira.Summary {
	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	yesterday := ymd(1)
	return &jira.Summary{
		CXField: "customfield_10106",
		Open: []jira.Issue{
			issue("ROT-501", "Validar book agregado antes do envio", "Em Andamento", "indeterminate", "Story", "High", 1, "Alta", today, 2, -1),
			issue("ROT-504", "Corrigir race no cache de cotação", "Em Andamento", "indeterminate", "Bug", "Highest", 0, "Média", yesterday, 3, -1),
			issue("ROT-512", "Endpoint de histórico de ordens", "Revisão 1", "indeterminate", "Story", "Medium", 3, "Alta", tomorrow, 4, -1),
			issue("ROT-500", "Novo formato de relatório", "Revisão 1", "indeterminate", "Task", "Medium", 3, "Baixa", "", 2, -1),
			issue("ROT-499", "Ajustar parser de mensagens", "Em Deploy", "indeterminate", "Bug", "Low", 5, "Baixa", "", 5, -1),
			issue("ROT-520", "Investigar lentidão no fechamento", "Bloqueado", "indeterminate", "Bug", "High", 1, "Média", yesterday, 1, -1),
			issue("ROT-525", "Mapear regras de roteamento novas", "Análise em Progresso", "new", "Story", "Medium", 3, "Alta", "", 1, -1),
		},
		Resolved: []jira.Issue{
			issue("ROT-498", "Ajustar timeout do roteador", "Concluído", "done", "Bug", "High", 1, "Média", "", 4, 1),
			issue("ROT-490", "Novo campo de complexidade no book", "Concluído", "done", "Story", "Medium", 3, "Alta", "", 6, 3),
			issue("ROT-485", "Refatorar serviço de cotação", "Concluído", "done", "Task", "Medium", 3, "Alta", "", 9, 5),
			issue("ROT-470", "Corrigir cálculo de média", "Concluído", "done", "Bug", "High", 1, "Baixa", "", 14, 11),
			issue("ROT-455", "Melhorar logs do envio", "Concluído", "done", "Task", "Low", 5, "Baixa", "", 22, 19),
			issue("ROT-440", "Validar payload de entrada", "Concluído", "done", "Story", "Medium", 3, "Média", "", 38, 33),
			issue("ROT-435", "Ajustar formato de export", "Concluído", "done", "Task", "Medium", 3, "Baixa", "", 40, 35),
			issue("ROT-430", "Corrigir fuso no relatório", "Concluído", "done", "Bug", "High", 1, "Média", "", 44, 40),
		},
	}
}

// demoTasks inclui uma tarefa CRÍTICA já vencida (para demonstrar o alerta
// constante), uma de prioridade alta, uma comum e uma concluída.
func demoTasks() []tasks.Task {
	today := time.Now().Format("2006-01-02")
	done := time.Now().Add(-2 * time.Hour)
	past := time.Now().Add(-30 * time.Minute).Format("15:04")
	soon := time.Now().Add(90 * time.Minute).Format("15:04")
	return []tasks.Task{
		{Text: "corrigir incidente em produção", Priority: tasks.PriorityCritical, Due: today, DueTime: past, Created: hoursAgo(3)},
		{Text: "preparar deploy do fechamento", Priority: tasks.PriorityHigh, Due: today, DueTime: soon, Created: hoursAgo(3)},
		{Text: "responder e-mail do time de risco", Priority: tasks.PriorityMedium, Due: today, Created: hoursAgo(5)},
		{Text: "revisar documentação do roteador", Priority: tasks.PriorityLow, Created: daysAgo(1)},
		{Text: "atualizar dependências", Done: true, DoneAt: &done, Created: daysAgo(1)},
	}
}

func demoSessions() []session.Session {
	return []session.Session{
		{
			ID: "hades-9470-demo", Key: "hades!9470", Title: "novo formato de relatório",
			Service: "hades", Branch: "feature/ROT-500", Kind: session.KindMR, Mode: session.ModeReview,
			Phase: session.PhaseReview, Status: session.StatusDone, Created: hoursAgo(2),
			Results: map[string]string{
				session.PhaseReview: "PEDIR AJUSTES: o formato novo não trata o caso de relatório vazio (hades/report.go:88) e o cabeçalho ignora o fuso configurado (report.go:120). Sugiro cobrir os dois antes de aprovar.",
			},
		},
		{
			ID: "ROT-512-demo", Key: "ROT-512", Title: "endpoint de histórico de ordens",
			Service: "backoffice", Branch: "feature/ROT-512", Kind: session.KindIssue, Mode: session.ModeImplement,
			Phase: session.PhasePlan, Status: session.StatusWaiting, Created: hoursAgo(1),
			Results: map[string]string{
				session.PhasePlan: "Objetivo: expor o histórico de ordens por conta. Plano: 1) novo handler GET /ordens/historico; 2) consulta paginada no repositório; 3) testes do handler. Validação fica com o usuário.",
			},
		},
	}
}

// SeedData grava as tarefas e sessões de demonstração no diretório de dados
// atual (em modo demo o app aponta XDG_DATA_HOME para uma pasta temporária,
// então os dados reais não são tocados). Sobrescreve a cada execução.
func SeedData() {
	if ts, err := tasks.Load(); err == nil {
		ts.Tasks = demoTasks()
		_ = ts.Save()
	}
	if ss, err := session.Load(); err == nil {
		ss.Sessions = demoSessions()
		_ = ss.Save()
	}
}
