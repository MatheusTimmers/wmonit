# wmonit

Monitor de trabalho no terminal: mostra sua atividade no GitLab e no Jira da
Nelogica, suas pendências do dia e dos próximos dias, e mantém uma lista de
tarefas manuais.

## Build

```sh
go build -o wmonit .
```

## Modo demo (sem GitLab/Jira)

Para ver as telas e testar a interface sem nenhuma conexão, rode com dados
inventados:

```sh
./wmonit --demo
```

O modo demo preenche GitLab e Jira com dados fictícios (MRs, fila de review,
issues e métricas) e semeia algumas tarefas e sessões — inclusive uma tarefa
**crítica** já vencida, para você ver o alerta constante em ação. Nada é
buscado na rede e seus dados reais não são tocados (as tarefas e sessões do
demo ficam numa pasta temporária). O rodapé mostra **🧪 DEMO** enquanto está
ativo.

## Configuração

Copie o exemplo e preencha URLs e tokens:

```sh
mkdir -p ~/.config/wmonit
cp config.example.toml ~/.config/wmonit/config.toml
```

- **GitLab**: crie um Personal Access Token em *Preferences → Access Tokens*
  com o scope `read_api`.
- **Jira Server/DC**: crie um PAT em *Perfil → Personal Access Tokens* e use
  `auth = "bearer"`.
- **Jira Cloud**: crie um API token em id.atlassian.com, use `auth = "basic"`
  e preencha `email`.

Os tokens também podem vir de variáveis de ambiente (têm precedência sobre o
arquivo): `WMONIT_GITLAB_URL`, `WMONIT_GITLAB_TOKEN`, `WMONIT_JIRA_URL`,
`WMONIT_JIRA_TOKEN`, `WMONIT_JIRA_EMAIL`.

## Uso

```sh
./wmonit
```

| Tecla            | Ação                                  |
| ---------------- | ------------------------------------- |
| `1`–`6` / `tab`  | troca de aba (Hoje, Desempenho, GitLab, Jira, Tarefas, Sessões) |
| `j`/`k`, setas, PgUp/PgDn | rola / move o cursor da aba  |
| `g`              | abre o relatório do dia (esc/q volta) |
| `r`              | atualiza os dados agora               |
| `q`              | sai                                   |
| `enter`          | (GitLab/Jira) abre o detalhe do item selecionado |
| `o`              | (GitLab/Jira) abre o item no navegador |
| `/`              | (GitLab/Jira) filtra a lista (esc limpa) |
| `a`              | (Tarefas) adiciona tarefa             |
| `espaço` / `x`   | (Tarefas) marca/desmarca como feita   |
| `d`              | (Tarefas) apaga a tarefa selecionada  |
| `j`/`k` ou setas | (Tarefas) move o cursor               |

A tecla **`g`** abre o **relatório do dia**: o que você concluiu hoje — MRs
mergeados e issues resolvidas — além das tarefas marcadas como feitas no dia.
É uma visão à parte, rolável; `esc` ou `q` volta para as abas.

A aba **Desempenho** traz: tabela comparando esta semana, o mês atual e o
mês anterior (MRs mergeados e issues resolvidas); tendência melhor/pior do
mês atual contra o mesmo número de dias do mês anterior, incluindo lead
time médio (criação → resolução); ritmo semanal em barras desde o início
do mês anterior; e a composição de cada mês — MRs por tipo, issues por
tipo, issues por complexidade e lead time. A complexidade vem de um campo
do Jira detectado automaticamente (nome contendo "complex"); se o seu
projeto usar outro nome, defina `complexity_field` no `config.toml`.

Na aba **Hoje**, "Em andamento" é ordenada por prioridade (mais urgente
primeiro, prioridades altas destacadas), limitada às 6 primeiras, e issues
em deploy ou bloqueadas saem da lista e viram um resumo de uma linha.

