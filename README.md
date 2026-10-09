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
três abas — **terminal**, **claude** e **nvim** — na pasta do trabalho. O
wmonit cria, acompanha e fecha esses workspaces, e conversa com o Claude de
cada um sem você sair da lista. Funciona no Linux e no Windows; o herdr precisa
estar aberto (o wmonit fala com ele pela CLI `herdr`).

**Criar um workspace:**

- Nas abas GitLab/Jira, `c` no item selecionado. Uma issue ganha um
  **worktree** com uma branch nova com o nome da chave (`ABC-123`;
  `branch_prefix` em `[claude]` para algo como `feature/`), criada a partir do
  default do remoto; um MR usa a branch existente. O MR de outra pessoa que
  aguarda seu review vira **revisão** (o Claude só analisa e entrega um
  parecer); `ctrl+r` alterna.
- Na aba Workspaces, `n` cria um workspace **livre**, direto na pasta do
  repositório, sem worktree próprio.
- Nos dois casos você **escolhe o repositório** (as subpastas git de
  `sources_dir`; o deduzido da `#TAG`/projeto do MR já vem selecionado) e
  escreve o que fazer. `ctrl+d` cria e manda a tarefa ao Claude; `ctrl+n` só
  cria (o Claude fica esperando você).

O contexto — sua explicação, descrição e comentários da issue, descrição e
comentários do MR ligado e o template do serviço (`[claude.templates]`) — vai
para `WMONIT_TASK.md` na raiz do workspace (fora do git via `info/exclude`), e
o Claude recebe só "leia o `WMONIT_TASK.md`". Se o Claude ainda não confia na
pasta, ele para pedindo confirmação: a linha mostra `⏳` e o pedido sai sozinho
assim que ele ficar livre.

**Acompanhar:** cada linha mostra o estado do Claude como o herdr o vê
(▶ trabalhando · ⏸ precisa de você · ✔ terminou · ○ parado), os links com o
Jira (com o status) e o GitLab, e erros. Uma **notificação** avisa quando um
Claude trava esperando você ou termina. Workspaces do herdr que não são de
nenhuma tarefa (abertos pelo `dev`, por exemplo) aparecem em "Outros" e podem
ser **adotados**.

**Pendências:** a primeira linha é um agent dedicado (workspace `wmonit`)
que ajuda a lidar com tudo o que está em aberto. `i` monta um resumo — cada
workspace com o estado do Claude, o fim da tela dele, git (commits e arquivos
pendentes), Jira e GitLab, mais as issues sem workspace, os MRs na sua fila e
as tarefas locais — em `~/.local/share/wmonit/pendencias/pendencias.md` e pede
ao agent o que está travado, como destravar cada coisa e o que fazer a seguir.
A resposta aparece no próprio wmonit (`enter` na linha 🧭) e você continua a
conversa com `p`, ou no herdr com `a`.

Na aba Workspaces:

- `enter` — detalhes: links, git, sua explicação e a tela atual do Claude.
- `a` — abre o workspace no herdr · `p` — manda uma mensagem ao Claude dele
  (sobe um Claude novo se não houver) · `x` — manda `esc` (interrompe).
- `i` — pede a análise de pendências.
- `L` — liga o workspace a uma issue do Jira ou a um MR · `u` — adota um
  workspace de fora · `w` — reabre um workspace que foi fechado no herdr.
- `v` — diff contra a branch base · `o` — abre a issue/MR no navegador.
- `f` — conclui: fecha o workspace e remove o worktree (recusa se houver
  mudanças não commitadas; `F` força). A branch continua no repositório.
- `d`/`D` — o mesmo, e tira o workspace da lista.

Os workspaces ficam em `~/.local/share/wmonit/sessions.json`. Configure a pasta
de fontes, os worktrees e o Claude em `[claude]`, e o binário do herdr e o
comando da aba de editor em `[herdr]`.
