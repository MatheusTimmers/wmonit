package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Task struct {
	Text    string     `json:"text"`
	Due     string     `json:"due,omitempty"` // YYYY-MM-DD
	Done    bool       `json:"done"`
	Created time.Time  `json:"created"`
	DoneAt  *time.Time `json:"done_at,omitempty"` // quando foi concluída
}

type Store struct {
	path  string
	Tasks []Task
}

func storePath() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "wmonit", "tasks.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "wmonit", "tasks.json")
}

func Load() (*Store, error) {
	s := &Store{path: storePath()}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.Tasks); err != nil {
		return nil, fmt.Errorf("lendo %s: %w", s.path, err)
	}
	return s, nil
}

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

// Add cria uma tarefa a partir do texto digitado. Um sufixo "@hoje",
// "@amanha" ou "@YYYY-MM-DD" vira a data de vencimento.
func (s *Store) Add(input string) {
	text, due := parseDue(input)
	s.Tasks = append(s.Tasks, Task{Text: text, Due: due, Created: time.Now()})
}

func (s *Store) ToggleAt(i int) {
	if i >= 0 && i < len(s.Tasks) {
		s.Tasks[i].Done = !s.Tasks[i].Done
		if s.Tasks[i].Done {
			now := time.Now()
			s.Tasks[i].DoneAt = &now
		} else {
			s.Tasks[i].DoneAt = nil
		}
	}
}

func (s *Store) DeleteAt(i int) {
	if i >= 0 && i < len(s.Tasks) {
		s.Tasks = append(s.Tasks[:i], s.Tasks[i+1:]...)
	}
}

func parseDue(input string) (text, due string) {
	text = strings.TrimSpace(input)
	idx := strings.LastIndex(text, "@")
	if idx < 0 {
		return text, ""
	}
	tag := strings.TrimSpace(text[idx+1:])
	today := time.Now()
	switch strings.ToLower(tag) {
	case "hoje":
		due = today.Format("2006-01-02")
	case "amanha", "amanhã":
		due = today.AddDate(0, 0, 1).Format("2006-01-02")
	default:
		if t, err := time.Parse("2006-01-02", tag); err == nil {
			due = t.Format("2006-01-02")
		}
	}
	if due != "" {
		text = strings.TrimSpace(text[:idx])
	}
	return text, due
}
