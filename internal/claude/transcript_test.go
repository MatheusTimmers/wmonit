package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const sampleTranscript = `{"type":"permission-mode","permissionMode":"default","sessionId":"s"}
{"type":"user","timestamp":"2026-10-09T10:00:00Z","message":{"role":"user","content":"faça X"}}
{"type":"assistant","timestamp":"2026-10-09T10:00:05Z","message":{"id":"m1","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"vou ler"}],"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":50}}}
{"type":"assistant","timestamp":"2026-10-09T10:00:06Z","message":{"id":"m1","role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Read"}],"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":50}}}
{"type":"user","timestamp":"2026-10-09T10:00:07Z","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"assistant","timestamp":"2026-10-09T10:00:09Z","isSidechain":true,"message":{"id":"m9","model":"claude-haiku","content":[],"usage":{"input_tokens":999,"output_tokens":999}}}
{"type":"assistant","timestamp":"2026-10-09T10:01:00Z","message":{"id":"m2","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"pronto, fiz X"}],"usage":{"input_tokens":1,"output_tokens":20,"cache_read_input_tokens":200,"cache_creation_input_tokens":0}}}
{"type":"ai-title","aiTitle":"Fazer X"}
{"type":"last-prompt","lastPrompt":"faça X"}
não é json
`

func TestReadUsage(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	id := NewSessionID()
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("uuid inválido: %s", id)
	}
	if TranscriptPath(id) != "" {
		t.Fatal("sem arquivo não deveria achar")
	}
	dir := filepath.Join(cfg, "projects", "-src-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(sampleTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	if TranscriptPath(id) != path || TranscriptPath("../x") != "" || TranscriptPath("*") != "" {
		t.Fatal("TranscriptPath")
	}
	u, err := ReadUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if u.Turns != 1 || u.Input != 11 || u.Output != 25 || u.CacheRead != 300 || u.CacheWrite != 50 || u.Tokens() != 386 {
		t.Fatalf("uso = %+v", u)
	}
	if u.Model != "claude-opus-5-5" || u.Title != "Fazer X" || u.LastTool != "Read" || u.LastText != "pronto, fiz X" || u.LastPrompt != "faça X" {
		t.Fatalf("uso = %+v", u)
	}
	if u.Started.Format("15:04:05") != "10:00:00" || u.LastAt.Format("15:04:05") != "10:01:00" {
		t.Fatalf("tempos = %v %v", u.Started, u.LastAt)
	}
	cost, ok := u.Cost(map[string]Price{"opus": {Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5}, "sonnet": {Input: 1}})
	if !ok || strings.TrimSpace(fmtCost(cost)) != "0.002285" {
		t.Fatalf("custo = %v %v", cost, ok)
	}
	if _, ok := u.Cost(map[string]Price{"haiku": {Input: 1}}); ok {
		t.Fatal("modelo sem preço não deveria ter custo")
	}
	if FormatTokens(386) != "386" || FormatTokens(12_500) != "12k" || FormatTokens(2_300_000) != "2.3M" {
		t.Fatal("FormatTokens")
	}
}

func fmtCost(c float64) string { return strings.TrimRight(strings.TrimRight(sprintf6(c), "0"), ".") }

func sprintf6(f float64) string { return fmt.Sprintf("%.6f", f) }
