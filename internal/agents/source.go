package agents

// Local é a fonte real dos agents: o CLI do Claude Code e o que ele deixa
// em ~/.claude (heartbeats do mod fleet e transcripts).
type Local struct {
	CLI
	Dir string // ~/.claude
}

// NewLocal monta a fonte real com o binário configurado.
func NewLocal(bin string) Local { return Local{CLI: CLI{Bin: bin}, Dir: ClaudeDir()} }

// Heartbeats lê os heartbeats do mod fleet.
func (l Local) Heartbeats() map[string]Heartbeat { return ReadHeartbeats(l.Dir) }

// Rename pede ao mod fleet da sessão que a renomeie (/rename).
func (l Local) Rename(sessionID, name string) error { return RequestRename(l.Dir, sessionID, name) }

// Transcript lê o fim da conversa da sessão que roda (ou rodou) em cwd.
func (l Local) Transcript(cwd, sessionID string) (Transcript, error) {
	return ReadTranscript(TranscriptPath(l.Dir, cwd, sessionID))
}
