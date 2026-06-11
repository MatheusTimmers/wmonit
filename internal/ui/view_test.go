package ui

import (
	"reflect"
	"testing"
	"time"

	"github.com/timmers/wmonit/internal/jira"
)

func TestStartOfWeek(t *testing.T) {
	want := time.Date(2026, 6, 8, 0, 0, 0, 0, time.Local) // segunda
	for _, d := range []time.Time{
		time.Date(2026, 6, 8, 0, 0, 1, 0, time.Local),   // segunda
		time.Date(2026, 6, 10, 15, 0, 0, 0, time.Local), // quarta
		time.Date(2026, 6, 14, 23, 0, 0, 0, time.Local), // domingo
	} {
		if got := startOfWeek(d); !got.Equal(want) {
			t.Errorf("startOfWeek(%v) = %v, esperado %v", d, got, want)
		}
	}
}

func TestResolvedIn(t *testing.T) {
	issues := []jira.Issue{
		{Key: "A-1", Resolved: "2026-05-31"},
		{Key: "A-2", Resolved: "2026-06-01"},
		{Key: "A-3", Resolved: "2026-06-10"},
		{Key: "A-4", Resolved: "2026-06-11"},
	}
	got := resolvedIn(issues, "2026-06-01", "2026-06-10")
	if len(got) != 2 || got[0].Key != "A-2" || got[1].Key != "A-3" {
		t.Errorf("resolvedIn = %v, esperado A-2 e A-3", got)
	}
}

func TestAvgLeadDays(t *testing.T) {
	issues := []jira.Issue{
		{Created: "2026-06-01", Resolved: "2026-06-05"}, // 4 dias
		{Created: "2026-06-02", Resolved: "2026-06-08"}, // 6 dias
		{Created: "", Resolved: "2026-06-08"},           // ignorada
	}
	if got := avgLeadDays(issues); got != 5 {
		t.Errorf("avgLeadDays = %v, esperado 5", got)
	}
}

func TestSidelineKind(t *testing.T) {
	cases := map[string]string{
		"Em Deploy":            "em deploy",
		"Bloqueado":            "bloqueada",
		"Em Andamento":         "",
		"Aplicando Correção":   "",
		"Análise em Progresso": "",
	}
	for status, want := range cases {
		if got := sidelineKind(status); got != want {
			t.Errorf("sidelineKind(%q) = %q, esperado %q", status, got, want)
		}
	}
}

func TestGroupByStatus(t *testing.T) {
	issues := []jira.Issue{
		{Key: "A-1", Status: "Em Deploy"},
		{Key: "A-2", Status: "Em Andamento"},
		{Key: "A-3", Status: "Status Novo Qualquer"},
		{Key: "A-4", Status: "Em Andamento"},
		{Key: "A-5", Status: "revisão 1"}, // ordem ignora maiúsculas
	}
	order := []string{"Análise em Progresso", "Em Andamento", "Revisão 1", "Em Deploy"}

	names, groups := groupByStatus(issues, order)

	want := []string{"Em Andamento", "revisão 1", "Em Deploy", "Status Novo Qualquer"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("ordem dos grupos = %v, esperado %v", names, want)
	}
	if len(groups["Em Andamento"]) != 2 {
		t.Errorf("Em Andamento com %d issues, esperado 2", len(groups["Em Andamento"]))
	}
}
