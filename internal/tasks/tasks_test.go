package tasks

import (
	"testing"
	"time"
)

func TestParseDue(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	cases := []struct {
		input, text, due, dueTime string
		wantErr                   bool
	}{
		{"revisar MR do fulano", "revisar MR do fulano", "", "", false},
		{"pagar boleto @today", "pagar boleto", today, "", false},
		{"responder e-mail @tomorrow", "responder e-mail", tomorrow, "", false},
		{"entrega @2026-06-15", "entrega", "2026-06-15", "", false},
		{"reunião @today 15:00", "reunião", today, "15:00", false},
		{"deploy @2026-06-15 09:30", "deploy", "2026-06-15", "09:30", false},
		// Data válida + hora inválida é engano inequívoco: vira erro.
		{"hora inválida @today 99:99", "hora inválida @today 99:99", "", "", true},
		// Texto extra depois da tag não é descartado em silêncio: o
		// sufixo inteiro deixa de ser tag e a tarefa fica como digitada.
		{"pagar boleto @today urgente demais", "pagar boleto @today urgente demais", "", "", false},
		{"falar com time@nelogica", "falar com time@nelogica", "", "", false},
		{"tarefa @data-invalida", "tarefa @data-invalida", "", "", false},
		{"@today", "@today", "", "", false}, // tag sem descrição não vira vencimento
	}
	for _, c := range cases {
		text, due, dueTime, err := parseDue(c.input)
		if (err != nil) != c.wantErr {
			t.Errorf("parseDue(%q) err = %v, esperado erro=%v", c.input, err, c.wantErr)
		}
		if text != c.text || due != c.due || dueTime != c.dueTime {
			t.Errorf("parseDue(%q) = (%q, %q, %q), esperado (%q, %q, %q)",
				c.input, text, due, dueTime, c.text, c.due, c.dueTime)
		}
	}
}

func TestParsePriority(t *testing.T) {
	cases := []struct{ input, rest, prio string }{
		{"revisar PR do fulano", "revisar PR do fulano", ""},
		{"deploy !critica", "deploy", PriorityCritical},
		{"deploy !crítica", "deploy", PriorityCritical},
		{"corrigir bug !alta", "corrigir bug", PriorityHigh},
		{"!alta corrigir bug", "corrigir bug", PriorityHigh},
		{"tarefa !media", "tarefa", PriorityMedium},
		{"tarefa !baixa", "tarefa", PriorityLow},
		{"deploy !alta @today 15:00", "deploy @today 15:00", PriorityHigh},
		{"importante! agora", "importante! agora", ""}, // "!" no fim da palavra não conta
		{"avisar !time", "avisar !time", ""},           // nível desconhecido fica intacto
	}
	for _, c := range cases {
		rest, prio := parsePriority(c.input)
		if rest != c.rest || prio != c.prio {
			t.Errorf("parsePriority(%q) = (%q, %q), esperado (%q, %q)", c.input, rest, prio, c.rest, c.prio)
		}
	}
}

func TestAddWithPriority(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	today := time.Now().Format("2006-01-02")
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add("deploy !critica @today 15:00"); err != nil {
		t.Fatal(err)
	}
	got := s.Tasks[0]
	if got.Text != "deploy" || got.Priority != PriorityCritical || got.Due != today || got.DueTime != "15:00" {
		t.Fatalf("Add com prioridade = %+v", got)
	}
	if !got.Urgent() {
		t.Errorf("tarefa crítica deveria ser urgente")
	}

	// Hora inválida não grava a tarefa e devolve erro para a UI avisar.
	if err := s.Add("reunião @today 99:99"); err == nil {
		t.Errorf("hora inválida deveria falhar")
	}
	if len(s.Tasks) != 1 {
		t.Errorf("tarefa inválida não deveria ter sido adicionada: %d", len(s.Tasks))
	}
}
