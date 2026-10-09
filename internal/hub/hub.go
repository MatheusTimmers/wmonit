package hub

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

type Herdr interface {
	Workspaces() ([]herdr.Workspace, error)
	Agents() ([]herdr.Agent, error)
	Panes(workspaceID string) ([]herdr.Pane, error)
	Tabs(workspaceID string) ([]herdr.Tab, error)
	CreateWorkspace(label, cwd string) (herdr.Workspace, herdr.Tab, herdr.Pane, error)
	CreateTab(workspaceID, cwd, label string) (herdr.Tab, herdr.Pane, error)
	RenameTab(tabID, label string) error
	FocusWorkspace(workspaceID string) error
	CloseWorkspace(workspaceID string) error
	Run(paneID, command string) error
	StartAgent(name, kind, paneID string, args ...string) error
	Prompt(agent, text string) error
	SendKeys(agent string, keys ...string) error
	Read(agent string, lines int) (string, error)
}

const (
	TabTerminal = "terminal"
	TabClaude   = "claude"
	TabEditor   = "nvim"
)

type Snapshot struct {
	Workspaces []herdr.Workspace
	Agents     []herdr.Agent
	Err        error
	At         time.Time
}

func Poll(h Herdr) Snapshot {
	snap := Snapshot{At: time.Now()}
	snap.Workspaces, snap.Err = h.Workspaces()
	if snap.Err != nil {
		return snap
	}
	snap.Agents, snap.Err = h.Agents()
	return snap
}

func (s Snapshot) Online() bool { return s.Err == nil && !s.At.IsZero() }

func (s Snapshot) Workspace(id string) (herdr.Workspace, bool) {
	for _, w := range s.Workspaces {
		if w.ID == id {
			return w, true
		}
	}
	return herdr.Workspace{}, false
}

func (s Snapshot) WorkspaceByLabel(label string) (herdr.Workspace, bool) {
	for _, w := range s.Workspaces {
		if strings.EqualFold(w.Label, label) {
			return w, true
		}
	}
	return herdr.Workspace{}, false
}

func (s Snapshot) Agent(name string) (herdr.Agent, bool) {
	for _, a := range s.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return herdr.Agent{}, false
}

func (s Snapshot) AgentIn(workspaceID string) (herdr.Agent, bool) {
	var found herdr.Agent
	ok := false
	for _, a := range s.Agents {
		if a.WorkspaceID != workspaceID {
			continue
		}
		if a.Kind == "claude" {
			return a, true
		}
		if !ok {
			found, ok = a, true
		}
	}
	return found, ok
}

func (s Snapshot) nameTaken(name string) bool {
	_, ok := s.Agent(name)
	return ok || name == HubAgent
}

func (s Snapshot) labelTaken(label string) bool {
	_, ok := s.WorkspaceByLabel(label)
	return ok || ReservedLabel(label)
}

func ReservedLabel(label string) bool { return strings.EqualFold(label, HubLabel) }

func Reconcile(sess *session.Session, snap Snapshot) bool {
	if !snap.Online() || !sess.Active() || sess.Label == "" {
		return false
	}
	before := *sess
	ws, ok := snap.Workspace(sess.Workspace)
	if !ok || !strings.EqualFold(ws.Label, sess.Label) {
		ws, ok = snap.WorkspaceByLabel(sess.Label)
	}
	if !ok {
		sess.Workspace, sess.Agent = "", ""
		return before != *sess
	}
	sess.Workspace = ws.ID
	if a, ok := snap.AgentIn(ws.ID); ok {
		sess.Agent = a.Name
	} else {
		sess.Agent = ""
	}
	return before != *sess
}

type OpenOpts struct {
	ClaudeArgs []string
	Editor     string
	TaskText   string
	Kickoff    string
}

func Open(h Herdr, snap Snapshot, sess session.Session, o OpenOpts) (session.Session, error) {
	dir := sess.Worktree
	if dir == "" {
		dir = sess.Repo
	}
	if sess.Label == "" || snap.labelTaken(sess.Label) {
		base := sess.Key
		if base == "" {
			base = sess.Service
		}
		sess.Label = herdr.UniqueName(herdr.AgentName(base), snap.labelTaken)
	}
	if o.TaskText != "" {
		if err := os.WriteFile(filepath.Join(dir, claude.TaskFile), []byte(o.TaskText), 0o644); err != nil {
			return sess, err
		}
		_ = worktree.ExcludeFiles(dir, claude.TaskFile)
	}
	ws, term, _, err := h.CreateWorkspace(sess.Label, dir)
	if err != nil {
		return sess, err
	}
	sess.Workspace = ws.ID
	_ = h.RenameTab(term.ID, TabTerminal)

	_, claudePane, err := h.CreateTab(ws.ID, dir, TabClaude)
	if err != nil {
		return sess, err
	}
	if editor := strings.TrimSpace(o.Editor); editor != "" {
		if _, editorPane, err := h.CreateTab(ws.ID, dir, TabEditor); err == nil {
			_ = h.Run(editorPane.ID, editor)
		}
	}

	name := herdr.UniqueName(herdr.AgentName(sess.Label), snap.nameTaken)
	sess.Agent = name
	sess.PendingPrompt = o.Kickoff
	if err := h.StartAgent(name, "claude", claudePane.ID, o.ClaudeArgs...); err != nil && !herdr.IsCode(err, herdr.CodeAgentNotReady) {
		sess.Err = err.Error()
	}
	return sess, nil
}

func EnsureAgent(h Herdr, snap Snapshot, sess session.Session, args []string) (name string, started bool, err error) {
	if sess.Workspace == "" {
		return "", false, errors.New("workspace fechado — abra de novo com w")
	}
	if a, ok := snap.AgentIn(sess.Workspace); ok {
		return a.Name, false, nil
	}
	dir := sess.Worktree
	if dir == "" {
		dir = sess.Repo
	}
	_, pane, err := h.CreateTab(sess.Workspace, dir, TabClaude)
	if err != nil {
		return "", false, err
	}
	name = herdr.UniqueName(herdr.AgentName(sess.Name()), snap.nameTaken)
	if err := h.StartAgent(name, "claude", pane.ID, args...); err != nil && !herdr.IsCode(err, herdr.CodeAgentNotReady) {
		return "", false, err
	}
	return name, true, nil
}

func Deliver(h Herdr, snap Snapshot, sess *session.Session) (bool, error) {
	if sess.PendingPrompt == "" || sess.Agent == "" {
		return false, nil
	}
	a, ok := snap.Agent(sess.Agent)
	if !ok || !a.Ready() {
		return false, nil
	}
	if err := h.Prompt(a.Name, sess.PendingPrompt); err != nil {
		if herdr.IsCode(err, herdr.CodePromptStalled) {
			return false, nil
		}
		return false, err
	}
	sess.PendingPrompt = ""
	return true, nil
}

func ClaudeArgs(model, permissionMode string) []string {
	var args []string
	if model != "" {
		args = append(args, "--model", model)
	}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}
	return args
}
