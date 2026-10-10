package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	StatusWorking = "working"
	StatusBlocked = "blocked"
	StatusDone    = "done"
	StatusIdle    = "idle"
	StatusUnknown = "unknown"
)

const (
	CodeServerNotRunning = "server_not_running"
	CodeAgentNotReady    = "agent_not_ready"
	CodeAgentBlocked     = "agent_blocked"
	CodePromptStalled    = "agent_prompt_stalled"
	CodePaneBusy         = "agent_pane_busy"
)

type Workspace struct {
	ID          string            `json:"workspace_id"`
	Label       string            `json:"label"`
	Number      int               `json:"number"`
	AgentStatus string            `json:"agent_status"`
	Focused     bool              `json:"focused"`
	TabCount    int               `json:"tab_count"`
	ActiveTabID string            `json:"active_tab_id"`
	Tokens      map[string]string `json:"tokens"`
}

type Tab struct {
	ID          string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
}

type Pane struct {
	ID            string `json:"pane_id"`
	TabID         string `json:"tab_id"`
	WorkspaceID   string `json:"workspace_id"`
	Cwd           string `json:"cwd"`
	ForegroundCwd string `json:"foreground_cwd"`
	AgentStatus   string `json:"agent_status"`
}

type Agent struct {
	Name          string        `json:"name"`
	Kind          string        `json:"agent"`
	Status        string        `json:"agent_status"`
	PaneID        string        `json:"pane_id"`
	TabID         string        `json:"tab_id"`
	WorkspaceID   string        `json:"workspace_id"`
	Cwd           string        `json:"cwd"`
	LaunchPending bool          `json:"launch_pending"`
	Session       *AgentSession `json:"agent_session"`
	Title         string        `json:"terminal_title_stripped"`
}

type AgentSession struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

func (a Agent) SessionID() string {
	if a.Session == nil || a.Session.Kind != "id" {
		return ""
	}
	return a.Session.Value
}

type State struct {
	Workspaces []Workspace `json:"workspaces"`
	Tabs       []Tab       `json:"tabs"`
	Panes      []Pane      `json:"panes"`
	Agents     []Agent     `json:"agents"`
	Version    string      `json:"version"`
}

func (a Agent) Ready() bool { return a.Status == StatusIdle || a.Status == StatusDone }

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return "herdr: " + e.Message }

func IsCode(err error, code string) bool {
	var he *Error
	return errors.As(err, &he) && he.Code == code
}

type Client struct {
	Bin       string
	Session   string
	Timeout   time.Duration
	ShellWait time.Duration
}

func New(bin string) *Client {
	if bin == "" {
		bin = "herdr"
	}
	return &Client{Bin: bin, Timeout: 15 * time.Second, ShellWait: 15 * time.Second}
}

