package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

var WatchedEvents = []string{
	"workspace.created", "workspace.closed", "workspace.renamed", "workspace.updated",
	"tab.created", "tab.closed", "tab.renamed",
	"pane.created", "pane.closed", "pane.exited", "pane.updated", "pane.agent_detected",
}

type Event struct {
	Name        string
	WorkspaceID string
	Raw         json.RawMessage
}

var ErrEventsLost = errors.New("herdr: eventos perdidos, ressincronize")

func configDir() string {
	if p := os.Getenv("HERDR_CONFIG_PATH"); p != "" {
		return filepath.Dir(p)
	}
	if runtime.GOOS != "windows" {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			return filepath.Join(x, "herdr")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr")
}

func (c *Client) SocketPath() string {
	if c.Session != "" {
		return filepath.Join(configDir(), "sessions", c.Session, "herdr.sock")
	}
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	if s := os.Getenv("HERDR_SESSION"); s != "" {
		return filepath.Join(configDir(), "sessions", s, "herdr.sock")
	}
	return filepath.Join(configDir(), "herdr.sock")
}

func (c *Client) Watch(ctx context.Context, events []string, onStart func(), onEvent func(Event)) error {
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := dialSocket(dctx, c.SocketPath())
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	subs := make([]map[string]string, len(events))
	for i, e := range events {
		subs[i] = map[string]string{"type": e}
	}
	req, _ := json.Marshal(map[string]any{
		"id":     "wmonit:events",
		"method": "events.subscribe",
		"params": map[string]any{"subscriptions": subs},
	})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	started := false
	for sc.Scan() {
		var line struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *Error          `json:"error"`
			Event  string          `json:"event"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		switch {
		case line.Error != nil:
			if line.Error.Code == "events_lost" {
				return ErrEventsLost
			}
			return line.Error
		case !started && line.ID != "":
			started = true
			if onStart != nil {
				onStart()
			}
		case line.Event != "":
			onEvent(Event{Name: line.Event, WorkspaceID: eventWorkspace(line.Data), Raw: line.Data})
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return fmt.Errorf("herdr: conexão de eventos encerrada")
}

func eventWorkspace(data json.RawMessage) string {
	var d struct {
		WorkspaceID string `json:"workspace_id"`
		Workspace   *struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
		Pane *struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"pane"`
		Tab *struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"tab"`
	}
	if json.Unmarshal(data, &d) != nil {
		return ""
	}
	switch {
	case d.WorkspaceID != "":
		return d.WorkspaceID
	case d.Workspace != nil:
		return d.Workspace.ID
	case d.Pane != nil:
		return d.Pane.WorkspaceID
	case d.Tab != nil:
		return d.Tab.WorkspaceID
	}
	return ""
}
