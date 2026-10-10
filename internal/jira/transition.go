package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   struct {
		Name string `json:"name"`
	} `json:"to"`
}

func (c *Client) Transitions(ctx context.Context, key string) ([]Transition, error) {
	var r struct {
		Transitions []Transition `json:"transitions"`
	}
	_, err := c.get(ctx, "/rest/api/2/issue/"+url.PathEscape(key)+"/transitions", nil, &r)
	return r.Transitions, err
}

func (c *Client) TransitionTo(ctx context.Context, key, target string) (string, error) {
	ts, err := c.Transitions(ctx, key)
	if err != nil {
		return "", err
	}
	var pick *Transition
	for i, t := range ts {
		if strings.EqualFold(t.To.Name, target) || strings.EqualFold(t.Name, target) {
			pick = &ts[i]
			break
		}
	}
	if pick == nil {
		var names []string
		for _, t := range ts {
			names = append(names, t.To.Name)
		}
		return "", fmt.Errorf("%s não pode ir para %q agora (disponíveis: %s)", key, target, strings.Join(names, ", "))
	}
	body, _ := json.Marshal(map[string]any{"transition": map[string]string{"id": pick.ID}})
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/rest/api/2/issue/"+url.PathEscape(key)+"/transitions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", fmt.Errorf("Jira transição de %s: HTTP %d: %s", key, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return pick.To.Name, nil
}

func (c *Client) authorize(req *http.Request) {
	if c.auth == "basic" {
		req.SetBasicAuth(c.email, c.token)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
