# wmonit — revisão de design e plano de reorganização

Documento para orientar uma refatoração de arquitetura. O objetivo é reduzir a
concentração de responsabilidades em `internal/ui` (~3.600 das ~6.700 linhas do
projeto) e trocar os pontos de acoplamento improvisados por composição
explícita — **sem mudar comportamento observável**.

Contexto: TUI em bubbletea que monitora GitLab/Jira, mantém tarefas locais e
orquestra sessões do Claude Code em worktrees isolados. Código e comentários em
pt-BR. Há mudanças não commitadas na working tree que são **intencionais** —
preserve-as. Não commite nada a menos que o dono do repo peça; se pedir, o
estilo é: mensagem de uma frase só (título), sem corpo, sem Co-Authored-By.

Regras gerais:

- Comportamento idêntico ao final de cada etapa: `go build ./...`,
  `go vet ./...`, `go test ./...` e `gofmt -l .` limpos.
- Sem dependências novas.
- Interfaces **só** onde este documento indica (onde já existem duas
  implementações ou nil-checks fazendo papel de polimorfismo). Não criar
  interfaces para `store`, `worktree`, `claude`, `tasks` ou `history` — têm
  uma implementação só e os testes não sofrem por isso.
- Não mexer nos TODOs de produto (`config.go` em `Goals`/`Templates`,
  `tasks.go` em `priorityAliases`) — são decisões pendentes do dono.
- As etapas são independentes na medida do possível e devem ser feitas na
  ordem abaixo (a 2 depende da 1).

---

## Etapa 1 — Composition root: fonte de dados como interface, storage sem `os.Setenv`

**Problema.** O modo demo está espalhado por três mecanismos distintos:

1. `ui.Model.fetchAll` (`internal/ui/ui.go`) tem um `if m.demo` que chama
   `demo.GitLab()`/`demo.Jira()` — a UI importa `internal/demo` e conhece as
   duas implementações em vez de depender de um contrato.
2. `main.go` faz `os.Setenv("XDG_DATA_HOME", …)` antes de carregar os stores —
   variável de ambiente global como mecanismo de injeção de dependência.
3. `jiraDetail()` em `internal/ui/sessions.go` e `fetchTaskContext` toleram
   client `nil` (caso demo/testes) com nil-checks — polimorfismo manual.

**Design proposto.**

- Definir no lado consumidor (`internal/ui`) duas interfaces pequenas,
  não exportadas, com exatamente o que a UI usa:

  ```go
  type gitlabSource interface {
      Fetch(ctx context.Context) (*gitlab.Summary, error)
      MRNotes(ctx context.Context, projectID, iid int) ([]gitlab.Note, error)
  }
  type jiraSource interface {
      Fetch(ctx context.Context) (*jira.Summary, error)
      IssueDetail(ctx context.Context, key string) (*jira.IssueDetail, error)
  }
  ```

  Os clients reais já satisfazem esses contratos sem mudança. Criar em
  `internal/demo` implementações (`demo.GitLabSource`/`demo.JiraSource` ou um
  tipo só) que devolvem os dados inventados — `MRNotes`/`IssueDetail` podem
  devolver algo fixo plausível, o que de quebra faz `enter` (detalhe) funcionar
  no demo em vez de dar "jira não configurado".
- `ui.New` passa a receber as fontes prontas; `main.go` vira o composition
  root: decide entre client real e demo e injeta. `internal/ui` **deixa de
  importar** `internal/demo`; some o campo `demo bool` de uso lógico (pode
  restar só para o selo "🧪 DEMO" do rodapé — ou virar um campo `badge string`).
- Remover os nil-checks de client em `sessions.go` (`jiraDetail`,
  `fetchTaskContext`) — com interface sempre presente, eles somem.
