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
| `1`–`6` / `tab`  | troca de aba (Hoje, Desempenho, GitLab, Jira, Tarefas, Agentes) |
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

## Agents do Claude Code

A aba **Agentes** (tecla `6`) cria e acompanha agents do Claude Code que
rodam em **background** (`claude --bg`) — os mesmos que o `claude agents`
(agent view) lista. Cada tarefa é **um agent só**, criado quando você pede:
ele entende, planeja e implementa numa conversa, e você entra nela quando
quiser (`a`) para responder, aprovar uma permissão ou corrigir o rumo.

**Criar um agent:**

- Nas abas GitLab/Jira, `c` no item selecionado: a TUI vai para a aba
  Agentes e abre um textbox para você explicar a tarefa (`ctrl+d` lança,
  `ctrl+e` edita no editor). O **modo** é deduzido do item: seu MR ou uma
  issue → **desenvolvimento**; o MR de outra pessoa que aguarda seu review →
  **revisão** (o agent só analisa e entrega um parecer com comentários
  prontos para colar). `ctrl+r` alterna os dois.
- Uma issue ganha um **worktree** com uma branch nova com o nome da chave
  (`ABC-123`; `branch_prefix` em `[claude]` para algo como `feature/`),
  criada a partir do default do remoto; um MR usa a branch existente. O
  serviço vem da `#TAG`/projeto do MR; se não der, você escolhe numa lista.
- Na aba Agentes, `n` cria um **agent livre**: sem issue/MR, roda direto no
  repositório escolhido (sem worktree próprio).

O prompt leva o contexto completo da tarefa — sua explicação, descrição e
comentários da issue, descrição e comentários do MR ligado e o template do
serviço (`[claude.templates]`). O agent é instruído a **não rodar build nem
testes** (o worktree isolado não builda), a commitar seguindo o CLAUDE.md e
a não dar push.

**Acompanhar:** a aba lista os agents do wmonit e, à parte, as **outras
sessões** vivas do agent view, com estado (▶ trabalhando · ⏸ aguarda você),
contexto, custo, subagents e a última resposta. Uma **notificação** avisa
quando um agent do wmonit para de trabalhar. No topo aparecem as
**colisões**: a mesma tarefa aberta em dois worktrees, dois agents no mesmo
worktree e o mesmo arquivo editado por mais de um agent. Contexto, custo e
arquivos editados vêm do mod `fleet` (`~/.claude/mods/fleet`); sem ele, o
contexto sai do transcript e o resto fica em branco.

Na aba Agentes:

- `enter`/`p` — detalhes: uso, arquivos editados, sua explicação e as
  últimas respostas do agent.
- `a` — abre a conversa numa **aba nova do terminal** (Windows Terminal;
  `claude attach`, ou o Claude interativo retomando a conversa num agent já
  encerrado) e o wmonit segue na tela · `A` — abre aqui mesmo, suspendendo
  a TUI até você sair.
- `i` — o **haiku** lê por cima o agent selecionado (tarefa, git e últimas
  respostas) e devolve um nome e **o que falta**; `I` faz isso com todos. O
  nome também é aplicado à sessão no agent view (via mod `fleet`, com
  `/rename`). Roda sozinho quando um agent do wmonit para de trabalhar.
- `s` — lança o agent de uma sessão sem agent vivo (retoma a conversa, se
  houver) · `x` — para o agent (a conversa fica).
- `v` — diff do worktree · `e` — abre a pasta no VS Code · `o` — abre a
  issue/MR.
- `f` — conclui: remove o worktree (recusa se houver mudanças não
  commitadas; `F` força), tira o agent do agent view e marca como concluída.
- `d`/`D` — remove a sessão da lista (e o worktree).

O modelo e o modo de permissão dos agents vêm de `model` e
`permission_mode` em `[claude]`; vazios, valem os do seu `settings.json`.
As sessões ficam em `~/.local/share/wmonit/sessions.json`; as do antigo
pipeline (plan → dev → review) continuam listadas e `s` lança um agent para
elas.
