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
	}{
		{"revisar MR do fulano", "revisar MR do fulano", "", ""},
		{"pagar boleto @today", "pagar boleto", today, ""},
		{"responder e-mail @tomorrow", "responder e-mail", tomorrow, ""},
		{"entrega @2026-06-15", "entrega", "2026-06-15", ""},
		{"reunião @today 15:00", "reunião", today, "15:00"},
		{"deploy @2026-06-15 09:30", "deploy", "2026-06-15", "09:30"},
		// Texto extra depois da tag não é descartado em silêncio: o
		// sufixo inteiro deixa de ser tag e a tarefa fica como digitada.
		{"hora inválida @today 99:99", "hora inválida @today 99:99", "", ""},
		{"pagar boleto @today urgente demais", "pagar boleto @today urgente demais", "", ""},
		{"falar com time@nelogica", "falar com time@nelogica", "", ""},
		{"tarefa @data-invalida", "tarefa @data-invalida", "", ""},
		{"@today", "@today", "", ""}, // tag sem descrição não vira vencimento
	}
	for _, c := range cases {
		text, due, dueTime := parseDue(c.input)
		if text != c.text || due != c.due || dueTime != c.dueTime {
			t.Errorf("parseDue(%q) = (%q, %q, %q), esperado (%q, %q, %q)",
				c.input, text, due, dueTime, c.text, c.due, c.dueTime)
		}
	}
}