- Storage: em vez do `os.Setenv` no `main.go`, os stores recebem o caminho.
  `tasks.Load`, `session.Load` e `history.Load` ganham variante
  `LoadFrom(dir string)` (ou passam a receber o dir; escolher UMA forma e
  aplicar às três). `paths.DataDir()` continua sendo o default usado pelo
  `main` no modo normal; no demo, `main` cria `os.MkdirTemp` e passa esse dir
  para os três stores e para o `demo.SeedData(dir)`. `session.LogDir()` também
  deve derivar do dir injetado (hoje usa `paths.DataDir()` fixo).
- Testes: os testes de UI que hoje montam `Model` na mão passam a poder usar
  as fontes demo — verificar se algum stub duplicado pode ser apagado.

**Resultado esperado.** `grep -r "internal/demo" internal/ui` vazio; nenhum
`os.Setenv` no projeto; nenhum nil-check de client em `internal/ui`.

---

## Etapa 2 — Extrair a orquestração do pipeline da UI

**Problema.** `internal/ui/sessions.go` (~880 linhas) mistura três coisas:
teclas/render do bubbletea, a lógica de negócio do pipeline e o acesso a
rede/processos. Funções como `fetchTaskContext`, `mrFor`, `noteLines`,
`runPhase` (montagem de `claude.Opts`, escolha de prompt por fase, registro de
`Handle`, nome do log) e o cache `taskCtx` não precisam saber o que é um
`tea.Msg`. A máquina de estados pura já foi extraída (`session.Plan()` — manter
como está), mas a execução não.

**Design proposto.** Novo pacote `internal/pipeline`:

```go
// Runner executa fases do pipeline de uma sessão e guarda o estado de
// runtime (handles vivos, contexto de tarefa cacheado por sessão).
type Runner struct {
    cfg    config.Claude
    gitlab gitlabSource // as mesmas interfaces da etapa 1 (redeclaradas aqui,
    jira   jiraSource   // no consumidor — Go permite a duplicação de contrato)
    // handles e taskCtx saem do ui.Model para cá
}

// BuildContext monta o TaskContext (Jira + GitLab); mrs é a lista em memória
// para achar o MR ligado (substitui o mrFor do ui, que vira função daqui
// recebendo os MRs em vez de ler m.gl).
func (r *Runner) BuildContext(sess session.Session, mrs []gitlab.MR) claude.TaskContext

// RunPhase executa a Action (bloqueante — o chamador embrulha em tea.Cmd).
// Resolve prompt/modelo/log/resume e devolve o prompt usado.
func (r *Runner) RunPhase(sess session.Session, act session.Action, ctx *claude.TaskContext) (prompt string, err error)

func (r *Runner) Cancel(id string)      // mata o handle, se houver
func (r *Runner) Running(id string) bool
```

- `ui.Model` guarda um `*pipeline.Runner` e os métodos de tecla viram casca
  fina: `startRun` chama `s.Plan()`, atualiza status/fase/log na sessão, salva,
  e dispara `tea.Cmd` que chama `runner.RunPhase` e devolve `sessFinishedMsg`.
- `progress map[string]claude.Progress` e o polling podem ficar na UI (é
  estado de exibição), lendo via `claude.ReadProgress` como hoje.
- Mover para o novo pacote: `fetchTaskContext`, `mrFor` (recebendo `[]gitlab.MR`),
  `noteLines`, `mrRef`, a lógica central de `runPhase`. Ficam na UI:
  teclas, `viewSessoes`, `viewDescribe`, `pendingSession`, mensagens `tea.Msg`.
- Testes: `BuildContext` e a escolha de prompt por `Action` ganham testes
  unitários diretos no pacote novo (hoje só são exercitados via UI).

**Resultado esperado.** `sessions.go` encolhe para a metade ou menos;
`internal/pipeline` não importa bubbletea nem lipgloss.

---

## Etapa 3 — Decompor o `ui.Model` por preocupação

**Problema.** `Model` tem ~40 campos planos misturando estados independentes;
qualquer função vê tudo.