type envelope struct {
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

func (c *Client) raw(timeout time.Duration, args ...string) ([]byte, error) {
	if c.Session != "" {
		args = append([]string{"--session", c.Session}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	name := commandName(args)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s: sem resposta em %s", name, timeout)
	}
	if err != nil {
		for _, out := range [][]byte{stderr.Bytes(), stdout.Bytes()} {
			var env envelope
			if json.Unmarshal(bytes.TrimSpace(out), &env) == nil && env.Error != nil {
				return nil, env.Error
			}
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", name, msg)
	}
	return stdout.Bytes(), nil
}

func commandName(args []string) string {
	var parts []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(parts) == 2 {
			break
		}
		parts = append(parts, a)
	}
	return "herdr " + strings.Join(parts, " ")
}

func (c *Client) call(timeout time.Duration, out any, args ...string) error {
	data, err := c.raw(timeout, args...)
	if err != nil {
		return err
	}
	return decode(data, out)
}

func decode(data []byte, out any) error {
	if len(bytes.TrimSpace(data)) == 0 && out == nil {
		return nil
	}
	var env envelope
	if err := json.Unmarshal(bytes.TrimSpace(data), &env); err != nil {
		return fmt.Errorf("herdr: resposta inválida: %w", err)
	}
	if env.Error != nil {
		return env.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

func (c *Client) Workspaces() ([]Workspace, error) {
	var r struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	err := c.call(c.Timeout, &r, "workspace", "list")
	return r.Workspaces, err
}

func (c *Client) Panes(workspaceID string) ([]Pane, error) {
	var r struct {
		Panes []Pane `json:"panes"`
	}
	err := c.call(c.Timeout, &r, "pane", "list", "--workspace", workspaceID)
	return r.Panes, err
}

func (c *Client) Tabs(workspaceID string) ([]Tab, error) {
	var r struct {
		Tabs []Tab `json:"tabs"`
	}
	err := c.call(c.Timeout, &r, "tab", "list", "--workspace", workspaceID)
	return r.Tabs, err
}

func (c *Client) Agents() ([]Agent, error) {
	var r struct {
		Agents []Agent `json:"agents"`
	}
	err := c.call(c.Timeout, &r, "agent", "list")
	return r.Agents, err
}

func (c *Client) State() (State, error) {
	var r struct {
		Snapshot State `json:"snapshot"`
	}
	err := c.call(c.Timeout, &r, "api", "snapshot")
	return r.Snapshot, err
}

func (c *Client) ReportMetadata(workspaceID, source string, tokens map[string]string) error {
	args := []string{"workspace", "report-metadata", workspaceID, "--source", source}
	for _, k := range sortedKeys(tokens) {
		if v := tokens[k]; v != "" {
			args = append(args, "--token", k+"="+v)
		} else {
			args = append(args, "--clear-token", k)
		}
	}
	return c.call(c.Timeout, nil, args...)
}

func (c *Client) Notify(title, body, sound string) error {
	args := []string{"notification", "show", title}
	if body != "" {
		args = append(args, "--body", body)
	}
	if sound != "" {
		args = append(args, "--sound", sound)
	}
	return c.call(c.Timeout, nil, args...)
}

func (c *Client) Wait(agent string, timeout time.Duration, until ...string) (Agent, error) {
	args := []string{"agent", "wait", agent, "--timeout", fmt.Sprint(timeout.Milliseconds())}
	for _, u := range until {
		args = append(args, "--until", u)
	}
	var r struct {
		Agent Agent `json:"agent"`
	}
	err := c.call(timeout+5*time.Second, &r, args...)
	return r.Agent, err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *Client) CreateWorkspace(label, cwd string) (Workspace, Tab, Pane, error) {
	var r struct {
		Workspace Workspace `json:"workspace"`
		Tab       Tab       `json:"tab"`
		RootPane  Pane      `json:"root_pane"`
	}
	err := c.call(c.Timeout, &r, "workspace", "create", "--cwd", cwd, "--label", label, "--no-focus")
	return r.Workspace, r.Tab, r.RootPane, err
}

func (c *Client) CreateTab(workspaceID, cwd, label string) (Tab, Pane, error) {
	var r struct {
		Tab      Tab  `json:"tab"`
		RootPane Pane `json:"root_pane"`
	}
	err := c.call(c.Timeout, &r, "tab", "create", "--workspace", workspaceID, "--cwd", cwd, "--label", label)
	return r.Tab, r.RootPane, err
}

func (c *Client) RenameTab(tabID, label string) error {
	return c.call(c.Timeout, nil, "tab", "rename", tabID, label)
}

func (c *Client) FocusTab(tabID string) error {
	return c.call(c.Timeout, nil, "tab", "focus", tabID)
}

func (c *Client) FocusWorkspace(workspaceID string) error {
	return c.call(c.Timeout, nil, "workspace", "focus", workspaceID)
}

func (c *Client) CloseWorkspace(workspaceID string) error {
	return c.call(c.Timeout, nil, "workspace", "close", workspaceID)
}

func (c *Client) Run(paneID, command string) error {
	return c.call(c.Timeout, nil, "pane", "run", paneID, command)
}

func (c *Client) StartAgent(name, kind, paneID string, args ...string) error {
	cmd := []string{"agent", "start", name, "--kind", kind, "--pane", paneID, "--timeout", "40000"}
	if len(args) > 0 {
		cmd = append(append(cmd, "--"), args...)
	}
	deadline := time.Now().Add(c.ShellWait)
	for {
		err := c.call(50*time.Second, nil, cmd...)
		if !IsCode(err, CodePaneBusy) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (c *Client) Prompt(agent, text string) error {
	return c.call(c.Timeout, nil, "agent", "prompt", agent, text)
}

func (c *Client) SendKeys(agent string, keys ...string) error {
	return c.call(c.Timeout, nil, append([]string{"agent", "send-keys", agent}, keys...)...)
}

func (c *Client) Read(agent string, lines int) (string, error) {
	data, err := c.raw(c.Timeout, "agent", "read", agent, "--source", "recent-unwrapped", "--lines", fmt.Sprint(lines))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n "), nil
}

var invalidName = regexp.MustCompile(`[^a-z0-9_-]+`)

func AgentName(label string) string {
	name := strings.Trim(invalidName.ReplaceAllString(strings.ToLower(label), "-"), "-_")
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		name = "w" + name
	}
	if len(name) > 32 {
		name = strings.TrimRight(name[:32], "-_")
	}
	return name
}

func UniqueName(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("-%d", i)
		name := base
		if len(name)+len(suffix) > 32 {
			name = strings.TrimRight(name[:32-len(suffix)], "-_")
		}
		if !taken(name + suffix) {
			return name + suffix
		}
	}
}
