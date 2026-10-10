package herdr

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestRealEventsAndMetadata(t *testing.T) {
	name := os.Getenv("WMONIT_HERDR_IT")
	if name == "" {
		t.Skip("defina WMONIT_HERDR_IT=<sessão de teste do herdr> para rodar contra um herdr real")
	}
	c := New("herdr")
	c.Session = name

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got []Event
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- c.Watch(ctx, WatchedEvents, func() { close(started) }, func(e Event) {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		})
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("watch: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("assinatura não começou")
	}

	ws, _, _, err := c.CreateWorkspace("it-events", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseWorkspace(ws.ID) }()

	if err := c.ReportMetadata(ws.ID, "wmonit", map[string]string{"wm_jira": "Em review", "wm_mr": ""}); err != nil {
		t.Fatal(err)
	}
	st, err := c.State()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range st.Workspaces {
		if w.ID == ws.ID {
			found = true
			if w.Tokens["wm_jira"] != "Em review" {
				t.Errorf("tokens = %v", w.Tokens)
			}
		}
	}
	if !found || st.Version == "" {
		t.Fatalf("snapshot sem o workspace: %+v", st)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := 0
		for _, e := range got {
			if e.WorkspaceID == ws.ID {
				n++
			}
		}
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	var names []string
	for _, e := range got {
		if e.WorkspaceID == ws.ID {
			names = append(names, e.Name)
		}
	}
	if len(names) == 0 {
		t.Fatalf("nenhum evento do workspace %s (todos: %+v)", ws.ID, got)
	}
	t.Logf("eventos: %v", names)

	if err := c.Notify("wmonit", "teste de integração", "none"); err != nil {
		t.Logf("notify: %v", err)
	}
}
