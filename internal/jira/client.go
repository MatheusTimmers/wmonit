package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	base    string
	auth    string // "bearer" ou "basic"
	email   string
	token   string
	cxField string // campo de complexidade; vazio = descobrir pela API
	http    *http.Client

	// Metadados estáticos do Jira (campos e prioridades) memoizados: não
	// mudam entre refreshes, então cada Fetch reaproveita. mu protege o
	// cache contra refreshes concorrentes sobre o mesmo Client.
	mu          sync.Mutex
	cxResolved  string         // campo de complexidade já descoberto
	cxDone      bool           // descoberta já tentada com sucesso
	priosCached map[string]int // nil = ainda não resolvido (ou último erro)
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

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) (int, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
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
		// Lê um trecho do corpo — ajuda a diagnosticar token expirado etc.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		if msg := strings.TrimSpace(string(body)); msg != "" {
			return resp.StatusCode, fmt.Errorf("Jira %s: HTTP %d: %s", path, resp.StatusCode, msg)
		}
		return resp.StatusCode, fmt.Errorf("Jira %s: HTTP %d", path, resp.StatusCode)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

type searchResp struct {
	Issues []struct {
		Key    string          `json:"key"`
		Fields json.RawMessage `json:"fields"`
	} `json:"issues"`
	Total         int    `json:"total"`         // só no /rest/api/2/search
	NextPageToken string `json:"nextPageToken"` // só no /rest/api/3/search/jql
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

// dateOnly converte um timestamp do Jira ("2026-06-11T23:45:00.000-0300")
// para a data no fuso local — truncar a string usaria o fuso do servidor,
// que perto da meia-noite desloca o dia. Datas puras passam direto.
func dateOnly(s string) string {
	if t, err := time.Parse("2006-01-02T15:04:05.000-0700", s); err == nil {
		return t.In(time.Local).Format("2006-01-02")
	}
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// pageSize é o teto real por página dos dois endpoints de busca — pedir
// mais que isso é silenciosamente ignorado, daí a paginação abaixo.
const pageSize = 100

// doSearch executa a JQL paginando até max issues. O /rest/api/2/search
// pagina por startAt/total; o /rest/api/3/search/jql (Cloud), por
// nextPageToken.
func (c *Client) doSearch(ctx context.Context, path, jql string, max int, cxField string, prios map[string]int) ([]Issue, int, error) {
	fields := "summary,status,issuetype,duedate,created,resolutiondate,priority"
	if cxField != "" {
		fields += "," + cxField
	}
	tokenPaged := strings.HasSuffix(path, "/search/jql")
	var all []Issue
	startAt, token := 0, ""
	for len(all) < max {
		q := url.Values{
			"jql":        {jql},
			"maxResults": {fmt.Sprint(min(pageSize, max-len(all)))},
			"fields":     {fields},
		}
		if tokenPaged {
			if token != "" {
				q.Set("nextPageToken", token)
			}
		} else if startAt > 0 {
			q.Set("startAt", fmt.Sprint(startAt))
		}
		var sr searchResp
		code, err := c.get(ctx, path, q, &sr)
		if err != nil {
			return nil, code, err
		}
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
			if r, ok := prios[f.Priority.ID]; ok {
				rank = r
			} else if n, err := strconv.Atoi(f.Priority.ID); err == nil {
				rank = n // sem a lista de prioridades, o id é o que há
			}
			all = append(all, Issue{
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
		if len(sr.Issues) == 0 {
			break
		}
		startAt += len(sr.Issues)
		token = sr.NextPageToken
		if tokenPaged {
			if token == "" {
				break
			}
		} else if startAt >= sr.Total {
			break
		}
	}
	return all, http.StatusOK, nil
}

func (c *Client) search(ctx context.Context, jql string, max int, cxField string, prios map[string]int) ([]Issue, error) {
	issues, code, err := c.doSearch(ctx, "/rest/api/2/search", jql, max, cxField, prios)
	// Jira Cloud removeu /rest/api/2/search; o substituto é /search/jql.
	if code == http.StatusGone || code == http.StatusNotFound {
		issues2, _, err2 := c.doSearch(ctx, "/rest/api/3/search/jql", jql, max, cxField, prios)
		if err2 != nil {
			// Os dois falharam: os dois erros importam para diagnosticar.
			return nil, fmt.Errorf("%w (fallback v3: %v)", err, err2)
		}
		return issues2, nil
	}
	return issues, err
}

// resolvePriorities mapeia o id da prioridade para a posição na ordem
// oficial (mais urgente primeiro), via /rest/api/2/priority — ids de
// prioridades customizadas (10000+) não têm ordem numérica confiável.
// Memoizado entre refreshes; em erro devolve nil (o chamador cai no id
// numérico) e tenta de novo no próximo refresh.
func (c *Client) resolvePriorities(ctx context.Context) map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.priosCached != nil {
		return c.priosCached
	}
	var ps []struct {
		ID string `json:"id"`
	}
	if _, err := c.get(ctx, "/rest/api/2/priority", nil, &ps); err != nil {
		return nil
	}
	m := make(map[string]int, len(ps))
	for i, p := range ps {
		m[p.ID] = i
	}
	c.priosCached = m
	return m
}

// resolveCXField devolve o id do campo de complexidade: o configurado, ou
// o primeiro campo cujo nome contenha "complex" (Complexidade, Complexity…).
// A descoberta (que baixa o catálogo de campos) é memoizada; só um erro de
// rede faz repetir no próximo refresh.
func (c *Client) resolveCXField(ctx context.Context) string {
	if c.cxField != "" {
		return c.cxField
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cxDone {
		return c.cxResolved
	}
	var fields []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if _, err := c.get(ctx, "/rest/api/2/field", nil, &fields); err != nil {
		return ""
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f.Name), "complex") {
			c.cxResolved = f.ID
			break
		}
	}
	c.cxDone = true
	return c.cxResolved
}

type IssueDetail struct {
	Key         string
	Summary     string
	Status      string
	Description string
	Comments    []Comment
}

type Comment struct {
	Author  string
	Body    string
	Created string // YYYY-MM-DD
}

// textOf extrai texto de um campo que pode ser string (Jira Server/DC) ou
// um objeto rich text/ADF (Jira Cloud); nesse caso devolve um aviso.
func textOf(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return "(conteúdo em rich text — abra no navegador com 'o')"
}

func (c *Client) doIssue(ctx context.Context, path string) (*IssueDetail, int, error) {
	q := url.Values{"fields": {"summary,status,description,comment"}}
	var resp struct {
		Key    string `json:"key"`
		Fields struct {
			Summary     string          `json:"summary"`
			Description json.RawMessage `json:"description"`
			Status      struct {
				Name string `json:"name"`
			} `json:"status"`
			Comment struct {
				Comments []struct {
					Author struct {
						DisplayName string `json:"displayName"`
					} `json:"author"`
					Body    json.RawMessage `json:"body"`
					Created string          `json:"created"`
				} `json:"comments"`
			} `json:"comment"`
		} `json:"fields"`
	}
	code, err := c.get(ctx, path, q, &resp)
	if err != nil {
		return nil, code, err
	}
	d := &IssueDetail{
		Key:         resp.Key,
		Summary:     resp.Fields.Summary,
		Status:      resp.Fields.Status.Name,
		Description: textOf(resp.Fields.Description),
	}
	for _, cm := range resp.Fields.Comment.Comments {
		d.Comments = append(d.Comments, Comment{
			Author:  cm.Author.DisplayName,
			Body:    textOf(cm.Body),
			Created: dateOnly(cm.Created),
		})
	}
	return d, code, nil
}

// IssueDetail busca descrição e comentários de uma issue, com o mesmo
// fallback de endpoint do search para o Jira Cloud.
func (c *Client) IssueDetail(ctx context.Context, key string) (*IssueDetail, error) {
	d, code, err := c.doIssue(ctx, "/rest/api/2/issue/"+key)
	if code == http.StatusGone || code == http.StatusNotFound {
		d2, _, err2 := c.doIssue(ctx, "/rest/api/3/issue/"+key)
		if err2 != nil {
			return nil, fmt.Errorf("%w (fallback v3: %v)", err, err2)
		}
		return d2, nil
	}
	return d, err
}

func (c *Client) Fetch(ctx context.Context) (*Summary, error) {
	if c.base == "" || c.token == "" {
		return nil, fmt.Errorf("Jira não configurado — defina url e token em %s", "~/.config/wmonit/config.toml")
	}
	cx := c.resolveCXField(ctx)
	prios := c.resolvePriorities(ctx)

	open, err := c.search(ctx, `assignee = currentUser() AND statusCategory != Done ORDER BY status, updated DESC`, 100, cx, prios)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	prevMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -1, 0)
	jql := fmt.Sprintf(`assignee = currentUser() AND statusCategory = Done AND resolved >= "%s" ORDER BY resolved DESC`,
		prevMonthStart.Format("2006-01-02"))
	resolved, err := c.search(ctx, jql, 500, cx, prios)
	if err != nil {
		return nil, err
	}

	return &Summary{Open: open, Resolved: resolved, CXField: cx}, nil
}
