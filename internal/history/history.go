// Package history guarda um instantâneo diário do que foi entregue, para
// dar uma série temporal real (sequências, tendências) em vez de depender
// só da janela que a API devolve.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Day é o resumo de um dia: quantidade de MRs mergeados, issues resolvidas
// e tarefas concluídas.
type Day struct {
	Date   string `json:"date"` // YYYY-MM-DD
	MRs    int    `json:"mrs"`
	Issues int    `json:"issues"`
	Tasks  int    `json:"tasks"`
}

type Store struct {
	path string
	Days []Day
}

func storePath() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "wmonit", "history.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "wmonit", "history.json")
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
	if err := json.Unmarshal(data, &s.Days); err != nil {
		return nil, fmt.Errorf("lendo %s: %w", s.path, err)
	}
	return s, nil
}

func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.Days, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// Upsert grava o resumo do dia, substituindo o registro existente da mesma
// data (os números crescem ao longo do dia, então o último vale).
func (s *Store) Upsert(d Day) {
	for i := range s.Days {
		if s.Days[i].Date == d.Date {
			s.Days[i] = d
			return
		}
	}
	s.Days = append(s.Days, d)
}

func (s *Store) Get(date string) (Day, bool) {
	for _, d := range s.Days {
		if d.Date == date {
			return d, true
		}
	}
	return Day{}, false
}

// Streak conta dias úteis consecutivos com pelo menos uma entrega (MR ou
// issue), terminando no dia ativo mais recente. Fins de semana são pulados
// (não contam nem quebram). Se hoje ainda não teve entrega, a contagem
// parte de ontem — o dia em curso não zera a sequência.
func (s *Store) Streak(today time.Time) int {
	active := map[string]bool{}
	for _, d := range s.Days {
		if d.MRs+d.Issues > 0 {
			active[d.Date] = true
		}
	}
	day := today
	if !active[day.Format("2006-01-02")] {
		day = day.AddDate(0, 0, -1)
	}
	streak := 0
	for {
		switch day.Weekday() {
		case time.Saturday, time.Sunday:
			day = day.AddDate(0, 0, -1)
			continue
		}
		if !active[day.Format("2006-01-02")] {
			break
		}
		streak++
		day = day.AddDate(0, 0, -1)
	}
	return streak
}
