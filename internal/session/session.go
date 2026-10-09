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
	StatusPending   Status = "pending"   // worktree pronto, agent ainda não lançado
	StatusRunning   Status = "running"   // agent lançado; o estado vivo vem do agent view
	StatusFailed    Status = "failed"    // o lançamento falhou (Err diz por quê)
	StatusCompleted Status = "completed" // concluída: worktree removido
	StatusCancelled Status = "cancelled" // agent parado pelo usuário (s retoma)

	// Estados do antigo pipeline plan → dev → review; sessions.json antigos
	// ainda os trazem. Contam como ativas e 's' lança um agent para elas.
	StatusWaiting Status = "waiting"
	StatusDone    Status = "done"
)

const (
	KindIssue = "issue" // issue do Jira (branch nova)
	KindMR    = "mr"    // merge request existente
	KindFree  = "free"  // agent livre: roda direto no repo, sem worktree próprio
)

const (
	ModeImplement = "implement" // default: o agent implementa
	ModeReview    = "review"    // só revisa o MR e reporta
)

// Session liga uma tarefa (issue/MR) ao worktree e ao agent do Claude Code
// que trabalha nela em background.
type Session struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Key      string `json:"key"` // chave Jira (ABC-123) ou ref do MR (hades!9470)
	URL      string `json:"url,omitempty"`
	Service  string `json:"service"`  // nome do serviço em sources_dir
	Repo     string `json:"repo"`     // caminho do repositório principal
	Worktree string `json:"worktree"` // pasta em que o agent trabalha
	Branch   string `json:"branch"`
	Prompt   string `json:"prompt,omitempty"`
	UserNote string `json:"user_note,omitempty"` // explicação digitada no wmonit
	Kind     string `json:"kind,omitempty"`      // origem: issue, mr ou free
	Mode     string `json:"mode,omitempty"`      // implement (default) ou review
	// AgentID é o id curto do `claude --bg` (attach/stop/rm); ClaudeID, o
	// session id completo da conversa (--resume), conhecido pelo agent view.
	AgentID  string `json:"agent_id,omitempty"`
	ClaudeID string `json:"claude_session_id,omitempty"`
	// AIName/AIMissing: a última leitura do haiku sobre o agent (nome do
	// trabalho e o que falta), feita em AIAt.
	AIName    string     `json:"ai_name,omitempty"`
	AIMissing string     `json:"ai_missing,omitempty"`
	AIAt      *time.Time `json:"ai_at,omitempty"`
	Status    Status     `json:"status"`
	Created   time.Time  `json:"created"`
	Finished  *time.Time `json:"finished,omitempty"`
	Err       string     `json:"err,omitempty"`
}

// Active informa se a sessão ainda ocupa um worktree.
func (s Session) Active() bool {
	return s.Status != StatusCompleted && s.Status != StatusCancelled
}

// OwnsWorktree informa se o worktree é da sessão (criado pelo wmonit) e
// pode ser removido ao concluir; o agent livre trabalha no próprio repo.
func (s Session) OwnsWorktree() bool {
	return s.Kind != KindFree && s.Worktree != "" && s.Worktree != s.Repo
}

// IsReview informa se a sessão é só de revisão (revisar o MR de outra
// pessoa), em vez de desenvolvimento.
func (s Session) IsReview() bool { return s.Mode == ModeReview }

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

// Load lê o sessions.json. Mesmo em erro devolve um store utilizável
// (vazio), para o app seguir funcionando sem as sessões antigas.
func Load() (*Store, error) { return LoadFrom(paths.DataDir()) }

func LoadFrom(dir string) (*Store, error) {
	js := store.JSON[[]Session]{Path: filepath.Join(dir, "sessions.json")}
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

// Get devolve uma cópia da sessão com o id.
func (s *Store) Get(id string) (Session, bool) {
	for i := range s.Sessions {
		if s.Sessions[i].ID == id {
			return s.Sessions[i], true
		}
	}
	return Session{}, false
}

// Update aplica fn à sessão com o id. O ponteiro não escapa da chamada, o que
// evita a armadilha de guardar um ponteiro para dentro do slice (um Add
// posterior pode realocá-lo). Devolve false se não achou.
func (s *Store) Update(id string, fn func(*Session)) bool {
	for i := range s.Sessions {
		if s.Sessions[i].ID == id {
			fn(&s.Sessions[i])
			return true
		}
	}
	return false
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
