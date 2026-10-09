package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Heartbeat é o que o mod fleet (~/.claude/mods/fleet) grava por sessão em
// ~/.claude/fleet/<sessionId>.json: ticket, branch, uso e arquivos editados.
type Heartbeat struct {
	ID        string   `json:"id"`
	Ticket    string   `json:"ticket"`
	Cwd       string   `json:"cwd"`
	Repo      string   `json:"repo"`
	Root      string   `json:"root"`
	Branch    string   `json:"branch"`
	Prompt    string   `json:"prompt"`
	CtxPct    *int     `json:"ctxPct"`
	USD       *float64 `json:"usd"`
	Agents    int      `json:"agents"`
	Files     []string `json:"files"`
	IsEnded   bool     `json:"isEnded"`
	UpdatedAt int64    `json:"updatedAt"`
}

// ClaudeDir é ~/.claude.
func ClaudeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// ReadHeartbeats lê os heartbeats do último dia, por session id. Sessões sem
// o mod simplesmente não aparecem.
func ReadHeartbeats(claudeDir string) map[string]Heartbeat {
	dir := filepath.Join(claudeDir, "fleet")
	entries, err := os.ReadDir(dir)
	out := map[string]Heartbeat{}
	if err != nil {
		return out
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if info, err := e.Info(); err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var hb Heartbeat
		if json.Unmarshal(data, &hb) == nil && hb.ID != "" {
			out[hb.ID] = hb
		}
	}
	return out
}

// Transcript resume o fim da conversa de uma sessão.
type Transcript struct {
	Recent        []string  // últimos textos do assistente, o mais novo no fim
	ContextTokens int       // entrada da última resposta (contexto em uso)
	LastAt        time.Time // modificação do arquivo
}

// Last devolve o último texto do assistente.
func (t Transcript) Last() string {
	if len(t.Recent) == 0 {
		return ""
	}
	return t.Recent[len(t.Recent)-1]
}

// TranscriptPath é onde o Claude Code guarda a conversa: a pasta do projeto
// vira um nome com tudo que não é letra/dígito trocado por '-'.
func TranscriptPath(claudeDir, cwd, sessionID string) string {
	enc := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, cwd)
	return filepath.Join(claudeDir, "projects", enc, sessionID+".jsonl")
}

const (
	tailBytes  = 512 * 1024 // o fim da conversa basta; transcripts chegam a dezenas de MB
	keepRecent = 5
)

type transcriptLine struct {
	Type    string `json:"type"`
	Message struct {
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input         int `json:"input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ReadTranscript lê só o fim do transcript: os últimos textos do assistente
// e o contexto que a última resposta usou.
func ReadTranscript(path string) (Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return Transcript{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Transcript{}, err
	}
	t := Transcript{LastAt: info.ModTime()}
	skipFirst := false
	if info.Size() > tailBytes {
		if _, err := f.Seek(-tailBytes, io.SeekEnd); err != nil {
			return t, err
		}
		skipFirst = true // a primeira linha veio cortada pelo meio
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		if skipFirst {
			skipFirst = false
			continue
		}
		var l transcriptLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Type != "assistant" {
			continue
		}
		if u := l.Message.Usage; u != nil {
			t.ContextTokens = u.Input + u.CacheRead + u.CacheCreation
		}
		if text := assistantText(l.Message.Content); text != "" {
			t.Recent = append(t.Recent, text)
			if len(t.Recent) > keepRecent {
				t.Recent = t.Recent[len(t.Recent)-keepRecent:]
			}
		}
	}
	return t, sc.Err()
}

func assistantText(raw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n")
}
