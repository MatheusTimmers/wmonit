package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProgress(t *testing.T) {
	log := `{"type":"system","subtype":"init","session_id":"abc-123"}
{"type":"assistant","message":{"content":[{"type":"text","text":"vou começar"}]}}
ruído de stderr no meio do log
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"},{"type":"tool_use","name":"Edit"}]}}
{"type":"result","subtype":"success","session_id":"abc-123","is_error":false,"result":"tudo pronto"}
`
	path := filepath.Join(t.TempDir(), "x.jsonl")
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := ReadProgress(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.SessionID != "abc-123" {
		t.Errorf("SessionID = %q", p.SessionID)
	}
	if p.Turns != 2 {
		t.Errorf("Turns = %d, esperado 2", p.Turns)
	}
	if p.LastText != "vou começar" {
		t.Errorf("LastText = %q", p.LastText)
	}
	if len(p.Tools) != 2 || p.Tools[1] != "Edit" {
		t.Errorf("Tools = %v", p.Tools)
	}
	if !p.Done || p.IsError || p.Result != "tudo pronto" {
		t.Errorf("resultado: %+v", p)
	}
}

func TestReadProgressError(t *testing.T) {
	log := `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"deu ruim"}`
	path := filepath.Join(t.TempDir(), "x.jsonl")
	os.WriteFile(path, []byte(log), 0o644)
	p, err := ReadProgress(path)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Done || !p.IsError {
		t.Errorf("esperado done+erro: %+v", p)
	}
}
