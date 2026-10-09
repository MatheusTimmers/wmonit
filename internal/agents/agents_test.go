package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Saída real do `claude --bg`, com as cores.
const launchedOut = "backgrounded · \x1b[36mef43e5db\x1b[39m · ROT-1 corrige x\n\x1b[2m  claude agents   list sessions\x1b[22m\n"

func TestParseLaunched(t *testing.T) {
	id, err := ParseLaunched(launchedOut)
	if err != nil || id != "ef43e5db" {
		t.Fatalf("ParseLaunched = %q, %v", id, err)
	}
	if _, err := ParseLaunched("Workspace not trusted. Run `claude` in C:\\x once"); err == nil ||
		!strings.Contains(err.Error(), "Workspace not trusted") {
		t.Fatalf("erro deveria trazer a primeira linha do CLI, veio %v", err)
	}
}

func TestParseList(t *testing.T) {
	out := `[{"pid":1,"id":"ef43e5db","cwd":"C:\\Fontes\\.worktrees\\x","kind":"background",
	"sessionId":"ef43e5db-ad83","name":"ROT-1","status":"busy","state":"working"},
	{"pid":2,"cwd":"C:\\Fontes\\wdc","kind":"interactive","sessionId":"48ac","name":"wdc","status":"idle"}]`
	list, err := ParseList(out)
	if err != nil || len(list) != 2 {
		t.Fatalf("ParseList = %v, %v", list, err)
	}
	if !list[0].IsBusy() || !list[0].IsBackground() || list[0].Ref() != "ef43e5db" {
		t.Errorf("background mal lido: %+v", list[0])
	}
	if list[1].IsBusy() || list[1].IsBackground() || list[1].Ref() != "48ac" {
		t.Errorf("interativa mal lida: %+v", list[1])
	}
}

func TestTranscriptPathEncoding(t *testing.T) {
	got := TranscriptPath("H", `C:\Fontes\.worktrees\wmonit-probe`, "abc")
	want := filepath.Join("H", "projects", "C--Fontes--worktrees-wmonit-probe", "abc.jsonl")
	if got != want {
		t.Fatalf("TranscriptPath = %s, esperado %s", got, want)
	}
}

func TestReadTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"user","message":{"content":"faz X"}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hm"}],"usage":{"input_tokens":10,"cache_read_input_tokens":30000,"cache_creation_input_tokens":1000}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Plano: 1) a 2) b"}],"usage":{"input_tokens":12,"cache_read_input_tokens":40000,"cache_creation_input_tokens":500}}}`,
		`{"type":"attachment"}`,
		`não é json`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := ReadTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Last() != "Plano: 1) a 2) b" || len(tr.Recent) != 1 {
		t.Errorf("Recent = %q", tr.Recent)
	}
	if tr.ContextTokens != 40512 {
		t.Errorf("ContextTokens = %d, esperado 40512 (a última resposta)", tr.ContextTokens)
	}
}

func TestReadHeartbeats(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "fleet")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{"id":"a","ticket":"ROT-1","ctxPct":42,"usd":1.5,"files":["x.pas"]}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "lixo.json"), []byte(`{`), 0o644)
	beats := ReadHeartbeats(home)
	if len(beats) != 1 || beats["a"].Ticket != "ROT-1" || *beats["a"].CtxPct != 42 {
		t.Fatalf("ReadHeartbeats = %+v", beats)
	}
}

func TestCollisions(t *testing.T) {
	got := Collisions([]Member{
		{Name: "a", Ticket: "ROT-1", Repo: "hades", Root: "C:/Fontes/hades", Files: []string{"src/x.pas"}},
		{Name: "b", Ticket: "ROT-1", Repo: "hades", Root: "C:/Fontes/.worktrees/ROT-1", Files: []string{"src/x.pas"}},
		{Name: "c", Repo: "wdc", Root: `C:\Fontes\wdc`},
		{Name: "d", Repo: "wdc", Root: "c:/fontes/wdc"},
	})
	text := strings.Join(got, "\n")
	for _, want := range []string{"ROT-1 aberta em 2 worktrees: a, b", "2 agents no mesmo worktree", "c, d", "hades:src/x.pas editado por a, b"} {
		if !strings.Contains(text, want) {
			t.Errorf("Collisions sem %q:\n%s", want, text)
		}
	}
	if len(got) != 3 {
		t.Errorf("esperado 3 avisos, veio %d:\n%s", len(got), text)
	}
}
