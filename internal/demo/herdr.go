package demo

import (
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/timmers/wmonit/internal/herdr"
)

type Herdr struct {
	mu         sync.Mutex
	workspaces []herdr.Workspace
	agents     []herdr.Agent
	panes      []herdr.Pane
	seq        int
}

func NewHerdr() *Herdr {
	return &Herdr{
		seq: 3,
		workspaces: []herdr.Workspace{
			{ID: "w1", Label: "rot-512", AgentStatus: herdr.StatusWorking, TabCount: 3},
			{ID: "w2", Label: "hades-9470", AgentStatus: herdr.StatusBlocked, TabCount: 3},
			{ID: "w3", Label: "bode_os", AgentStatus: herdr.StatusIdle, TabCount: 2},
		},
		agents: []herdr.Agent{
			{Name: "rot-512", Kind: "claude", Status: herdr.StatusWorking, WorkspaceID: "w1", PaneID: "w1:p2"},
			{Name: "hades-9470", Kind: "claude", Status: herdr.StatusBlocked, WorkspaceID: "w2", PaneID: "w2:p2"},
			{Name: "bode-os", Kind: "claude", Status: herdr.StatusIdle, WorkspaceID: "w3", PaneID: "w3:p2"},
		},
		panes: []herdr.Pane{{ID: "w3:p1", WorkspaceID: "w3", Cwd: "/tmp/bode_os"}},
	}
}

func (h *Herdr) State() (herdr.State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws := make([]herdr.Workspace, len(h.workspaces))
	for i, w := range h.workspaces {
		w.Tokens = maps.Clone(w.Tokens)
		ws[i] = w
	}
	return herdr.State{Workspaces: ws, Agents: append([]herdr.Agent{}, h.agents...), Version: "demo"}, nil
}

func (h *Herdr) ReportMetadata(workspaceID, _ string, tokens map[string]string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.workspaces {
		if h.workspaces[i].ID != workspaceID {
			continue
		}
		if h.workspaces[i].Tokens == nil {
			h.workspaces[i].Tokens = map[string]string{}
		}
		for k, v := range tokens {
			if v == "" {
				delete(h.workspaces[i].Tokens, k)
			} else {
				h.workspaces[i].Tokens[k] = v
			}
		}
		return nil
	}
	return &herdr.Error{Code: "workspace_not_found", Message: "workspace " + workspaceID + " not found"}
}

func (h *Herdr) Notify(string, string, string) error { return nil }

func (h *Herdr) Panes(workspaceID string) ([]herdr.Pane, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []herdr.Pane
	for _, p := range h.panes {
		if p.WorkspaceID == workspaceID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (h *Herdr) Tabs(string) ([]herdr.Tab, error) { return nil, nil }

func (h *Herdr) CreateWorkspace(label, cwd string) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	id := fmt.Sprintf("w%d", h.seq)
	ws := herdr.Workspace{ID: id, Label: label, AgentStatus: herdr.StatusUnknown, TabCount: 1}
	h.workspaces = append(h.workspaces, ws)
	pane := herdr.Pane{ID: id + ":p1", WorkspaceID: id, Cwd: cwd}
	h.panes = append(h.panes, pane)
	return ws, herdr.Tab{ID: id + ":t1", WorkspaceID: id}, pane, nil
}

func (h *Herdr) CreateTab(workspaceID, cwd, label string) (herdr.Tab, herdr.Pane, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.panes) + 1
	pane := herdr.Pane{ID: fmt.Sprintf("%s:p%d", workspaceID, n), WorkspaceID: workspaceID, Cwd: cwd}
	h.panes = append(h.panes, pane)
	return herdr.Tab{ID: fmt.Sprintf("%s:t%d", workspaceID, n), WorkspaceID: workspaceID, Label: label}, pane, nil
}

func (h *Herdr) RenameTab(string, string) error   { return nil }
func (h *Herdr) FocusWorkspace(string) error      { return nil }
func (h *Herdr) Run(string, string) error         { return nil }
func (h *Herdr) SendKeys(string, ...string) error { return nil }

func (h *Herdr) CloseWorkspace(workspaceID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ws []herdr.Workspace
	for _, w := range h.workspaces {
		if w.ID != workspaceID {
			ws = append(ws, w)
		}
	}
	var agents []herdr.Agent
	for _, a := range h.agents {
		if a.WorkspaceID != workspaceID {
			agents = append(agents, a)
		}
	}
	h.workspaces, h.agents = ws, agents
	return nil
}

func (h *Herdr) StartAgent(name, kind, paneID string, _ ...string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws, _, _ := strings.Cut(paneID, ":")
	h.agents = append(h.agents, herdr.Agent{Name: name, Kind: kind, Status: herdr.StatusIdle, WorkspaceID: ws, PaneID: paneID})
	return nil
}

func (h *Herdr) Prompt(agent, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.agents {
		if h.agents[i].Name == agent {
			h.agents[i].Status = herdr.StatusWorking
			return nil
		}
	}
	return &herdr.Error{Code: "agent_not_found", Message: "agent " + agent + " não existe"}
}

func (h *Herdr) Read(agent string, _ int) (string, error) {
	switch agent {
	case "hades-9470":
		return "● Read(hades/report.go)\n\n Do you want to make this edit to report.go?\n ❯ 1. Yes\n   2. No, and tell Claude what to do differently", nil
	case "rot-512":
		return "● Plano:\n  1. handler GET /ordens/historico\n  2. consulta paginada\n\n✻ Implementando o handler… (esc to interrupt)", nil
	}
	return "> ", nil
}
