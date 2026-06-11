# wmonit

Monitor de trabalho no terminal: mostra sua atividade no GitLab e no Jira da
Nelogica, suas pendências do dia e dos próximos dias, e mantém uma lista de
tarefas manuais.

## Build

```sh
go build -o wmonit .
```

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
| `1`–`5` / `tab`  | troca de aba (Hoje, Desempenho, GitLab, Jira, Tarefas) |
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

Itens que pedem atenção — vencendo hoje/atrasados e reviews aguardando você —
aparecem num **realce no topo** da tela enquanto existirem.

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

Nas abas GitLab/Jira, `c` cria uma sessão para o item selecionado: uma issue
Jira ganha uma branch nova `feature/ABC-123`; um MR usa a branch existente.
O serviço (repositório em `sources_dir`) é deduzido da `#TAG`/projeto do MR;
se não der, você escolhe numa lista. O worktree é criado em
`<worktrees_dir>/<id-da-sessão>`.

Na aba Sessões:

- `s`/`enter` — executa o Claude headless (`claude -p --output-format
  stream-json`) no worktree, com prompt montado da descrição da task mais
  as instruções do template do serviço (`[claude.templates]`). Em uma sessão
  já concluída, retoma a conversa (`--resume`). O progresso (turnos,
  ferramentas, último texto) aparece ao vivo e uma notificação de desktop
  avisa quando termina.
- `t` — abre o Claude **interativo** no worktree (a TUI volta ao sair).
- `v` — mostra o diff do worktree · `e` — abre no editor (`$VISUAL`/VS Code).
- `f` — conclui: remove o worktree (recusa se houver mudanças não
  commitadas; `F` força) e marca a sessão como concluída.
- `x` — cancela a execução em andamento · `d`/`D` — remove a sessão.

As sessões ficam em `~/.local/share/wmonit/sessions.json` e os logs em
`~/.local/share/wmonit/logs/`. Configure a pasta de fontes, os worktrees e o
binário na seção `[claude]` do `config.toml`.
