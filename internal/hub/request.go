package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const requestFile = "pedido.json"

type Request struct {
	Asked   time.Time `json:"asked"`
	Pending bool      `json:"pending"`
}

func SaveRequest(dir string, r Request) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, requestFile), data, 0o644)
}

func LoadRequest(dir string) (Request, bool) {
	data, err := os.ReadFile(filepath.Join(dir, requestFile))
	if err != nil {
		return Request{}, false
	}
	var r Request
	if json.Unmarshal(data, &r) != nil || r.Asked.IsZero() {
		return Request{}, false
	}
	return r, true
}

func TrustKeys(screen string) ([]string, bool) {
	if !strings.Contains(screen, "trust this folder") {
		return nil, false
	}
	for _, l := range strings.Split(screen, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "❯") {
			continue
		}
		if strings.Contains(l, "Yes") {
			return []string{"enter"}, true
		}
		if strings.Contains(l, "No") {
			return []string{"down", "enter"}, true
		}
	}
	return nil, false
}
