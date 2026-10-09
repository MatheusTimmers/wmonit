package demo

import (
	"errors"
	"os/exec"
	"time"

	"github.com/timmers/wmonit/internal/agents"
)

// AgentsSource devolve um agent view inventado: os agents das sessões demo
// e uma sessão interativa avulsa. Ações (criar, parar, attach) não rodam.
type AgentsSource struct{}

var errDemo = errors.New("indisponível no modo demo")

func intp(v int) *int           { return &v }
func floatp(v float64) *float64 { return &v }

func (AgentsSource) List() ([]agents.Live, error) {
	started := time.Now().Add(-40 * time.Minute).UnixMilli()
	return []agents.Live{
		{PID: 101, ID: "a1b2c3d4", SessionID: "a1b2c3d4-demo", Cwd: `c:\Fontes\.worktrees\ROT-512-demo`, Kind: "background",
			Name: "ROT-512 endpoint de histórico de ordens", Status: "busy", State: "working", StartedAt: started},
		{PID: 102, ID: "e5f6a7b8", SessionID: "e5f6a7b8-demo", Cwd: `c:\Fontes\.worktrees\hades-9470-demo`, Kind: "background",
			Name: "hades!9470 novo formato de relatório", Status: "idle", State: "done", StartedAt: started},
		{PID: 103, SessionID: "c9d0-demo", Cwd: `c:\Fontes\backoffice`, Kind: "interactive",
			Name: "investigação de leak", Status: "busy", StartedAt: started},
	}, nil
}

func (AgentsSource) Heartbeats() map[string]agents.Heartbeat {
	return map[string]agents.Heartbeat{
		"a1b2c3d4-demo": {ID: "a1b2c3d4-demo", Ticket: "ROT-512", Repo: "backoffice", Root: "c:/Fontes/.worktrees/ROT-512-demo",
			Branch: "feature/ROT-512", CtxPct: intp(38), USD: floatp(2.41), Agents: 1, Files: []string{"source/ordensu.pas"}},
		"e5f6a7b8-demo": {ID: "e5f6a7b8-demo", Repo: "hades", Root: "c:/Fontes/.worktrees/hades-9470-demo",
			Branch: "feature/ROT-500", CtxPct: intp(22), USD: floatp(0.87)},
		"c9d0-demo": {ID: "c9d0-demo", Repo: "backoffice", Root: "c:/Fontes/backoffice", Branch: "develop",
			CtxPct: intp(81), USD: floatp(6.10), Files: []string{"source/ordensu.pas"}},
	}
}

func (AgentsSource) Transcript(cwd, sessionID string) (agents.Transcript, error) {
	switch sessionID {
	case "a1b2c3d4-demo":
		return agents.Transcript{Recent: []string{
			"Plano: 1) handler GET /ordens/historico; 2) consulta paginada; 3) registro na rota.",
			"Implementando a consulta paginada em OrdensU.pas…",
		}}, nil
	case "e5f6a7b8-demo":
		return agents.Transcript{Recent: []string{
			"PEDIR AJUSTES: o formato novo não trata o relatório vazio (hades/report.go:88) e o cabeçalho ignora o fuso configurado (report.go:120).",
		}}, nil
	}
	return agents.Transcript{}, errDemo
}

func (AgentsSource) Launch(agents.LaunchOpts) (string, error)     { return "", errDemo }
func (AgentsSource) Resume(dir, sessionID string) (string, error) { return "", errDemo }
func (AgentsSource) Stop(ref string) error                        { return errDemo }
func (AgentsSource) Remove(ref string) error                      { return errDemo }
func (AgentsSource) AttachCmd(ref string) *exec.Cmd               { return nil }
func (AgentsSource) Rename(sessionID, name string) error          { return nil }

// Summarize devolve uma leitura fixa no lugar do haiku.
func (AgentsSource) Summarize(in agents.SummaryInput) (agents.Summary, error) {
	return agents.Summary{Name: "Histórico paginado de ordens", Missing: "registrar a rota e revisar o diff", At: time.Now()}, nil
}
func (AgentsSource) InteractiveCmd(dir, sessionID string) *exec.Cmd {
	return nil
}
