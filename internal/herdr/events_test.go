//go:build !windows

package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fakeServer(t *testing.T, lines ...string) (string, chan map[string]any) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	reqs := make(chan map[string]any, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		line, _ := r.ReadBytes('\n')
		var req map[string]any
		_ = json.Unmarshal(line, &req)
		reqs <- req
		for _, l := range lines {
			conn.Write([]byte(l + "\n"))
		}
		time.Sleep(50 * time.Millisecond)
	}()
	return sock, reqs
}

func TestWatch(t *testing.T) {
	sock, reqs := fakeServer(t,
		`{"id":"wmonit:events","result":{"type":"subscription_started"}}`,
		`{"event":"workspace_created","data":{"type":"workspace_created","workspace":{"workspace_id":"w3","label":"x"}}}`,
		`lixo`,
		`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w4:p1","workspace_id":"w4","agent_status":"working"}}}`,
		`{"event":"pane_agent_detected","data":{"agent":"claude","pane_id":"w5:p1","workspace_id":"w5"}}`,
		`{"id":"wmonit:events","error":{"code":"events_lost","message":"fell behind"}}`,
	)
	t.Setenv("HERDR_SOCKET_PATH", sock)
	c := New("herdr")
	started := false
	var got []Event
	err := c.Watch(context.Background(), []string{"workspace.created", "pane.updated"}, func() { started = true }, func(e Event) { got = append(got, e) })
	if !errors.Is(err, ErrEventsLost) {
		t.Fatalf("erro = %v", err)
	}
	req := <-reqs
	params := req["params"].(map[string]any)
	if req["method"] != "events.subscribe" || len(params["subscriptions"].([]any)) != 2 {
		t.Fatalf("pedido = %v", req)
	}
	if !started || len(got) != 3 || got[0].WorkspaceID != "w3" || got[1].WorkspaceID != "w4" || got[2].WorkspaceID != "w5" || got[1].Name != "pane_updated" {
		t.Fatalf("eventos = %+v", got)
	}
}

func TestWatchStopsWithContext(t *testing.T) {
	sock, _ := fakeServer(t, `{"id":"wmonit:events","result":{"type":"subscription_started"}}`)
	t.Setenv("HERDR_SOCKET_PATH", sock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- New("herdr").Watch(ctx, WatchedEvents, cancel, func(Event) {}) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("erro = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Watch não parou com o contexto")
	}
}

func TestSocketPath(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("HERDR_CONFIG_PATH", "/cfg/herdr/config.toml")
	c := New("herdr")
	if c.SocketPath() != "/cfg/herdr/herdr.sock" {
		t.Fatalf("padrão = %s", c.SocketPath())
	}
	c.Session = "teste"
	if c.SocketPath() != "/cfg/herdr/sessions/teste/herdr.sock" {
		t.Fatalf("sessão = %s", c.SocketPath())
	}
	c.Session = ""
	t.Setenv("HERDR_SOCKET_PATH", "/x.sock")
	if c.SocketPath() != "/x.sock" {
		t.Fatalf("env = %s", c.SocketPath())
	}
}