O wmonit entende o padrão de título de MR `breve descrição #TAG-JIRA [type]`:
a `#TAG` é destacada e usada para ligar o MR à issue — as issues mostram os
MRs vinculados (aberto/mergeado) nas abas Hoje e Jira — e o `[type]` alimenta
a contagem "MRs do mês por tipo" na aba Desempenho.

Na aba **Jira** as issues abertas são agrupadas pelo status (Em Andamento,
Revisão 1, Em Deploy…). A ordem dos grupos é configurável via
`status_order` no `config.toml` — use os nomes exatos dos status do seu
projeto; status fora da lista aparecem no final.

Ao adicionar uma tarefa, um sufixo `@today`, `@tomorrow` ou `@2026-06-15`
define a data de vencimento — tarefas vencendo aparecem na aba **Hoje**. Um
horário opcional logo após a data (ex.: `@today 15:00`) vira um **lembrete**:
ao chegar a hora, o wmonit dispara uma **notificação de desktop** (toast no
Windows, notify-send no Linux).

Um marcador `!alta`, `!critica`, `!media` ou `!baixa` (em qualquer posição do
texto) define a **prioridade** — exibida como selo na lista. Tarefas de
prioridade **alta ou crítica** com horário recebem **alertas constantes**: a
partir da hora marcada, o wmonit re-notifica a cada verificação (a cada 30s)
até você marcar a tarefa como concluída, em vez do aviso único das demais.

Itens que pedem atenção — vencendo hoje/atrasados e reviews aguardando você —
aparecem num **realce no topo** da tela enquanto existirem.

A **fila de review** (na aba Hoje e na GitLab) lista os MRs aguardando seu
review ordenados pelo tempo parado — o mais esquecido primeiro.

Além dos lembretes de tarefa, o wmonit dispara **notificações de desktop
proativas** a cada atualização: review pedido, menção/comentário, build
quebrado e outras pendências do GitLab (via a lista de *todos*), além de
issue nova atribuída a você ou mudança de status no Jira. A primeira leitura
ao abrir o app só estabelece a linha de base — você só é avisado das
novidades dali em diante. As mais recentes também aparecem num bloco
**🔔 Novidades** na aba Hoje.

As abas **GitLab** e **Jira** mostram quanto você produziu hoje, na semana e
no mês; cada item é selecionável (`j`/`k`): `enter` abre um painel com a
descrição, os comentários e os MRs ligados, `o` abre no navegador e `/`
filtra a lista por chave, título ou status. A aba **Desempenho** compara
cada janela (dia/semana/mês) com o período anterior equivalente e, se você
definir metas em `[goals]`, mostra barras de progresso e a sequência de
dias úteis com entrega.

Cada atualização grava um instantâneo do dia (MRs, issues e tarefas) em
`~/.local/share/wmonit/history.json`, que alimenta a sequência de entregas
e dá uma série temporal real ao longo do tempo.

Os dados são atualizados automaticamente a cada 5 minutos. As tarefas ficam
salvas em `~/.local/share/wmonit/tasks.json`.

## Sessões de trabalho com o Claude Code

A aba **Sessões** (tecla `6`) gerencia tarefas executadas pelo Claude Code
em worktrees isolados — útil para trabalhar duas tarefas ao mesmo tempo no
mesmo repositório.

Nas abas GitLab/Jira, `c` cria uma sessão para o item selecionado: a TUI
muda para a aba Sessões e abre um textbox para você explicar a tarefa
(`ctrl+d` confirma). O **modo** é deduzido do item: seu MR ou uma issue →
**desenvolvimento** (o pipeline abaixo); o MR de outra pessoa que aguarda
seu review → **revisão**, em que o Claude só analisa o MR e te entrega um
parecer, sem mexer no código. `ctrl+r` alterna os dois antes de iniciar.
Uma issue Jira ganha uma branch nova com o nome da
chave (`ABC-123`; defina `branch_prefix` em `[claude]` para algo como
`feature/`), criada a partir do default do remoto (`origin/HEAD`)
atualizado — se a branch já existir (sessão anterior da mesma issue), ela
é reaproveitada; um MR usa a branch existente. O serviço (repositório em `sources_dir`) é
deduzido da `#TAG`/projeto do MR; se não der, você escolhe numa lista. O
worktree é criado em `<worktrees_dir>/<id-da-sessão>` e o pipeline inicia
automaticamente.

