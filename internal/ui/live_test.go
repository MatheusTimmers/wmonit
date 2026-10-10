package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/demo"
	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/session"
)

func TestMRStatusAndMetadata(t *testing.T) {
	sessions := []session.Session{{
		ID: "ROT-512-x", Key: "ROT-512", Issue: "ROT-512", Title: "histórico", Service: "backoffice",
		Kind: session.KindIssue, Label: "rot-512", Workspace: "w1", Agent: "rot-512", Status: session.StatusActive,
	}}
	m, h := newWorkspaceModel(t, config.Config{}, sessions)
	mm0, _ := m.Update(metaDoneMsg{})
	m = mm0.(Model)
	cmd := m.fetchMRStatus()
	if cmd == nil {
		t.Fatal("deveria buscar o status do MR da sessão")
	}
	mm, metaCmd := m.Update(cmd())
	m = mm.(Model)
	st := m.live.mrStatus["backoffice!412"]
	if st == nil || st.CI() != "failed" {
		t.Fatalf("status = %+v", m.live.mrStatus)
	}
	if out := m.render(); !strings.Contains(out, "CI falhou (test-integration)") {
		t.Fatalf("a linha deveria mostrar o CI quebrado:\n%s", out)
	}
	if metaCmd == nil {
		t.Fatal("status novo deveria publicar metadata")
	}
	mm, _ = m.Update(metaCmd())
	m = mm.(Model)
	ws, _ := hub.Poll(h).Workspace("w1")
	if ws.Tokens["wm_task"] != "ROT-512" || ws.Tokens["wm_mr"] != "backoffice!412" || ws.Tokens["wm_ci"] != "✗ CI" || !strings.Contains(ws.Tokens["wm_todo"], "CI falhou") {
		t.Fatalf("tokens = %v", ws.Tokens)
	}
	if m.publishMeta() != nil {
		t.Fatal("publicação deveria respeitar o intervalo")
	}
}

func TestShipKey(t *testing.T) {
	m, _ := newWorkspaceModel(t, config.Config{}, demoSessions())
	for i, r := range m.wsRows() {
		if r.kind == rowSession && r.sess.Key == "ROT-512" {
			m.cursor = i
		}
	}
	m, cmd := press(t, m, runes("s"))
	if cmd == nil || !strings.Contains(m.sui.info, "entregando") {
		t.Fatalf("s deveria entregar: %q", m.sui.info)
	}
	msg := cmd()
	sm, ok := msg.(shippedMsg)
	if !ok || sm.err == nil {
		t.Fatalf("sem worktree real a entrega deveria falhar com erro: %#v", msg)
	}
	mm, _ := m.Update(sm)
	if out := mm.(Model).sui.info; !strings.Contains(out, "entrega:") {
		t.Fatalf("info = %q", out)
	}
}

func TestCleanStaleNeedsConfirmation(t *testing.T) {
	sessions := []session.Session{{
		ID: "ROT-498-x", Key: "ROT-498", Issue: "ROT-498", MR: "hades!9460", Service: "hades",
		Kind: session.KindIssue, Label: "rot-498", Status: session.StatusActive,
	}}
	m, _ := newWorkspaceModel(t, config.Config{}, sessions)
	if out := m.render(); !strings.Contains(out, "🧹 1 workspace") {
		t.Fatalf("deveria avisar do MR mergeado:\n%s", out)
	}
	m, cmd := press(t, m, runes("C"))
	if cmd != nil || !m.sui.confirmClean || !strings.Contains(m.sui.info, "rot-498") {
		t.Fatalf("primeiro C só confirma: %q", m.sui.info)
	}
	m, _ = press(t, m, runes("j"))
	if m.sui.confirmClean {
		t.Fatal("outra tecla cancela a confirmação")
	}
	m, _ = press(t, m, runes("C"))
	m, cmd = press(t, m, runes("C"))
	if cmd == nil {
		t.Fatal("segundo C deveria concluir")
	}
}

func TestOpenTarget(t *testing.T) {
	sources := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sources, "hades", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Claude: config.Claude{SourcesDir: sources}, Jira: config.Jira{URL: "https://jira"}}
	m, _ := newWorkspaceModel(t, cfg, nil)

	m = m.WithOpen("https://jira/browse/ROT-512")
	mm, _ := m.resolveOpen()
	m = mm.(Model)
	if m.mode != modeForm || m.sui.form.pending.sess.Key != "ROT-512" || m.open != "" {
		t.Fatalf("issue: mode=%v form=%+v", m.mode, m.sui.form)
	}

	m.mode, m.sui.form = modeNormal, nil
	m = m.WithOpen("hades!9470")
	mm, _ = m.resolveOpen()
	m = mm.(Model)
	if m.mode != modeForm || m.sui.form.pending.sess.MR != "hades!9470" || m.sui.form.pending.sess.Mode != session.ModeReview {
		t.Fatalf("mr: mode=%v form=%+v", m.mode, m.sui.form)
	}

	m.mode, m.sui.form = modeNormal, nil
	m = m.WithOpen("https://git/x/-/merge_requests/1")
	mm, _ = m.resolveOpen()
	if mm.(Model).mode != modeNormal || !strings.Contains(mm.(Model).sui.info, "não está entre") {
		t.Fatalf("mr desconhecido: %q", mm.(Model).sui.info)
	}
}

func TestUsageShownInRow(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	id := claude.NewSessionID()
	proj := filepath.Join(cfgDir, "projects", "-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","timestamp":"2026-10-09T10:00:00Z","message":{"role":"user","content":"oi"}}
{"type":"assistant","timestamp":"2026-10-09T10:00:01Z","message":{"id":"a","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":1000,"output_tokens":500}}}
`
	if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions := []session.Session{{ID: "s1", Key: "ROT-512", Label: "rot-512", Workspace: "w1", Status: session.StatusActive, ClaudeSession: id}}
	cfg := config.Config{Claude: config.Claude{Prices: map[string]claude.Price{"opus": {Input: 5, Output: 25}}}}
	m, _ := newWorkspaceModel(t, cfg, sessions)
	m.live.usageAt, m.live.usageBusy = time.Time{}, false
	cmd := m.refreshUsage()
	if cmd == nil {
		t.Fatal("deveria ler o transcript")
	}
	mm, _ := m.Update(cmd())
	m = mm.(Model)
	if out := m.render(); !strings.Contains(out, "1 pedido(s) · 2k tokens · US$ 0.02") {
		t.Fatalf("linha sem o uso:\n%s", out)
	}
}

var _ = demo.NewHerdr
