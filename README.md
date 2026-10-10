# wmonit

Monitor de trabalho no terminal: mostra sua atividade no GitLab e no Jira da
Nelogica, suas pendências do dia e dos próximos dias, e mantém uma lista de
tarefas manuais.

## Build

```sh
go build -o wmonit .
```

Precisa do Go 1.26 ou mais novo (com o `GOTOOLCHAIN` padrão, o `go` baixa a
versão certa sozinho).

## Modo demo (sem GitLab/Jira)

Para ver as telas e testar a interface sem nenhuma conexão, rode com dados
inventados:

```sh
./wmonit --demo
```

O modo demo preenche GitLab e Jira com dados fictícios (MRs, fila de review,
issues e métricas), simula um herdr com alguns workspaces e semeia algumas tarefas — inclusive uma tarefa
**crítica** já vencida, para você ver o alerta constante em ação. Nada é
buscado na rede e seus dados reais não são tocados (as tarefas e workspaces do
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
| `1`–`6` / `tab`  | troca de aba (Hoje, Desempenho, GitLab, Jira, Tarefas, Workspaces) |
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

## Workspaces (hub do herdr)

A aba **Workspaces** (tecla `6`) é um hub de workspaces do
[herdr](https://herdr.dev): cada tarefa que você trata vira um workspace com
as abas **terminal**, **claude** e **nvim** (mais as que o repositório pedir)
na pasta do trabalho. O wmonit cria, acompanha e fecha esses workspaces, e
conversa com o Claude de cada um sem você sair da lista. Funciona no Linux e no
Windows; o herdr precisa estar aberto.

**Criar um workspace:**

- Nas abas GitLab/Jira, `c` no item selecionado. Uma issue ganha um
  **worktree** com uma branch nova (`branch_prefix` + chave, editável no
  formulário), criada a partir do default do remoto; um MR usa a branch
  existente. O MR de outra pessoa que aguarda seu review já vem em modo
  **revisão** (o Claude só analisa e entrega um parecer).
- Na aba Workspaces, `n` cria um workspace **livre**, direto na pasta do
  repositório, sem worktree próprio.
- `wmonit --open <chave ou link>` abre direto o formulário de uma issue ou MR
  (chave `ABC-123`, link do Jira, link do MR ou `projeto!123`).

O formulário pede o **repositório** (as subpastas git de `sources_dir`; `/`
filtra; o deduzido da `#TAG`/projeto do MR já vem selecionado), a branch, o
modo do MR, o **modelo** do Claude (`models` em `[claude]`), o que fazer
(`ctrl+e` abre no editor) e se a tarefa já vai para o Claude ou se ele só
fica esperando. `esc` cancela.

O contexto — sua explicação, descrição e comentários da issue, descrição e
comentários do MR ligado e os templates (`template` geral, `[claude.templates]`
por serviço e o `template` do `.wmonit.toml`) — vai para `WMONIT_TASK.md` na
raiz do workspace (fora do git via `info/exclude`), e o Claude recebe só "leia
o `WMONIT_TASK.md`". Se o Claude ainda não confia na pasta, ele para pedindo
confirmação: a linha mostra `⏳` e o pedido sai sozinho assim que ele ficar
livre.

**Conversa do Claude:** cada workspace fixa o id da conversa
(`--session-id`). Reabrir com `w` retoma a mesma conversa (`--resume`), mesmo
depois de reiniciar a máquina; se o histórico sumiu, começa uma nova. A linha
mostra quantos pedidos, tokens e a última atividade (lidos do transcript do
Claude Code); com `[claude.prices]`, também o custo estimado.

**Acompanhar:** cada linha mostra o estado do Claude como o herdr o vê
(▶ trabalhando · ⏸ precisa de você · ✔ terminou · ○ parado), os links com o
Jira (com o status) e o GitLab, a situação do MR (CI quebrado e quais jobs,
conflito, threads abertas, aprovações faltando, draft, pronto para merge) e
erros. O wmonit assina os **eventos** do herdr pelo socket, então a lista muda
na hora em que algo acontece; sem o socket ele volta a consultar a cada poucos
segundos. Uma **notificação** (desktop, toast do herdr ou as duas — `notify`
em `[herdr]`) avisa quando um Claude trava esperando você ou termina, e o
título da janela mostra quantos precisam de você. Workspaces do herdr que não
são de nenhuma tarefa aparecem em "Outros" e podem ser **adotados**.

**Sidebar do herdr:** o wmonit publica em cada workspace os tokens
`$wm_task`, `$wm_jira`, `$wm_mr`, `$wm_ci`, `$wm_git` e `$wm_todo`. Para vê-los
na sidebar, adicione ao `config.toml` do herdr:

```toml
[ui.sidebar.spaces]
rows = [["state_icon", "workspace"], ["branch", "git_status"], ["$wm_jira", "$wm_mr", "$wm_ci"], ["$wm_todo"]]
```

**Pendências:** a primeira linha é um agent dedicado (workspace `wmonit`)
que ajuda a lidar com tudo o que está em aberto. `i` monta um resumo — cada
workspace com o estado e a conversa do Claude, o fim da tela dele, git, Jira,
a situação do MR, mais as issues sem workspace, os MRs na sua fila e as
tarefas locais — em `~/.local/share/wmonit/pendencias/pendencias.md` e pede ao
agent o que está travado, como destravar cada coisa e o que fazer a seguir. A
resposta aparece no próprio wmonit assim que o arquivo é escrito (`enter` na
linha 🧭) e você continua a conversa com `p`, ou no herdr com `a`.

**Entregar e limpar:** `s` faz o push da branch, abre o MR no GitLab (título
com a `#CHAVE`, descrição com o link da issue e sua explicação; `draft_mr` em
`[gitlab]` abre como draft; precisa de token com scope `api`) ou reaproveita o
que já existe, e move a issue no Jira para `review_transition`. Ao criar um
workspace de issue, ela vai para `start_transition`. Workspaces cujo MR foi
mergeado/fechado ou cuja issue foi resolvida ganham um aviso 🧹; `C` mostra
quais são e, apertado de novo, conclui todos.

`?` abre um popup com todos os atalhos e a config em uso (aba 2; `e` edita o
arquivo e recarrega ao sair).

Na aba Workspaces:

- `enter` — detalhes: links, git, MR, conversa do Claude, sua explicação e a
  tela atual do Claude.
- `a` — abre o workspace no herdr · `p` — manda uma mensagem ao Claude dele
  (sobe um Claude novo se não houver) · `x` — manda `esc` (interrompe).
- `i` — pede a análise de pendências.
- `s` — entrega (push, MR, Jira) · `C` — limpa os que já terminaram.
- `L` — liga o workspace a uma issue do Jira ou a um MR (`/` filtra) · `u` —
  adota um workspace de fora · `w` — reabre um workspace que foi fechado.
- `v` — diff contra a branch base · `o` — abre a issue/MR no navegador.
- `f` — conclui: fecha o workspace e remove o worktree (recusa se houver
  mudanças não commitadas; `F` força). A branch continua no repositório.
- `d`/`D` — o mesmo, e tira o workspace da lista.

### Configuração por repositório (`.wmonit.toml`)

Um `.wmonit.toml` na raiz do repositório ajusta os workspaces dele:

```toml
setup = ["cp ../.env .env", "npm ci"]   # roda na aba terminal ao criar
model = "sonnet"                        # modelo padrão do Claude
permission_mode = "acceptEdits"
editor = "nvim ."
target_branch = "develop"               # destino do MR aberto com s
template = "Rode npm test antes de commitar."

[[tabs]]
label = "dev"
command = "npm run dev"
```

### Plugin do herdr

`wmonit herdr-plugin install` liga o wmonit ao herdr como plugin:

- ação `wmonit.open` — abre o wmonit num popup por cima do workspace atual;
- ctrl+clique num link do Jira ou de um MR (das URLs da sua config) abre o
  popup já no formulário daquela tarefa;
- ao iniciar, o herdr chama `wmonit herdr-action sync`, que republica os
  tokens da sidebar (eles não sobrevivem a um restart do servidor).

Para abrir com uma tecla, no `config.toml` do herdr:

```toml
[[keys.command]]
key = "prefix+m"
type = "plugin_action"
command = "wmonit.open"
description = "wmonit"
```

`wmonit herdr-plugin uninstall` desliga; `wmonit herdr-plugin manifest`
mostra o manifesto gerado.

Os workspaces ficam em `~/.local/share/wmonit/sessions.json`. Configure a pasta
de fontes, os worktrees e o Claude em `[claude]`, e o herdr em `[herdr]`
(`session` usa uma sessão nomeada; `events`, `metadata` e `notify` ligam e
desligam os recursos acima). `WMONIT_CONFIG` aponta outro arquivo de config.

### Teste contra um herdr real

```sh
herdr --session wmt server &
WMONIT_HERDR_IT=wmt go test ./internal/herdr ./internal/hub -run TestReal -v
WMONIT_HERDR_IT=wmt WMONIT_HERDR_IT_PROMPT=1 go test ./internal/hub -run TestRealHerdr -v
```

O segundo também confirma a pasta no Claude, entrega a tarefa, fecha e reabre o
workspace conferindo que a conversa foi retomada.