**Design proposto.** Composição (structs, não interfaces), mantendo o `Model`
como agregador bubbletea:

- `fetchState`: `gen`, `ctx`, `cancel`, `loading`, `gl`, `glErr`, `ji`,
  `jiErr`, `mine`, `updated` + métodos `refresh`/descartar-rodada-velha.
- `alertState`: `notified`, `seenTodos`, `issueStatus`, `glBaseline`,
  `jiBaseline` + os métodos de `alerts.go` viram métodos dele (já devolvem
  `[]string`, seguem testáveis).
- `detailState`: `detailLoading/Title/URL/Body`.
- `sessionUI`: `descInput`, `pending`, `pickOptions`, `pickCursor`, `sessInfo`,
  `progress`, `ticking` (os handles/taskCtx já foram para o Runner na etapa 2).

Regras: nada de getter/setter; campos acessados direto (`m.fetch.gl`). Mover
junto os métodos que só tocam um sub-estado. Não quebrar a assinatura
`Update/View` do bubbletea.

---

## Etapa 4 — Limpezas pontuais de duplicação e fragilidade

Em ordem de valor:

1. **`session.Store.Find` devolve ponteiro para dentro do slice**
   (`internal/session/session.go`). Um `Add` posterior pode realocar o slice e
   deixar o ponteiro apontando para memória velha — hoje funciona porque os
   call sites são cuidadosos, mas é armadilha. Trocar por
   `Update(id string, fn func(*Session)) bool` (e um `Get(id) (Session, bool)`
   por valor para leitura), migrando os call sites em `internal/ui`.
2. **Três helpers de scroll quase idênticos** em `ui.go` (`scrollToItem`,
   `scrollToSession`, `scrollToCursor`) — unificar num
   `scrollTo(content string, line int)`.
3. **Busca de MRs ligados duplicada**: `mrBadge` (`view.go`) e `linkedMRsText`
   (`detail.go`) fazem o mesmo loop sobre `OpenMRs`/`Merged` casando
   `JiraKey()`. Extrair `linkedMRs(key) []struct{IID int; Merged bool}` (ou
   equivalente) e formatar em cada lugar.
4. **Erros de `Save()` ignorados** nos call sites de tasks/sessions
   (`m.store.Save()` sem checar). Padronizar: capturar e exibir no
   `sessInfo`/rodapé — perda silenciosa de tarefa é o pior caso do app.
5. **`viewDesempenho`** (`view.go`, ~230 linhas) — fatiar em funções por seção
   (tabela, metas, tendência, ritmo, composição). Só extração, sem mudar saída.

---

## Registrado, mas **fora de escopo** desta refatoração

(Deixar como está; listado para o dono decidir depois.)

- **Cache de render**: `View()`/`content()` reconstroem o texto da aba a cada
  frame do spinner. Custo aceitável hoje; um cache invalidado por
  dados/resize é otimização prematura enquanto não houver lag perceptível.
- **`claude.ReadProgress` relê o log inteiro a cada poll de 2s** — O(n²) ao
  longo de uma execução. Só vira problema com logs de dezenas de MB; um leitor
  incremental com offset por sessão resolveria.
- **Cursor único compartilhado entre abas** (reset ao trocar) — mudança de UX,
  não de design.
- **Paginação/`get` parecidos nos clients gitlab e jira** — a duplicação é
  pequena e os endpoints divergem; unificar renderia menos que custaria.

---

## Checklist de aceitação (rodar após CADA etapa)

```sh
go build ./... && go vet ./... && go test ./... && gofmt -l .
```

- Sem imports novos de terceiros (`go.mod` intocado).
- `internal/ui` sem import de `internal/demo` (a partir da etapa 1).
- `internal/pipeline` sem import de bubbletea/lipgloss (a partir da etapa 2).
- Comportamento idêntico: mesmas teclas, mesmos textos de tela, demo
  funcionando via `--demo`.
