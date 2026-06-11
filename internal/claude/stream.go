package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

// Progress é o estado de uma execução extraído do log stream-json.
type Progress struct {
	SessionID string   // session_id do Claude (para --resume)
	Turns     int      // mensagens do assistente até agora
	LastText  string   // último texto do assistente
	Tools     []string // últimas ferramentas usadas (mais recente no fim)
	Result    string   // resumo final, quando terminou
	IsError   bool
	Done      bool
}

const keepTools = 5

// streamLine cobre os campos que interessam das linhas do stream-json.
type streamLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
	IsError   bool   `json:"is_error"`
	Message   struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

// ReadProgress lê o log de uma execução e resume o andamento. Linhas que
// não são JSON (ex.: stderr misturado) são ignoradas.
func ReadProgress(path string) (Progress, error) {
	f, err := os.Open(path)
	if err != nil {
		return Progress{}, err
	}
	defer f.Close()

	var p Progress
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024) // linhas podem ser longas
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var l streamLine
		if json.Unmarshal([]byte(line), &l) != nil {
			continue
		}
		if l.SessionID != "" {
			p.SessionID = l.SessionID
		}
		switch l.Type {
		case "assistant":
			p.Turns++
			for _, c := range l.Message.Content {
				switch c.Type {
				case "text":
					if t := strings.TrimSpace(c.Text); t != "" {
						p.LastText = t
					}
				case "tool_use":
					p.Tools = append(p.Tools, c.Name)
					if len(p.Tools) > keepTools {
						p.Tools = p.Tools[len(p.Tools)-keepTools:]
					}
				}
			}
		case "result":
			p.Done = true
			p.IsError = l.IsError || strings.HasPrefix(l.Subtype, "error")
			p.Result = strings.TrimSpace(l.Result)
		}
	}
	return p, sc.Err()
}
