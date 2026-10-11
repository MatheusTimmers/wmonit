package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/session"
)

func TestHubRequestSurvivesRestart(t *testing.T) {
	m, _ := newWorkspaceModel(t, config.Config{}, nil)
	asked := time.Now().Add(-time.Minute)
	if err := hub.SaveRequest(m.hubDir(), hub.Request{Asked: asked, Pending: true}); err != nil {
		t.Fatal(err)
	}
	again := New(m.cfg, m.store, m.hist, m.sess, m.glSrc, m.jiSrc, m.hr, "")
	if !again.sui.hubWaiting || !again.sui.hubPending || !again.sui.hubAsked.Equal(asked) {
		t.Fatalf("pedido não foi retomado: %+v", again.sui)
	}

	if err := hub.SaveRequest(m.hubDir(), hub.Request{Asked: time.Now().Add(-2 * time.Hour), Pending: true}); err != nil {
		t.Fatal(err)
	}
	old := New(m.cfg, m.store, m.hist, m.sess, m.glSrc, m.jiSrc, m.hr, "")
	if old.sui.hubWaiting {
		t.Fatal("pedido antigo demais não deveria ficar esperando")
	}
}

func TestTrustPrompt(t *testing.T) {
	sessions := []session.Session{{ID: "s1", Key: "ROT-512", Label: "rot-512", Workspace: "w1", Agent: "rot-512", Status: session.StatusActive}}
	m, h := newWorkspaceModel(t, config.Config{}, sessions)
	mm, _ := m.Update(trustProbeMsg{agent: "rot-512", keys: []string{"down", "enter"}})
	m = mm.(Model)
	if out := m.render(); !strings.Contains(out, "confiar na pasta") {
		t.Fatalf("a linha deveria pedir a confirmação:\n%s", out)
	}
	for i, r := range m.wsRows() {
		if r.kind == rowSession {
			m.cursor = i
		}
	}
	m, cmd := press(t, m, runes("t"))
	if cmd == nil {
		t.Fatal("t deveria confirmar a pasta")
	}
	m = deliver(t, m, cmd)
	if _, still := m.sui.trust["rot-512"]; still || !strings.Contains(m.sui.info, "pasta confirmada") {
		t.Fatalf("info = %q", m.sui.info)
	}

	mm, cmd = m.Update(trustProbeMsg{agent: hub.HubAgent, keys: []string{"enter"}})
	if cmd == nil || !strings.Contains(mm.(Model).sui.info, "confirmando a pasta das pendências") {
		t.Fatal("a pasta das pendências é do wmonit: confirma sozinho")
	}
	_ = h
}
