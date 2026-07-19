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
	Due     string     `json:"due,omitempty"`      // YYYY-MM-DD
	DueTime string     `json:"due_time,omitempty"` // HH:MM, hora do lembrete (opcional)
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

func (s *Store) Add(input string) {
	text, due, dueTime := parseDue(input)
	s.Tasks = append(s.Tasks, Task{Text: text, Due: due, DueTime: dueTime, Created: time.Now()})
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

func parseDue(input string) (text, due, dueTime string) {
	text = strings.TrimSpace(input)
	idx := strings.LastIndex(text, "@")
	if idx < 0 {
		return text, "", ""
	}

	fields := strings.Fields(text[idx+1:])
	if len(fields) == 0 || len(fields) > 2 {
		return text, "", ""
		// TODO: Retornar error
	}

	today := time.Now()
	switch strings.ToLower(fields[0]) {
	case "today":
		due = today.Format("2006-01-02")
	case "tomorrow":
		due = today.AddDate(0, 0, 1).Format("2006-01-02")
	default:
		if t, err := time.Parse("2006-01-02", fields[0]); err == nil {
			due = t.Format("2006-01-02")
		}
	}
	if due == "" {
		return text, "", "" // não era uma tag de data válida
		// TODO: Return error
	}

	if len(fields) == 2 {
		t, err := time.Parse("15:04", fields[1])
		if err != nil {
			return text, "", "" // "@today qualquer-coisa" não é uma tag
		}
		dueTime = t.Format("15:04")
	}

	rest := strings.TrimSpace(text[:idx])
	if rest == "" {
		return text, "", "" // só a tag, sem descrição — não vira vencimento
	}
	return rest, due, dueTime
}
