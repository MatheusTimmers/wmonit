package session

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/timmers/wmonit/internal/paths"
	"github.com/timmers/wmonit/internal/store"
)

type Status string

const (
	StatusPending   Status = "pending"   // criada, ainda não executada
	StatusRunning   Status = "running"   // claude em execução
	StatusWaiting   Status = "waiting"   // fase concluída, aguardando aprovação (gate)
	StatusDone      Status = "done"      // pipeline terminou, aguardando fechamento
	StatusFailed    Status = "failed"    // claude saiu com erro
	StatusCompleted Status = "completed" // fechada: worktree removido
	StatusCancelled Status = "cancelled" // cancelada pelo usuário
)

const (
	KindIssue = "issue" // issue do Jira (branch nova)
	KindMR    = "mr"    // merge request existente
)

const (
	ModeImplement = "implement" // default; pipeline plan → dev → review
	ModeReview    = "review"    // só revisa o MR e reporta (1 fase)
)

const (
	PhasePlan   = "plan"   // agente 1: compila a tarefa e escreve o plano
	PhaseDev    = "dev"    // agente 2: implementa seguindo o plano
	PhaseReview = "review" // agente 3: revisa e reporta
)

func NextPhase(p string) string {
	switch p {
	case PhasePlan:
		return PhaseDev
	case PhaseDev:
		return PhaseReview
	}
	return ""
}

// Session é uma sessão de trabalho: task + worktree + execução do Claude.
type Session struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Key      string `json:"key"` // chave Jira (ABC-123) ou ref do MR (hades!9470)
	URL      string `json:"url,omitempty"`
	Service  string `json:"service"`  // nome do serviço em sources_dir
	Repo     string `json:"repo"`     // caminho do repositório principal
	Worktree string `json:"worktree"` // caminho do worktree
	Branch   string `json:"branch"`
	Prompt   string `json:"prompt,omitempty"`
	UserNote string `json:"user_note,omitempty"` // explicação digitada no wmonit
	Kind     string `json:"kind,omitempty"`      // origem: issue ou mr
	Mode     string `json:"mode,omitempty"`      // implement (default) ou review
	Phase    string `json:"phase,omitempty"`     // fase atual do pipeline (plan/dev/review)
	// ClaudeIDs guarda o session_id do Claude por fase — retomar a fase
	// certa exige a conversa certa. ClaudeID mantém o da última fase
	// (compatibilidade e modo interativo).
	ClaudeIDs map[string]string `json:"claude_ids,omitempty"`
	// Results guarda o resumo final de cada fase (plano, o que o dev fez,
	// veredito do review) para consulta no TUI.
	Results  map[string]string `json:"results,omitempty"`
	LogFile  string            `json:"log_file,omitempty"`
	ClaudeID string            `json:"claude_session_id,omitempty"` // p/ --resume
	Status   Status            `json:"status"`
	Created  time.Time         `json:"created"`
	Finished *time.Time        `json:"finished,omitempty"`
	Err      string            `json:"err,omitempty"`
}

// Active informa se a sessão ainda ocupa um worktree.
func (s Session) Active() bool {
	return s.Status != StatusCompleted && s.Status != StatusCancelled
}

// SetClaudeID registra a conversa do Claude da fase (e a última, para o
// modo interativo e sessões antigas).
func (s *Session) SetClaudeID(phase, id string) {
	if s.ClaudeIDs == nil {
		s.ClaudeIDs = map[string]string{}
	}
	s.ClaudeIDs[phase] = id
	s.ClaudeID = id
}

// SetResult guarda o resumo final da fase.
func (s *Session) SetResult(phase, result string) {
	if s.Results == nil {
		s.Results = map[string]string{}
	}
	s.Results[phase] = result
}

// IsReview informa se a sessão é só de revisão (revisar o MR de outra
// pessoa), em vez do pipeline de desenvolvimento.
func (s Session) IsReview() bool { return s.Mode == ModeReview }

