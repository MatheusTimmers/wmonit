# Análise de arquitetura — wmonit

Cada ponto traz o trecho real do código, o porquê do problema e um esboço de
como ficaria.

---

## 1. `Model` god-object + sopa de booleanos

**Trecho** (`internal/ui/ui.go:61-120`):

```go
type Model struct {
	cfg   config.Config
	store *tasks.Store
	hist  *history.Store
	sess  *session.Store
	// ...
	describing     bool
	pickingService bool
	// ...
	adding bool
	report bool
	// ...
	filtering   bool
	// ...
	detail        bool
	// ...
}
```

São 6 flags de modo (`adding`, `describing`, `pickingService`, `filtering`, `detail`, `report`) que são **mutuamente exclusivas, mas nada no tipo garante isso**. A exclusão só existe porque a ordem de checagem no dispatch bate com a ordem na view:

`internal/ui/ui.go:404-423`:
```go
case tea.KeyMsg:
	if m.adding {
		return m.updateAdding(msg)
	}
	if m.describing {
		return m.updateDescribe(msg)
	}
	if m.pickingService {
		return m.updatePickService(msg)
	}
	if m.filtering {
		return m.updateFilter(msg)
	}
	if m.detail {
		return m.updateDetail(msg)
	}
	if m.report {
		return m.updateReport(msg)
	}
	return m.updateKeys(msg)
```

E **a mesma cascata, repetida**, em `internal/ui/view.go:195-226`:
```go
func (m Model) content() string {
	if m.describing {
		return m.viewDescribe()
	}
	if m.pickingService {
		return m.viewPickService()
	}
	if m.detail { ... }
	if m.report {
		return m.viewReport()
	}
	switch m.tab { ... }
}
```

**Por que mudar:** a verdade do "modo atual" está espalhada em 6 bools e replicada em dois lugares que precisam ficar na mesma ordem. Adicionar um modo novo (ex.: um diálogo de confirmação) exige inserir o bool, e lembrar de colocá-lo na posição certa nas **duas** cascatas. Se duas flags ficarem `true` por engano (ex.: entrar em `detail` sem limpar `filtering`), o comportamento passa a depender de qual `if` vem primeiro — bug silencioso e difícil de rastrear.

**Como ficaria:** um único enum torna o estado impossível de ficar inconsistente e centraliza o dispatch:

```go
type mode int
const (
	modeNormal mode = iota
	modeAdding
	modeDescribing
	modePickingService
	modeFiltering
	modeDetail
	modeReport
)

// no Update:
switch m.mode {
case modeAdding:        return m.updateAdding(msg)
case modeDescribing:    return m.updateDescribe(msg)
// ...
default:                return m.updateKeys(msg)
}
```

Bônus relacionado: o `Model` mistura receivers por valor e ponteiro. `checkReminders` é por **valor** mas muta um map (`ui.go:148`, `m.notified[key] = true` — funciona só porque map é referência), enquanto `scrollToItem` é por **ponteiro** (`ui.go:600`). Isso navega certo hoje, mas um método futuro `func (m Model) f() { m.cursor++ }` perderia a mutação sem aviso, porque `m` é cópia.

---

## 2. Lógica de pipeline de negócio dentro da UI

**Trecho** (`internal/ui/sessions.go:727-786`, `runPhase`):

```go
func (m Model) runPhase(s *session.Session, phase, resumeID string) (tea.Model, tea.Cmd) {
	s.Phase = phase
	s.Status = session.StatusRunning
	// ...
	run := func() tea.Msg {
		if resumeID != "" {
			if phase == session.PhaseDev && verdict != "" {
				opts.Prompt = claude.FixPrompt(verdict)
			} else {
				opts.Prompt = claude.ResumePrompt()
			}
			// ...
		}
		ctx := cached
		if ctx == nil {
			c := fetchTaskContext(cfg, sess, mr) // ← rede dentro do pacote ui
			ctx = &c
		}
		switch phase {
		case session.PhaseDev:    opts.Prompt = claude.DevPrompt(*ctx)
		case session.PhaseReview: /* ... */
		default:                  opts.Prompt = claude.PlanPrompt(*ctx)
		}
		err := claude.Run(opts, h)
		return sessFinishedMsg{id: sess.ID, prompt: opts.Prompt, ctx: ctx, err: err}
	}
	return m, tea.Batch(run, m.maybeTick())
}
```

