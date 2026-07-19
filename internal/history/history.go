// Package history guarda um instantâneo diário do que foi entregue, para
// dar uma série temporal real (sequências, tendências) em vez de depender
// só da janela que a API devolve.
package history

import (
	"time"

	"github.com/timmers/wmonit/internal/paths"
	"github.com/timmers/wmonit/internal/store"
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
	js   store.JSON[[]Day]
	Days []Day
}

func Load() (*Store, error) {
	js := store.JSON[[]Day]{Path: paths.DataFile("history.json")}
	days, err := js.Load()
	if err != nil {
		return nil, err
	}
	return &Store{js: js, Days: days}, nil
}

func (s *Store) Save() error { return s.js.Save(s.Days) }

// Upsert grava o resumo do dia, substituindo o registro existente da mesma
// data (os números crescem ao longo do dia, então o último vale). Devolve
// true se algo mudou — registro novo ou números diferentes —, para o
// chamador só gravar em disco quando vale a pena.
func (s *Store) Upsert(d Day) bool {
	for i := range s.Days {
		if s.Days[i].Date == d.Date {
			if s.Days[i] == d {
				return false
			}
			s.Days[i] = d
			return true
		}
	}
	s.Days = append(s.Days, d)
	return true
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
