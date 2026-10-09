package herdr

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const workspaceCreated = `{"id":"cli:workspace:create","result":{"root_pane":{"agent_status":"unknown","cwd":"/src/x","focused":true,"foreground_cwd":"/src/x","pane_id":"w1:p1","revision":0,"tab_id":"w1:t1","terminal_id":"term_1","workspace_id":"w1"},"tab":{"agent_status":"unknown","focused":true,"label":"1","number":1,"pane_count":1,"tab_id":"w1:t1","workspace_id":"w1"},"type":"workspace_created","workspace":{"active_tab_id":"w1:t1","agent_status":"unknown","focused":true,"label":"abc-123","number":1,"pane_count":1,"tab_count":1,"workspace_id":"w1"}}}`

const agentList = `{"id":"cli:agent:list","result":{"agents":[{"agent":"claude","agent_status":"blocked","cwd":"/src/x","focused":false,"foreground_cwd":"/src/x","launch_pending":true,"name":"abc-123","pane_id":"w1:p2","revision":0,"state_change_seq":1,"tab_id":"w1:t2","terminal_id":"term_2","workspace_id":"w1"}],"type":"agent_list"}}`

func TestDecode(t *testing.T) {
	var r struct {
		Workspace Workspace `json:"workspace"`
		Tab       Tab       `json:"tab"`
		RootPane  Pane      `json:"root_pane"`
	}
	if err := decode([]byte(workspaceCreated), &r); err != nil {
		t.Fatal(err)
	}
	if r.Workspace.ID != "w1" || r.Workspace.Label != "abc-123" || r.Tab.ID != "w1:t1" || r.RootPane.ID != "w1:p1" || r.RootPane.Cwd != "/src/x" {
		t.Fatalf("decode = %+v", r)
	}

	var a struct {
		Agents []Agent `json:"agents"`
	}
	if err := decode([]byte(agentList), &a); err != nil {
		t.Fatal(err)
	}
	if len(a.Agents) != 1 || a.Agents[0].Name != "abc-123" || a.Agents[0].Kind != "claude" ||
		a.Agents[0].Status != StatusBlocked || a.Agents[0].WorkspaceID != "w1" || a.Agents[0].Ready() {
		t.Fatalf("agents = %+v", a.Agents)
	}

	err := decode([]byte(`{"id":"x","error":{"code":"agent_not_ready","message":"blocked"}}`), nil)
	if !IsCode(err, CodeAgentNotReady) {
		t.Fatalf("erro = %v", err)
	}
}

func TestAgentName(t *testing.T) {
	cases := map[string]string{
		"ABC-123":                "abc-123",
		"hades!9470":             "hades-9470",
		"123":                    "w123",
		"  Ordens / Histórico  ": "ordens-hist-rico",
		"um-nome-bem-comprido-que-passa-do-limite-de-32": "um-nome-bem-comprido-que-passa-d",
	}
	for in, want := range cases {
		if got := AgentName(in); got != want {
			t.Errorf("AgentName(%q) = %q, esperado %q", in, got, want)
		}
	}
	taken := map[string]bool{"abc": true, "abc-2": true}
	if got := UniqueName("abc", func(s string) bool { return taken[s] }); got != "abc-3" {
		t.Errorf("UniqueName = %q", got)
	}
	if got := UniqueName("livre", func(string) bool { return false }); got != "livre" {
		t.Errorf("UniqueName = %q", got)
	}
}

func TestClientParsesCLIError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script de shell")
	}
	bin := filepath.Join(t.TempDir(), "herdr")
	script := "#!/bin/sh\necho '{\"id\":\"x\",\"error\":{\"code\":\"server_not_running\",\"message\":\"no server\"}}' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := New(bin).Workspaces()
	if !IsCode(err, CodeServerNotRunning) {
		t.Fatalf("erro = %v", err)
	}

	ok := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\necho '"+agentList+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agents, err := New(ok).Agents()
	if err != nil || len(agents) != 1 {
		t.Fatalf("agents = %v, %v", agents, err)
	}
}
