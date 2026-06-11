// Package session persiste as sessões de trabalho do Claude Code: cada
// sessão referencia uma task (issue Jira ou MR), um worktree isolado e o
// estado da execução. Mesmo padrão de armazenamento de tasks/history.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"   // criada, ainda não executada
	StatusRunning   Status = "running"   // claude em execução
	StatusDone      Status = "done"      // claude terminou, aguardando fechamento
	StatusFailed    Status = "failed"    // claude saiu com erro
	StatusCompleted Status = "completed" // fechada: worktree removido
	StatusCancelled Status = "cancelled" // cancelada pelo usuário
)

// Session é uma sessão de trabalho: task + worktree + execução do Claude.
type Session struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Key      string     `json:"key"` // chave Jira (ABC-123) ou ref do MR (hades!9470)
	URL      string     `json:"url,omitempty"`
	Service  string     `json:"service"`  // nome do serviço em sources_dir
	Repo     string     `json:"repo"`     // caminho do repositório principal
	Worktree string     `json:"worktree"` // caminho do worktree
	Branch   string     `json:"branch"`
	Prompt   string     `json:"prompt,omitempty"`
	LogFile  string     `json:"log_file,omitempty"`
	ClaudeID string     `json:"claude_session_id,omitempty"` // p/ --resume
	Status   Status     `json:"status"`
	Created  time.Time  `json:"created"`
	Finished *time.Time `json:"finished,omitempty"`
	Err      string     `json:"err,omitempty"`
}

// Active informa se a sessão ainda ocupa um worktree.
func (s Session) Active() bool {
	return s.Status != StatusCompleted && s.Status != StatusCancelled
}

type Store struct {
	path     string
	Sessions []Session
}

func dataDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "wmonit")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "wmonit")
}

func storePath() string { return filepath.Join(dataDir(), "sessions.json") }

// LogDir é onde ficam os logs stream-json das execuções.
func LogDir() string { return filepath.Join(dataDir(), "logs") }

// Load lê o sessions.json. Mesmo em erro devolve um store utilizável
// (vazio), para o app seguir funcionando sem as sessões antigas.
func Load() (*Store, error) {
	s := &Store{path: storePath()}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s.Sessions); err != nil {
		return s, fmt.Errorf("lendo %s: %w", s.path, err)
	}
	return s, nil
}

func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.Sessions, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

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
