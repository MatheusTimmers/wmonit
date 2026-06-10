package tasks

import (
	"testing"
	"time"
)

func TestParseDue(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	cases := []struct {
		input, text, due string
	}{
		{"revisar MR do fulano", "revisar MR do fulano", ""},
		{"pagar boleto @hoje", "pagar boleto", today},
		{"responder e-mail @amanha", "responder e-mail", tomorrow},
		{"responder e-mail @amanhã", "responder e-mail", tomorrow},
		{"entrega @2026-06-15", "entrega", "2026-06-15"},
		{"falar com time@nelogica", "falar com time@nelogica", ""},
		{"tarefa @data-invalida", "tarefa @data-invalida", ""},
	}
	for _, c := range cases {
		text, due := parseDue(c.input)
		if text != c.text || due != c.due {
			t.Errorf("parseDue(%q) = (%q, %q), esperado (%q, %q)", c.input, text, due, c.text, c.due)
		}
	}
}
