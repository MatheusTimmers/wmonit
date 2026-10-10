package jira

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransitionTo(t *testing.T) {
	var posted map[string]map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/rest/api/2/issue/ABC-1/transitions" {
			t.Errorf("rota %s", r.URL.Path)
		}
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&posted)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		io.WriteString(w, `{"transitions":[{"id":"11","name":"Iniciar","to":{"name":"Em Andamento"}},{"id":"21","name":"Enviar p/ revisão","to":{"name":"Revisão 1"}}]}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "bearer", "", "tok", "")

	to, err := c.TransitionTo(context.Background(), "ABC-1", "revisão 1")
	if err != nil || to != "Revisão 1" || posted["transition"]["id"] != "21" {
		t.Fatalf("transição = %q %v %v", to, err, posted)
	}
	if _, err := c.TransitionTo(context.Background(), "ABC-1", "Done"); err == nil || !strings.Contains(err.Error(), "Em Andamento") {
		t.Fatalf("transição inexistente deveria listar as possíveis: %v", err)
	}
}