E a decisão de "o que `s` faz a seguir" em `startRun` (`sessions.go:679-722`):
```go
switch s.Status {
case session.StatusPending:  return m.runPhase(s, session.PhasePlan, "")
case session.StatusWaiting:
	next := session.NextPhase(s.Phase)
	// ...
	return m.runPhase(s, next, "")
case session.StatusFailed:   /* retoma a fase */
case session.StatusDone:     /* ciclo de correção: retoma o dev */
}
```

**Por que mudar:** essa é a regra mais complexa do programa (máquina de estados de 3 fases + gates + ciclo de correção + modo revisão) e está acoplada ao `tea.Model`/`tea.Cmd`, com `fetchTaskContext` (chamadas a Jira e GitLab, `sessions.go:630`) morando no pacote de **view**. Consequência prática: **não dá para testar a transição "review reprovado → ciclo de correção retoma o dev"** sem montar o `Model` inteiro, a TUI e a rede. Por isso `sessions.go` (901 linhas) é o maior arquivo do projeto e o menos testável.

**Como ficaria:** uma função pura no pacote `session` que decide a próxima ação, independente da UI:

```go
// no pacote session — testável sozinho
type NextAction struct {
	Phase    string
	ResumeID string // vazio = fase nova
	UseFix   bool   // prompt de correção em vez de resume simples
}

func (s Session) Plan() (NextAction, bool) {
	if s.IsReview() { /* ... */ }
	switch s.Status {
	case StatusPending: return NextAction{Phase: PhasePlan}, true
	case StatusDone:    return NextAction{Phase: PhaseDev, ResumeID: s.ClaudeIDs[PhaseDev], UseFix: true}, true
	// ...
	}
}
```

A UI passa a só executar `NextAction`. A lógica vira testável com table tests; o pacote `ui` para de importar `jira`/`gitlab` para orquestrar.

---

## 3. Metadados do Jira re-buscados a cada refresh

**Trecho** (`internal/jira/client.go:361-366`):

```go
func (c *Client) Fetch() (*Summary, error) {
	if c.base == "" || c.token == "" { /* ... */ }
	cx := c.resolveCXField()       // ← GET /rest/api/2/field  (todo refresh)
	prios := c.resolvePriorities() // ← GET /rest/api/2/priority (todo refresh)
	// ...
}
```

`resolveCXField` (`client.go:260-277`) varre **todos** os campos do Jira procurando um com "complex" no nome; `resolvePriorities` (`client.go:244-256`) baixa a lista de prioridades. Ambos a cada `Fetch()`.

**Por que mudar:** isso roda a cada 5 minutos (e a cada `r` manual), mas a lista de campos customizados e a de prioridades do projeto **não mudam entre execuções** — são metadados estáticos. São 2 round-trips de rede desperdiçados por refresh, e `resolveCXField` ainda baixa o catálogo inteiro de fields só para achar uma string. Num dia de trabalho são centenas de chamadas sem nenhum dado novo.

**Como ficaria:** memoizar no `Client` (vive pelo processo):

```go
func (c *Client) resolvePriorities() map[string]int {
	if c.priosCached != nil {
		return c.priosCached
	}
	// ... busca como hoje ...
	c.priosCached = m
	return m
}
```

Custo trivial, elimina o tráfego repetido.

---

## 4. Default de `SourcesDir` é um caminho Windows hardcoded

**Trecho** (`internal/config/config.go:79-84`):

```go
if cfg.Claude.SourcesDir == "" {
	cfg.Claude.SourcesDir = "c:/Fontes"
}
if cfg.Claude.WorktreesDir == "" {
	cfg.Claude.WorktreesDir = filepath.Join(cfg.Claude.SourcesDir, ".worktrees")
}
```