// Action descreve o que rodar a seguir no pipeline.
type Action struct {
	Phase    string // fase a executar
	ResumeID string // session_id do Claude a retomar; vazio = fase nova
	UseFix   bool   // prompt de correção (dev com o veredito) em vez de resume simples
}

// Plan decide a próxima ação da sessão a partir do estado atual, sem tocar
// na UI nem na rede — é a máquina de estados do pipeline, testável sozinha.
// ok=false quando não há o que rodar: revisão já concluída, ou correção sem
// conversa do dev para retomar.
func (s Session) Plan() (Action, bool) {
	verdict := s.Results[PhaseReview]
	if s.IsReview() {
		// Revisão é fase única: sem plano, dev nem ciclo de correção.
		switch s.Status {
		case StatusPending:
			return Action{Phase: PhaseReview}, true
		case StatusFailed:
			return Action{Phase: PhaseReview, ResumeID: s.ClaudeIDs[PhaseReview]}, true
		}
		return Action{}, false
	}
	switch s.Status {
	case StatusPending:
		return Action{Phase: PhasePlan}, true
	case StatusWaiting:
		next := NextPhase(s.Phase)
		if next == "" {
			next = PhaseReview // não deveria acontecer; revisa de novo
		}
		return Action{Phase: next}, true
	case StatusFailed:
		phase := s.Phase
		if phase == "" {
			phase = PhasePlan
		}
		// Retomar o dev depois de um review já feito leva o prompt de correção.
		return Action{Phase: phase, ResumeID: s.ClaudeIDs[phase], UseFix: phase == PhaseDev && verdict != ""}, true
	case StatusDone:
		// Correção: retoma a conversa do DEV (a que pode editar código)
		// levando o veredito do review.
		devID := s.ClaudeIDs[PhaseDev]
		if devID == "" {
			devID = s.ClaudeID // sessões antigas: conversa única
		}
		if devID == "" {
			return Action{}, false
		}
		return Action{Phase: PhaseDev, ResumeID: devID, UseFix: verdict != ""}, true
	}
	return Action{}, false
}

// IsIssue informa se a sessão veio de uma issue do Jira; sessões antigas
// (sem Kind) caem no formato da chave.
func (s Session) IsIssue() bool {
	if s.Kind != "" {
		return s.Kind == KindIssue
	}
	return jiraKeyPattern.MatchString(s.Key)
}

var jiraKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-\d+$`)

type Store struct {
	js       store.JSON[[]Session]
	Sessions []Session
}

// LogDir é onde ficam os logs stream-json das execuções.
func LogDir() string { return filepath.Join(paths.DataDir(), "logs") }

// Load lê o sessions.json. Mesmo em erro devolve um store utilizável
// (vazio), para o app seguir funcionando sem as sessões antigas.
func Load() (*Store, error) {
	js := store.JSON[[]Session]{Path: paths.DataFile("sessions.json")}
	sessions, err := js.Load()
	return &Store{js: js, Sessions: sessions}, err
}

func (s *Store) Save() error { return s.js.Save(s.Sessions) }

// NewID gera um id único e legível a partir da chave da task.
func NewID(key string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		}
		return '-'
	}, key)
	return slug + "-" + time.Now().Format("20060102-150405")
}

func (s *Store) Add(sess Session) {
	s.Sessions = append(s.Sessions, sess)
}

// Find devolve um ponteiro para a sessão com o id, ou nil.
func (s *Store) Find(id string) *Session {
	for i := range s.Sessions {
		if s.Sessions[i].ID == id {
			return &s.Sessions[i]
		}
	}
	return nil
}

func (s *Store) DeleteAt(i int) {
	if i >= 0 && i < len(s.Sessions) {
		s.Sessions = append(s.Sessions[:i], s.Sessions[i+1:]...)
	}
}

// HasActiveFor informa se já existe sessão ativa para a mesma chave —
// evita dois worktrees disputando a mesma branch.
func (s *Store) HasActiveFor(key string) bool {
	for _, sess := range s.Sessions {
		if sess.Key == key && sess.Active() {
			return true
		}
	}
	return false
}
