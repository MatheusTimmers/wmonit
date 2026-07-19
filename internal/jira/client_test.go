package jira

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestComplexityValue(t *testing.T) {
	cases := []struct {
		name, fields, field, want string
	}{
		{"texto", `{"cf":"Alta"}`, "cf", "Alta"},
		{"inteiro", `{"cf":5}`, "cf", "5"},
		{"decimal", `{"cf":2.5}`, "cf", "2.5"},
		{"select", `{"cf":{"value":"Média"}}`, "cf", "Média"},
		{"null", `{"cf":null}`, "cf", ""},
		{"ausente", `{"outro":"x"}`, "cf", ""},
		{"select sem value", `{"cf":{"id":"10"}}`, "cf", ""},
		{"fields não-objeto", `[1,2,3]`, "cf", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := complexityValue(json.RawMessage(c.fields), c.field); got != c.want {
				t.Errorf("complexityValue(%s) = %q, esperado %q", c.fields, got, c.want)
			}
		})
	}
}

func TestDateOnly(t *testing.T) {
	orig := time.Local
	time.Local = time.FixedZone("BRT", -3*3600)
	defer func() { time.Local = orig }()

	cases := []struct{ in, want string }{
		// 02:30 UTC de 12/06 é 23:30 de 11/06 em -0300: truncar a string daria
		// o dia errado (12); dateOnly converte para o fuso local e acerta o 11.
		{"2026-06-12T02:30:00.000+0000", "2026-06-11"},
		{"2026-06-11T23:45:00.000-0300", "2026-06-11"},
		{"2026-06-11", "2026-06-11"}, // data pura passa direto
		{"", ""},
	}
	for _, c := range cases {
		if got := dateOnly(c.in); got != c.want {
			t.Errorf("dateOnly(%q) = %q, esperado %q", c.in, got, c.want)
		}
	}
}

func TestTextOf(t *testing.T) {
	if got := textOf(json.RawMessage(`"olá mundo"`)); got != "olá mundo" {
		t.Errorf("texto puro: %q", got)
	}
	if got := textOf(json.RawMessage(`null`)); got != "" {
		t.Errorf("null deveria ser vazio: %q", got)
	}
	if got := textOf(json.RawMessage(``)); got != "" {
		t.Errorf("vazio deveria ser vazio: %q", got)
	}
	if got := textOf(json.RawMessage(`{"type":"doc","content":[]}`)); !strings.Contains(got, "rich text") {
		t.Errorf("ADF deveria virar aviso: %q", got)
	}
}

// issueJSON monta o JSON de uma issue como o Jira devolve no search.
func issueJSON(key, prioID string) string {
	return `{"key":"` + key + `","fields":{` +
		`"summary":"` + key + ` resumo",` +
		`"status":{"name":"Em progresso","statusCategory":{"key":"indeterminate"}},` +
		`"issuetype":{"name":"Tarefa"},` +
		`"duedate":"2026-06-20",` +
		`"created":"2026-06-01T10:00:00.000+0000",` +
		`"resolutiondate":"",` +
		`"priority":{"id":"` + prioID + `","name":"High"}}}`
}

// TestDoSearchPaginationV2 cobre a paginação por startAt/total do endpoint v2.
func TestDoSearchPaginationV2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/search" {
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
		// Página 1 (sem startAt): 2 de 3. Página 2 (startAt=2): a última.
		if r.URL.Query().Get("startAt") == "" {
			w.Write([]byte(`{"total":3,"issues":[` + issueJSON("ABC-1", "1") + `,` + issueJSON("ABC-2", "2") + `]}`))
			return
		}
		w.Write([]byte(`{"total":3,"issues":[` + issueJSON("ABC-3", "3") + `]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "bearer", "", "tok", "")
	issues, code, err := c.doSearch("/rest/api/2/search", "jql", 100, "", map[string]int{"1": 0, "2": 1})
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	if len(issues) != 3 {
		t.Fatalf("len = %d, esperado 3 (paginou)", len(issues))
	}
	got := issues[0]
	if got.Key != "ABC-1" || got.Summary != "ABC-1 resumo" || got.Status != "Em progresso" ||
		got.Category != "indeterminate" || got.Type != "Tarefa" || got.Due != "2026-06-20" {
		t.Errorf("parsing da issue: %+v", got)
	}
	if got.Created != "2026-06-01" { // dateOnly aplicado
		t.Errorf("created = %q", got.Created)
	}
	if got.PrioRank != 0 { // id "1" mapeia para a posição 0 do mapa
		t.Errorf("PrioRank = %d, esperado 0", got.PrioRank)
	}
	if issues[2].PrioRank != 3 { // id "3" não está no mapa → cai no Atoi
		t.Errorf("PrioRank fallback = %d, esperado 3", issues[2].PrioRank)
	}
}

// TestSearchFallbackV3 cobre o 410 do Jira Cloud no v2 e a paginação por
// nextPageToken no v3.
func TestSearchFallbackV3(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/search":
			w.WriteHeader(http.StatusGone) // Cloud removeu o v2
		case "/rest/api/3/search/jql":
			if r.URL.Query().Get("nextPageToken") == "" {
				w.Write([]byte(`{"issues":[` + issueJSON("CLD-1", "1") + `],"nextPageToken":"tok2"}`))
				return
			}
			w.Write([]byte(`{"issues":[` + issueJSON("CLD-2", "1") + `]}`)) // sem token → fim
		default:
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "bearer", "", "tok", "")
	issues, err := c.search("jql", 100, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issues[0].Key != "CLD-1" || issues[1].Key != "CLD-2" {
		t.Fatalf("fallback v3 falhou: %+v", issues)
	}
}

// TestMetadataMemoized garante que campos e prioridades são buscados uma vez
// só por Client (a otimização do refresh).
func TestMetadataMemoized(t *testing.T) {
	var fieldHits, prioHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/field":
			fieldHits++
			w.Write([]byte(`[{"id":"customfield_1","name":"Complexidade"}]`))
		case "/rest/api/2/priority":
			prioHits++
			w.Write([]byte(`[{"id":"1"},{"id":"2"}]`))
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "bearer", "", "tok", "")
	for i := 0; i < 3; i++ {
		if got := c.resolveCXField(); got != "customfield_1" {
			t.Fatalf("cxField = %q", got)
		}
		if got := c.resolvePriorities(); got["2"] != 1 {
			t.Fatalf("prios = %v", got)
		}
	}
	if fieldHits != 1 {
		t.Errorf("field buscado %d vezes, esperado 1", fieldHits)
	}
	if prioHits != 1 {
		t.Errorf("priority buscado %d vezes, esperado 1", prioHits)
	}
}