**Por que mudar:** o default `"c:/Fontes"` é aplicado em **qualquer** plataforma. Em Linux (Fedora), usando o wmonit localmente, sem `config.toml` o `DetectServices` vai tentar ler `c:/Fontes`, falhar, e a tecla `c` (criar sessão) vai dar erro "lendo c:/Fontes: ...". É um default que não funciona no ambiente e ainda leva o `WorktreesDir` junto para o caminho errado.

**Como ficaria:** ou vazio com mensagem clara na hora de usar, ou um default sensato por plataforma — por exemplo `~/Projects` / `~/src` no Unix. O importante é não fixar um caminho de outro SO como default universal.

---

## 5. Sem `context.Context` / cancelamento real nos fetches

**Trecho** (`internal/ui/ui.go:41-45` e `234-239`):

```go
// gen marca de qual rodada de fetch a resposta veio: um refresh manual no
// meio de outro em voo invalida as respostas antigas (...)
type glMsg struct {
	gen int
	sum *gitlab.Summary
	err error
}
```
```go
func (m *Model) refresh() tea.Cmd {
	m.fetchGen++
	m.loading = 2
	return m.fetchAll()
}
```

E o descarte da resposta velha (`ui.go:272-275`):
```go
case glMsg:
	if msg.gen != m.fetchGen {
		return m, nil // resposta de uma rodada já substituída
	}
```

**Por que mudar:** o `fetchGen` é um **workaround** para a ausência de cancelamento. Quando você aperta `r` no meio de um fetch, a requisição antiga continua viva até o timeout de 15s — só a *resposta* é ignorada. Você paga o request e segura uma goroutine à toa. Os clients (`gitlab.New`, `jira.New`) usam `http.NewRequest` sem `context` (`gitlab/client.go:151`, `jira/client.go:58`), então não há como abortar.

**Como ficaria:** threading de `context.Context` no client (`http.NewRequestWithContext`) permite cancelar de verdade a rodada anterior, e o `fetchGen` deixa de ser necessário:

```go
func (c *Client) Fetch(ctx context.Context) (*Summary, error) { ... }
// refresh cancela o ctx anterior antes de abrir o novo
```

Não é urgente (o timeout limita o estrago), mas é a forma idiomática e elimina um campo de estado que existe só para compensar a falta dela.

---

## 6. Boilerplate de persistência triplicado

**Trecho** — a *mesma* convenção de diretório em 3 pacotes:

`internal/tasks/tasks.go:39-45`:
```go
func storePath() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "wmonit", "tasks.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "wmonit", "tasks.json")
}
```

`internal/history/history.go:29-35` — **idêntico**, trocando `tasks.json` por `history.json`. `internal/session/session.go:125-133` — idem via `dataDir()`.

E o par Load/Save repetido (`tasks.go:65-75`, igual em history e session):
```go
func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.Tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}
```

**Por que mudar:** a regra "onde ficam os dados do wmonit" está escrita 3 vezes. Se mudar a convenção (ex.: respeitar `WMONIT_DATA_DIR`, ou migrar de `~/.local/share`), precisa lembrar de alterar nos 3 — e o `config` usa `XDG_CONFIG_HOME` enquanto os stores usam `XDG_DATA_HOME`, então a chance de divergência é real. O Load/Save é o mesmo algoritmo (MkdirAll + MarshalIndent + WriteFile) copiado.

**Como ficaria:** um helper de caminho e um store JSON genérico (Go 1.18+):

```go
// internal/paths
func DataFile(name string) string { /* XDG_DATA_HOME ou ~/.local/share/wmonit */ }

// internal/store
type JSON[T any] struct{ path string }
func (s JSON[T]) Load() (T, error) { ... }
func (s JSON[T]) Save(v T) error   { ... } // MkdirAll + MarshalIndent + WriteFile
```

Os 3 pacotes passam a declarar só o `name` e o tipo. Some também a duplicação de regex de chave Jira (`session.go:118` vs `gitlab/client.go:115`) e dois parsers de `References.Full` (`shortRef` em `view.go:302`, `projectOf` em `sessions.go:183`).

