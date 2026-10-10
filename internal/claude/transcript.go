package claude

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func ConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func TranscriptPath(sessionID string) string {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\*?[`) {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(ConfigDir(), "projects", "*", sessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

type Price struct {
	Input      float64 `toml:"input"`
	Output     float64 `toml:"output"`
	CacheRead  float64 `toml:"cache_read"`
	CacheWrite float64 `toml:"cache_write"`
}

type Usage struct {
	Model      string
	Title      string
	LastPrompt string
	LastTool   string
	LastText   string
	Turns      int
	Input      int
	Output     int
	CacheRead  int
	CacheWrite int
	Started    time.Time
	LastAt     time.Time
}

func (u Usage) Tokens() int { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

func (u Usage) Cost(prices map[string]Price) (float64, bool) {
	model := strings.ToLower(u.Model)
	best := ""
	for k := range prices {
		if strings.Contains(model, strings.ToLower(k)) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return 0, false
	}
	p := prices[best]
	cost := float64(u.Input)*p.Input + float64(u.Output)*p.Output + float64(u.CacheRead)*p.CacheRead + float64(u.CacheWrite)*p.CacheWrite
	return cost / 1e6, true
}

type transcriptLine struct {
	Type        string    `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	AITitle     string    `json:"aiTitle"`
	LastPrompt  string    `json:"lastPrompt"`
	IsSidechain bool      `json:"isSidechain"`
	Message     *struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input      int `json:"input_tokens"`
			Output     int `json:"output_tokens"`
			CacheRead  int `json:"cache_read_input_tokens"`
			CacheWrite int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

func ReadUsage(path string) (Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Usage{}, err
	}
	defer f.Close()
	var u Usage
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var l transcriptLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if !l.Timestamp.IsZero() {
			if u.Started.IsZero() {
				u.Started = l.Timestamp
			}
			u.LastAt = l.Timestamp
		}
		switch l.Type {
		case "ai-title":
			u.Title = l.AITitle
		case "last-prompt":
			u.LastPrompt = l.LastPrompt
		case "user":
			if l.Message != nil && !l.IsSidechain && isPrompt(l.Message.Content) {
				u.Turns++
			}
		case "assistant":
			m := l.Message
			if m == nil || l.IsSidechain {
				continue
			}
			if m.Model != "" && !strings.HasPrefix(m.Model, "<") {
				u.Model = m.Model
			}
			var blocks []contentBlock
			if json.Unmarshal(m.Content, &blocks) == nil {
				for _, b := range blocks {
					switch b.Type {
					case "tool_use":
						u.LastTool = b.Name
					case "text":
						if t := strings.TrimSpace(b.Text); t != "" {
							u.LastText = t
						}
					}
				}
			}
			if m.Usage == nil || (m.ID != "" && seen[m.ID]) {
				continue
			}
			seen[m.ID] = true
			u.Input += m.Usage.Input
			u.Output += m.Usage.Output
			u.CacheRead += m.Usage.CacheRead
			u.CacheWrite += m.Usage.CacheWrite
		}
	}
	return u, sc.Err()
}

func isPrompt(content json.RawMessage) bool {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return strings.TrimSpace(s) != ""
	}
	var blocks []contentBlock
	if json.Unmarshal(content, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" {
			return true
		}
	}
	return false
}

func FormatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
