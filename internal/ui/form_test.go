package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/timmers/wmonit/internal/config"
	"github.com/timmers/wmonit/internal/demo"
	"github.com/timmers/wmonit/internal/history"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/tasks"
)

func TestCreateFormDrivenByKeys(t *testing.T) {
	sources := t.TempDir()
	for _, name := range []string{"alpha", "svc"} {
		repo := filepath.Join(sources, name)
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		gitRepo(t, repo)
	}
	dir := t.TempDir()
	store, _ := tasks.LoadFrom(dir)
	hist, _ := history.LoadFrom(dir)
	sess, _ := session.LoadFrom(dir)
	cfg := config.Config{Claude: config.Claude{SourcesDir: sources, Models: []string{"opus", "sonnet"}}, Herdr: config.Herdr{Editor: "nvim ."}}
	m := New(cfg, store, hist, sess, demo.GitLabSource{}, demo.JiraSource{}, demo.NewHerdr(), "")

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(140, 50))
	waitFor := func(what string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(what)) },
			teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(20*time.Millisecond))
	}
	key := func(code rune) {
		tm.Send(tea.KeyPressMsg{Code: code})
		time.Sleep(50 * time.Millisecond)
	}
	typeText := func(s string) {
		for _, r := range s {
			tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
	}

	tm.Type("6")
	waitFor("Pendências")
	tm.Type("n")
	waitFor("Repositório")

	typeText("/sv")
	key(tea.KeyEnter)
	key(tea.KeyRight)
	key(tea.KeyEnter)
	typeText("arrumar o login")
	key(tea.KeyEnter)
	key(tea.KeyEnter)
	waitFor("criado")

	_ = tm.Quit()
	final := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	if len(final.sess.Sessions) != 1 {
		t.Fatalf("sessão não criada: %q", final.sui.info)
	}
	s := final.sess.Sessions[0]
	if s.Service != "svc" || s.Model != "opus" || s.Title != "arrumar o login" || s.PendingPrompt == "" {
		t.Fatalf("sessão = %+v (mode %v, info %q)", s, final.mode, final.sui.info)
	}
}
