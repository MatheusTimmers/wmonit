package tasks

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/timmers/wmonit/internal/paths"
	"github.com/timmers/wmonit/internal/store"
)

const (
	PriorityLow      = "low"
	PriorityMedium   = "medium"
	PriorityHigh     = "high"
	PriorityCritical = "critical"
)

type Task struct {
	Text     string     `json:"text"`
	Due      string     `json:"due,omitempty"`      // YYYY-MM-DD
	DueTime  string     `json:"due_time,omitempty"` // HH:MM, hora do lembrete (opcional)
	Priority string     `json:"priority,omitempty"` // low/medium/high/critical
	Done     bool       `json:"done"`
	Created  time.Time  `json:"created"`
	DoneAt   *time.Time `json:"done_at,omitempty"` // quando foi concluída
}

func (t Task) Urgent() bool {
	return t.Priority == PriorityHigh || t.Priority == PriorityCritical
}

type Store struct {
	js    store.JSON[[]Task]
	Tasks []Task
}

func Load() (*Store, error) { return LoadFrom(paths.DataDir()) }

func LoadFrom(dir string) (*Store, error) {
	js := store.JSON[[]Task]{Path: filepath.Join(dir, "tasks.json")}
	tasks, err := js.Load()
	if err != nil {
		return nil, err
	}
	return &Store{js: js, Tasks: tasks}, nil
}

func (s *Store) Save() error { return s.js.Save(s.Tasks) }

// Add registra a tarefa. Devolve erro quando o vencimento digitado é
// claramente inválido (ver parseDue), para o chamador avisar em vez de
// gravar uma tarefa sem o horário que o usuário pediu.
func (s *Store) Add(input string) error {
	rest, priority := parsePriority(input)
	text, due, dueTime, err := parseDue(rest)
	if err != nil {
		return err
	}
	s.Tasks = append(s.Tasks, Task{Text: text, Due: due, DueTime: dueTime, Priority: priority, Created: time.Now()})
	return nil
}

var priorityAliases = map[string]string{
	"critica":  PriorityCritical,
	"crítica":  PriorityCritical,
	"critical": PriorityCritical,
	"crit":     PriorityCritical,
	"alta":     PriorityHigh,
	"high":     PriorityHigh,
	"alt":      PriorityHigh,
	"media":    PriorityMedium,
	"média":    PriorityMedium,
	"medium":   PriorityMedium,
	"med":      PriorityMedium,
	"normal":   PriorityMedium,
	"baixa":    PriorityLow,
	"low":      PriorityLow,
}

func parsePriority(input string) (rest, priority string) {
	fields := strings.Fields(input)
	out := fields[:0]
	for _, f := range fields {
		if strings.HasPrefix(f, "!") && len(f) > 1 {
			if p, ok := priorityAliases[strings.ToLower(f[1:])]; ok {
				priority = p
				continue
			}
		}
		out = append(out, f)
	}
	if priority == "" {
		return input, ""
	}
	return strings.Join(out, " "), priority
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

// parseDue extrai o vencimento de uma tag "@data [hora]" no fim do texto.
// O "@" é ambíguo (e-mails como "time@nelogica", menções), então uma tag que
// não vira data volta como texto comum, sem erro — preservar o "@..." já
// sinaliza ao usuário que não foi agendada. O único caso inequívoco de
// engano é data válida com hora inválida ("@today 99:99"): aí devolve erro,
// porque a intenção de agendar com horário é clara.
func parseDue(input string) (text, due, dueTime string, err error) {
	text = strings.TrimSpace(input)
	idx := strings.LastIndex(text, "@")
	if idx < 0 {
		return text, "", "", nil
	}

	fields := strings.Fields(text[idx+1:])
	if len(fields) == 0 || len(fields) > 2 {
		return text, "", "", nil
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
		return text, "", "", nil
	}

	if len(fields) == 2 {
		t, e := time.Parse("15:04", fields[1])
		if e != nil {
			return text, "", "", fmt.Errorf("hora inválida %q — use HH:MM (ex.: @%s 15:00)", fields[1], fields[0])
		}
		dueTime = t.Format("15:04")
	}

	rest := strings.TrimSpace(text[:idx])
	if rest == "" {
		return text, "", "", nil
	}
	return rest, due, dueTime, nil
}
