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
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
)

const (
	KindIssue = "issue"
	KindMR    = "mr"
	KindFree  = "free"
)

const (
	ModeImplement = "implement"
	ModeReview    = "review"
)

type Session struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Key           string     `json:"key"`
	URL           string     `json:"url,omitempty"`
	Issue         string     `json:"issue,omitempty"`
	MR            string     `json:"mr,omitempty"`
	Service       string     `json:"service"`
	Repo          string     `json:"repo"`
	Worktree      string     `json:"worktree"`
	Branch        string     `json:"branch"`
	UserNote      string     `json:"user_note,omitempty"`
	Kind          string     `json:"kind,omitempty"`
	Mode          string     `json:"mode,omitempty"`
	Label         string     `json:"label,omitempty"`
	Workspace     string     `json:"workspace,omitempty"`
	Agent         string     `json:"agent,omitempty"`
	PendingPrompt string     `json:"pending_prompt,omitempty"`
	Status        Status     `json:"status"`
	Created       time.Time  `json:"created"`
	Finished      *time.Time `json:"finished,omitempty"`
	Err           string     `json:"err,omitempty"`
}

func (s Session) Active() bool {
	return s.Status != StatusCompleted && s.Status != StatusCancelled
}

func (s Session) OwnsWorktree() bool {
	return s.Kind != KindFree && s.Worktree != "" && s.Worktree != s.Repo
}

func (s Session) IsReview() bool { return s.Mode == ModeReview }

func (s Session) IsIssue() bool {
	if s.Kind != "" {
		return s.Kind == KindIssue
	}
	return jiraKeyPattern.MatchString(s.Key)
}

func (s Session) IssueKey() string {
	if s.Issue != "" {
		return s.Issue
	}
	if s.IsIssue() {
		return s.Key
	}
	return ""
}

func (s Session) MRRef() string {
	if s.MR != "" {
		return s.MR
	}
	if s.Kind == KindMR {
		return s.Key
	}
	return ""
}

func (s Session) Name() string {
	switch {
	case s.Label != "":
		return s.Label
	case s.Key != "":
		return s.Key
	}
	return s.Service
}

var jiraKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*-\d+$`)

func IsJiraKey(s string) bool { return jiraKeyPattern.MatchString(s) }

type Store struct {
	js       store.JSON[[]Session]
	dir      string
	Sessions []Session
}

func (s *Store) Dir() string { return s.dir }

func Load() (*Store, error) { return LoadFrom(paths.DataDir()) }

func LoadFrom(dir string) (*Store, error) {
	js := store.JSON[[]Session]{Path: filepath.Join(dir, "sessions.json")}
	sessions, err := js.Load()
	return &Store{js: js, dir: dir, Sessions: sessions}, err
}

func (s *Store) Save() error { return s.js.Save(s.Sessions) }

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

func (s *Store) Get(id string) (Session, bool) {
	for i := range s.Sessions {
		if s.Sessions[i].ID == id {
			return s.Sessions[i], true
		}
	}
	return Session{}, false
}

func (s *Store) Update(id string, fn func(*Session)) bool {
	for i := range s.Sessions {
		if s.Sessions[i].ID == id {
			fn(&s.Sessions[i])
			return true
		}
	}
	return false
}

func (s *Store) Delete(id string) {
	for i := range s.Sessions {
		if s.Sessions[i].ID == id {
			s.Sessions = append(s.Sessions[:i], s.Sessions[i+1:]...)
			return
		}
	}
}

func (s *Store) HasActiveFor(key string) bool {
	if key == "" {
		return false
	}
	for _, sess := range s.Sessions {
		if sess.Key == key && sess.Active() {
			return true
		}
	}
	return false
}

func (s *Store) ByWorkspace(workspaceID string) (Session, bool) {
	for _, sess := range s.Sessions {
		if sess.Active() && sess.Workspace == workspaceID {
			return sess, true
		}
	}
	return Session{}, false
}

func (s *Store) LabelTaken(label string) bool {
	for _, sess := range s.Sessions {
		if sess.Active() && strings.EqualFold(sess.Label, label) {
			return true
		}
	}
	return false
}