Cada execução é um **pipeline de três agents** headless (`claude -p
--output-format stream-json`), todos com o contexto completo da tarefa —
sua explicação, descrição e comentários da issue no Jira, descrição e
comentários do MR ligado, e o template do serviço (`[claude.templates]`):

1. **plan** (opus) — explora o repositório (e o que já existe na branch, no
   caso de MR) e grava o plano em `WMONIT_PLAN.md` na raiz do worktree (o
   arquivo é excluído do git via `info/exclude`, não suja o status).
2. **dev** (sonnet) — implementa seguindo o plano e faz commits (sem push).
3. **review** (opus) — revisa o diff e responde com um veredito (APROVADO
   ou AJUSTES NECESSÁRIOS); não altera código.

Os agentes são instruídos a **não rodar build nem testes**: o worktree
isolado não builda (submodules/dependências não resolvem nele), então a
validação fica por sua conta depois do `f`/merge da branch.

**Sessão de revisão** (modo revisão): em vez do pipeline, roda uma fase
única que revisa o MR de outra pessoa — explora o diff contra a branch base,
aponta bugs/riscos/desvios de convenção com `arquivo:linha` e fecha com uma
recomendação (aprovar ou pedir ajustes) e comentários prontos para você colar
no GitLab. Não há plan, dev nem ciclo de correção; ao terminar, `enter`
mostra o parecer, `t` abre o Claude interativo para aprofundar e `f` conclui.

**Cada fase tem um gate manual:** ao terminar, a sessão fica
"⏸ aguardando aprovação" — `enter` mostra o resultado da fase (e o
`WMONIT_PLAN.md` completo) no próprio TUI, e `s` aprova e dispara a fase
seguinte. Quando o review termina, `s` inicia o **ciclo de correção**:
retoma a conversa do agente dev (cada fase tem sua conversa preservada)
com o veredito do review, aplica os ajustes e volta para o gate de review.
Numa fase que falhou, `s` tenta de novo retomando a conversa daquela fase.

O modelo de cada fase é configurável em `[claude.models]` (alias `opus`/
`sonnet`/`haiku` ou id completo, passado via `--model`):

```toml
[claude.models]
plan = "opus"     # raciocínio profundo para entender e planejar
dev = "sonnet"    # rápido e econômico com o plano já pronto
review = "opus"   # precisão para achar bugs
```

Como ninguém aprova ferramenta numa execução headless, o pipeline roda com
`--permission-mode bypassPermissions` por padrão (o worktree é isolado, mas
o agente ganha bash livre); configure `permission_mode` na seção `[claude]`
para um modo mais restrito, ciente de que build/commit podem ser negados.

O progresso (fase, turnos, ferramentas, último texto) aparece ao vivo e uma
notificação de desktop avisa quando o pipeline termina ou falha.

Na aba Sessões:

- `s` — inicia o pipeline (pendente) · aprova a fase (aguardando) · tenta
  de novo (falhou) · ciclo de correção (pronta).
- `enter`/`p` — resultado das fases, veredito e plano no painel de detalhes.
- `t` — abre o Claude **interativo** no worktree (a TUI volta ao sair).
- `v` — mostra o diff do worktree · `e` — abre no editor (`$VISUAL`/VS Code).
- `f` — conclui: remove o worktree (recusa se houver mudanças não
  commitadas; `F` força) e marca a sessão como concluída.
- `x` — cancela a execução em andamento · `d`/`D` — remove a sessão.

As sessões ficam em `~/.local/share/wmonit/sessions.json` e os logs em
`~/.local/share/wmonit/logs/`. Configure a pasta de fontes, os worktrees e o
binário na seção `[claude]` do `config.toml`.
