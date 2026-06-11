package jira

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	base    string
	auth    string // "bearer" ou "basic"
	email   string
	token   string
	cxField string // campo de complexidade; vazio = descobrir pela API
	http    *http.Client
}

func New(base, auth, email, token, cxField string) *Client {
	return &Client{
		base:    strings.TrimRight(base, "/"),
		auth:    auth,
		email:   email,
		token:   token,
		cxField: cxField,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

type Issue struct {
	Key        string
	Summary    string
	Status     string
	Category   string // statusCategory: "new", "indeterminate", "done"
	Type       string // tipo da issue (Bug, Tarefa…)
	Due        string // YYYY-MM-DD, vazio se não tiver
	Created    string // YYYY-MM-DD
	Resolved   string // YYYY-MM-DD, vazio se não resolvida
	Complexity string // valor do campo de complexidade, vazio se não preenchido
	Priority   string // nome da prioridade ("High", "Medium"…)
	PrioRank   int    // id numérico da prioridade; menor = mais urgente
}

type Summary struct {
	Open     []Issue // atribuídas a você, não concluídas
	Resolved []Issue // resolvidas desde o início do mês anterior
	CXField  string  // campo usado para complexidade; vazio se não encontrado
}

func (c *Client) get(path string, q url.Values, out any) (int, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return 0, err
	}
	if c.auth == "basic" {
		req.SetBasicAuth(c.email, c.token)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("Jira %s: HTTP %d", path, resp.StatusCode)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

type searchResp struct {
	Issues []struct {
		Key    string          `json:"key"`
		Fields json.RawMessage `json:"fields"`
	} `json:"issues"`
}

type issueFields struct {
	Summary        string `json:"summary"`
	DueDate        string `json:"duedate"`
	Created        string `json:"created"`
	ResolutionDate string `json:"resolutiondate"`
	Status         struct {
		Name           string `json:"name"`
		StatusCategory struct {
			Key string `json:"key"`
		} `json:"statusCategory"`
	} `json:"status"`
	IssueType struct {
		Name string `json:"name"`
	} `json:"issuetype"`
	Priority struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"priority"`
}

// complexityValue extrai o valor do campo de complexidade, que pode ser
// número, texto ou opção de select ({"value": "Alta"}).
func complexityValue(raw json.RawMessage, field string) string {
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return ""
	}
	v, ok := all[field]
	if !ok || string(v) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	var n float64
	if json.Unmarshal(v, &n) == nil {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	var opt struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(v, &opt) == nil && opt.Value != "" {
		return opt.Value
	}
	return ""
}

func dateOnly(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func (c *Client) doSearch(path, jql string, max int, cxField string) ([]Issue, int, error) {
	fields := "summary,status,issuetype,duedate,created,resolutiondate,priority"
	if cxField != "" {
		fields += "," + cxField
	}
	q := url.Values{
		"jql":        {jql},
		"maxResults": {fmt.Sprint(max)},
		"fields":     {fields},
	}
	var sr searchResp
	code, err := c.get(path, q, &sr)
	if err != nil {
		return nil, code, err
	}
	issues := make([]Issue, 0, len(sr.Issues))
	for _, raw := range sr.Issues {
		var f issueFields
		if err := json.Unmarshal(raw.Fields, &f); err != nil {
			continue
		}
		cx := ""
		if cxField != "" {
			cx = complexityValue(raw.Fields, cxField)
		}
		rank := 1 << 30 // sem prioridade vai para o fim
		if n, err := strconv.Atoi(f.Priority.ID); err == nil {
			rank = n
		}
		issues = append(issues, Issue{
			Key:        raw.Key,
			Summary:    f.Summary,
			Status:     f.Status.Name,
			Category:   f.Status.StatusCategory.Key,
			Type:       f.IssueType.Name,
			Due:        f.DueDate,
			Created:    dateOnly(f.Created),
			Resolved:   dateOnly(f.ResolutionDate),
			Complexity: cx,
			Priority:   f.Priority.Name,
			PrioRank:   rank,
		})
	}
	return issues, code, nil
}

func (c *Client) search(jql string, max int, cxField string) ([]Issue, error) {
	issues, code, err := c.doSearch("/rest/api/2/search", jql, max, cxField)
	// Jira Cloud removeu /rest/api/2/search; o substituto é /search/jql.
	if code == http.StatusGone || code == http.StatusNotFound {
		issues, _, err = c.doSearch("/rest/api/3/search/jql", jql, max, cxField)
	}
	return issues, err
}

// resolveCXField devolve o id do campo de complexidade: o configurado, ou
// o primeiro campo cujo nome contenha "complex" (Complexidade, Complexity…).
func (c *Client) resolveCXField() string {
	if c.cxField != "" {
		return c.cxField
	}
	var fields []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if _, err := c.get("/rest/api/2/field", nil, &fields); err != nil {
		return ""
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f.Name), "complex") {
			return f.ID
		}
	}
	return ""
}

func (c *Client) Fetch() (*Summary, error) {
	if c.base == "" || c.token == "" {
		return nil, fmt.Errorf("Jira não configurado — defina url e token em %s", "~/.config/wmonit/config.toml")
	}
	cx := c.resolveCXField()

	open, err := c.search(`assignee = currentUser() AND statusCategory != Done ORDER BY status, updated DESC`, 50, cx)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	prevMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -1, 0)
	jql := fmt.Sprintf(`assignee = currentUser() AND statusCategory = Done AND resolved >= "%s" ORDER BY resolved DESC`,
		prevMonthStart.Format("2006-01-02"))
	resolved, err := c.search(jql, 200, cx)
	if err != nil {
		return nil, err
	}

	return &Summary{Open: open, Resolved: resolved, CXField: cx}, nil
}
