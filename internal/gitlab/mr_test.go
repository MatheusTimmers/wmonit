package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMRStatusAndCreate(t *testing.T) {
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/v4/projects/3/merge_requests/9":
			io.WriteString(w, `{"state":"opened","draft":true,"has_conflicts":true,"detailed_merge_status":"conflict","blocking_discussions_resolved":false,"head_pipeline":{"id":55,"status":"failed","web_url":"https://g/p/55"}}`)
		case r.URL.Path == "/api/v4/projects/3/merge_requests/9/approvals":
			io.WriteString(w, `{"approvals_left":2}`)
		case r.URL.Path == "/api/v4/projects/3/pipelines/55/jobs":
			if r.URL.Query().Get("scope[]") != "failed" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			io.WriteString(w, `[{"name":"lint","stage":"test"},{"name":"unit","stage":"test"}]`)
		case r.URL.EscapedPath() == "/api/v4/projects/grupo%2Fsvc" && r.Method == "GET":
			io.WriteString(w, `{"default_branch":"develop"}`)
		case r.URL.EscapedPath() == "/api/v4/projects/grupo%2Fsvc/merge_requests" && r.Method == "POST":
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"iid":12,"web_url":"https://g/grupo/svc/-/merge_requests/12","references":{"full":"grupo/svc!12"}}`)
		default:
			t.Errorf("rota inesperada %s %s", r.Method, r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	st, err := c.MRStatus(context.Background(), 3, 9)
	if err != nil {
		t.Fatal(err)
	}
	want := "CI falhou (lint, unit)|conflito com a branch de destino|threads abertas|faltam 2 aprovação(ões)|draft"
	if got := strings.Join(st.Problems(), "|"); got != want {
		t.Fatalf("problemas = %s", got)
	}
	if st.Ready() || st.CI() != "failed" {
		t.Fatalf("status = %+v", st)
	}

	mr, err := c.CreateMR(context.Background(), NewMR{Project: "grupo/svc", SourceBranch: "feature/X-1", Title: "faz X #X-1", Description: "d", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if mr.ShortRef() != "svc!12" || created["target_branch"] != "develop" || created["title"] != "Draft: faz X #X-1" || created["source_branch"] != "feature/X-1" {
		t.Fatalf("mr = %+v, pedido = %v", mr, created)
	}
}

func TestProjectPath(t *testing.T) {
	cases := map[string]string{
		"git@gitlab.exemplo.com:Roteamento/hades.git":      "Roteamento/hades",
		"https://gitlab.exemplo.com/Roteamento/sub/x.git":  "Roteamento/sub/x",
		"ssh://git@gitlab.exemplo.com:2222/grupo/proj.git": "grupo/proj",
		"https://user:tok@gitlab.exemplo.com/grupo/p":      "grupo/p",
		"/tmp/local/repo.git":                              "",
		"":                                                 "",
	}
	for in, want := range cases {
		if got := ProjectPath(in); got != want {
			t.Errorf("ProjectPath(%q) = %q, esperado %q", in, got, want)
		}
	}
}

func TestMergedProblems(t *testing.T) {
	st := &MRStatus{State: "merged", HasConflicts: true}
	if p := st.Problems(); len(p) != 1 || p[0] != "MR mergeado" {
		t.Fatalf("problemas = %v", p)
	}
	var nilStatus *MRStatus
	if nilStatus.Problems() != nil || nilStatus.CI() != "" || nilStatus.Ready() {
		t.Fatal("status nil deveria ser neutro")
	}
}
