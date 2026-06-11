package history

import (
	"testing"
	"time"
)

func TestUpsert(t *testing.T) {
	s := &Store{}
	if !s.Upsert(Day{Date: "2026-06-10", MRs: 1}) {
		t.Error("registro novo deveria reportar mudança")
	}
	if !s.Upsert(Day{Date: "2026-06-10", MRs: 3, Issues: 2}) { // mesma data: substitui
		t.Error("números diferentes deveriam reportar mudança")
	}
	if s.Upsert(Day{Date: "2026-06-10", MRs: 3, Issues: 2}) {
		t.Error("valor idêntico não deveria reportar mudança")
	}
	s.Upsert(Day{Date: "2026-06-11", MRs: 1})
	if len(s.Days) != 2 {
		t.Fatalf("Days = %d, esperado 2", len(s.Days))
	}
	if d, _ := s.Get("2026-06-10"); d.MRs != 3 || d.Issues != 2 {
		t.Errorf("upsert não substituiu: %+v", d)
	}
}

func TestStreak(t *testing.T) {
	// Quarta 2026-06-10. Segunda a quarta com entrega → sequência 3.
	s := &Store{Days: []Day{
		{Date: "2026-06-08", MRs: 1}, // segunda
		{Date: "2026-06-09", Issues: 1},
		{Date: "2026-06-10", MRs: 2},
	}}
	today := time.Date(2026, 6, 10, 12, 0, 0, 0, time.Local)
	if got := s.Streak(today); got != 3 {
		t.Errorf("Streak = %d, esperado 3", got)
	}

	// Hoje sem entrega ainda: parte de ontem, sequência continua.
	today2 := time.Date(2026, 6, 11, 9, 0, 0, 0, time.Local)
	if got := s.Streak(today2); got != 3 {
		t.Errorf("Streak (dia em curso) = %d, esperado 3", got)
	}

	// Fim de semana é pulado: sexta com entrega, conta na segunda seguinte.
	s2 := &Store{Days: []Day{{Date: "2026-06-05", MRs: 1}}} // sexta
	monday := time.Date(2026, 6, 8, 10, 0, 0, 0, time.Local)
	if got := s2.Streak(monday); got != 1 {
		t.Errorf("Streak através do fim de semana = %d, esperado 1", got)
	}

	// Buraco quebra a sequência.
	s3 := &Store{Days: []Day{
		{Date: "2026-06-08", MRs: 1},
		{Date: "2026-06-10", MRs: 1}, // pulou 09 (terça, dia útil)
	}}
	if got := s3.Streak(time.Date(2026, 6, 10, 12, 0, 0, 0, time.Local)); got != 1 {
		t.Errorf("Streak com buraco = %d, esperado 1", got)
	}
}