---

## 7. `jira/client.go` sem nenhum teste

**Trecho** — o parsing mais arriscado do projeto, sem cobertura. Ex.: `complexityValue` (`jira/client.go:109-133`) que tenta string, depois número, depois `{value}`:

```go
func complexityValue(raw json.RawMessage, field string) string {
	// ...
	var s string
	if json.Unmarshal(v, &s) == nil { return s }
	var n float64
	if json.Unmarshal(v, &n) == nil { return strconv.FormatFloat(n, 'f', -1, 64) }
	var opt struct{ Value string `json:"value"` }
	if json.Unmarshal(v, &opt) == nil && opt.Value != "" { return opt.Value }
	return ""
}
```

E o fallback de endpoint Cloud em `search` (`client.go:226-238`):
```go
issues, code, err := c.doSearch("/rest/api/2/search", jql, max, cxField, prios)
if code == http.StatusGone || code == http.StatusNotFound {
	issues2, _, err2 := c.doSearch("/rest/api/3/search/jql", jql, max, cxField, prios)
	// ...
}
```

**Por que mudar:** `gitlab/client.go` tem `client_test.go`, mas `jira` (o client mais complexo — ADF vs texto puro em `textOf`, paginação dupla por `startAt`/`nextPageToken` em `doSearch`, `dateOnly` com fuso, fallback 410→v3) **não tem teste algum**. É exatamente a parte onde um JSON inesperado do servidor passa despercebido: `dateOnly` perto da meia-noite, complexidade que vem como select, o `nextPageToken` que só existe no Cloud. Um bug aqui se manifesta como número errado na aba Desempenho, difícil de notar.

**Como ficaria:** testes de unidade nas funções puras (`complexityValue`, `dateOnly`, `textOf`) — rápidos, sem rede — e um teste de `doSearch`/`search` com `httptest.Server` simulando v2, o 410, e a paginação. É a melhor relação esforço/risco entre as lacunas de teste.

---

## Menores (com trecho)

**Render duplicado** — `View` seta o conteúdo no viewport (`view.go:186`):
```go
vp := m.vp
vp.SetContent(m.content())
```
mas os handlers de scroll recomputam tudo de novo para achar a linha do cursor (`ui.go:600-608`):
```go
func (m *Model) scrollToItem() {
	content, sel := renderRows(m.currentRows(), m.cursor) // reconstrói a lista inteira
	m.vp.SetContent(content)
	// ...
}
```
`currentRows()` refiltra e reordena a lista a cada tecla **e** a cada render. *Por quê:* duas fontes de verdade para "o conteúdo desenhado" e trabalho repetido; tudo bem no volume atual, mas é o tipo de coisa que trava quando a lista cresce.

**Clients criados ad hoc** — `gitlab.New(...)`/`jira.New(...)` instanciados em `fetchAll` (`ui.go:224,228`) e de novo em `fetchTaskContext` (`sessions.go:642,647`). *Por quê:* nenhum estado compartilhado (ver #3, o cache de metadados não sobrevive), e a config de auth fica espalhada.

**TODOs que engolem erro** — `tasks.parseDue` (`tasks.go:144-148`):
```go
fields := strings.Fields(text[idx+1:])
if len(fields) == 0 || len(fields) > 2 {
	return text, "", ""
	// TODO: Retornar error
}
```
*Por quê:* uma tarefa com data malformada (`@amanha` em vez de `@tomorrow`) é aceita silenciosamente **sem** data — o usuário acha que agendou e não agendou. O `// TODO: Retornar error` reconhece a dívida; hoje vira bug de UX invisível.

---

## Ordem sugerida

Pela relação custo/benefício: começar por **#3 (cache Jira)** e **#4 (SourcesDir)** — correções de poucos minutos e de impacto imediato —, depois **#7 (testes de Jira)** e **#6 (helper de store)** para pagar dívida estrutural; **#1/#2** são os refactors maiores.
