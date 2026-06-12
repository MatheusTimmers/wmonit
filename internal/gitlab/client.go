package gitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(base, token string) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

type MR struct {
	IID          int    `json:"iid"`
	ProjectID    int    `json:"project_id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	WebURL       string `json:"web_url"`
	SourceBranch string `json:"source_branch"`
	References   struct {
		Full string `json:"full"`
	} `json:"references"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at"`
}

// Note é um comentário de um MR.
type Note struct {
	Body   string `json:"body"`
	System bool   `json:"system"`
	Author struct {
		Name string `json:"name"`
	} `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

type user struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

type Summary struct {
	Username      string
	OpenMRs       []MR
	Merged        []MR // mergeados desde o início do mês anterior
	Closed        []MR // fechados sem merge desde o início do mês anterior
	ReviewPending []MR
}

// Mine junta os MRs do usuário (abertos, mergeados e fechados) sem
// duplicar — a base para métricas por data de criação, onde o dia
// trabalhado é o da abertura.
func (s *Summary) Mine() []MR {
	seen := map[string]bool{}
	var out []MR
	for _, mr := range append(append(append([]MR{}, s.OpenMRs...), s.Merged...), s.Closed...) {
		id := fmt.Sprintf("%d-%d", mr.ProjectID, mr.IID)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, mr)
	}
	return out
}

func (mr MR) MergedTime() time.Time {
	if mr.MergedAt != nil {
		return *mr.MergedAt
	}
	return mr.UpdatedAt
}

// Títulos de MR seguem o padrão "breve descrição #TAG-JIRA [type]".
var (
	jiraKeyRe = regexp.MustCompile(`#([A-Za-z][A-Za-z0-9]*-\d+)`)
	kindRe    = regexp.MustCompile(`\[([^\]]+)\]`)
)

// JiraKey extrai a chave da issue do título ("#GAR-123" → "GAR-123").
func (mr MR) JiraKey() string {
	m := jiraKeyRe.FindStringSubmatch(mr.Title)
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1])
}

// Kind extrai o tipo do MR ("[feature]" → "feature").
func (mr MR) Kind() string {
	ms := kindRe.FindAllStringSubmatch(mr.Title, -1)
	if len(ms) == 0 {
		return ""
	}
	return strings.ToLower(ms[len(ms)-1][1])
}

// ShortTitle devolve o título sem a chave Jira e o tipo.
func (mr MR) ShortTitle() string {
	t := jiraKeyRe.ReplaceAllString(mr.Title, "")
	t = kindRe.ReplaceAllString(t, "")
	return strings.Join(strings.Fields(t), " ")
}

// getResp faz o GET e devolve a resposta com o body aberto (status já
// validado) — a paginação precisa dos headers além do corpo.
func (c *Client) getResp(path string, q url.Values) (*http.Response, error) {
	u := c.base + "/api/v4" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitLab %s: HTTP %d", path, resp.StatusCode)
	}
	return resp, nil
}

func (c *Client) get(path string, q url.Values, out any) error {
	resp, err := c.getResp(path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// maxPages limita a paginação: 5 páginas de 100 dão folga para os dois
// meses de MRs que o app olha, sem deixar uma consulta varrer tudo.
const maxPages = 5

// listMRs busca /merge_requests seguindo a paginação (X-Next-Page) — sem
// isso as métricas subcontam assim que um período passa de uma página.
func (c *Client) listMRs(q url.Values) ([]MR, error) {
	q.Set("per_page", "100")
	var all []MR
	page := "1"
	for i := 0; i < maxPages; i++ {
		q.Set("page", page)
		resp, err := c.getResp("/merge_requests", q)
		if err != nil {
			return nil, err
		}
		var batch []MR
		err = json.NewDecoder(resp.Body).Decode(&batch)
		next := resp.Header.Get("X-Next-Page")
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if next == "" {
			break
		}
		page = next
	}
	return all, nil
}

// MRNotes devolve os comentários (notas) de um MR, em ordem cronológica.
func (c *Client) MRNotes(projectID, iid int) ([]Note, error) {
	q := url.Values{"sort": {"asc"}, "order_by": {"created_at"}, "per_page": {"50"}}
	var notes []Note
	path := fmt.Sprintf("/projects/%d/merge_requests/%d/notes", projectID, iid)
	if err := c.get(path, q, &notes); err != nil {
		return nil, err
	}
	return notes, nil
}

func (c *Client) Fetch() (*Summary, error) {
	if c.base == "" || c.token == "" {
		return nil, fmt.Errorf("GitLab não configurado — defina url e token em %s", "~/.config/wmonit/config.toml")
	}
	var me user
	if err := c.get("/user", nil, &me); err != nil {
		return nil, err
	}

	s := &Summary{Username: me.Username}
	now := time.Now()
	prevMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -1, 0)

	var err error
	if s.OpenMRs, err = c.listMRs(url.Values{
		"scope": {"created_by_me"}, "state": {"opened"},
	}); err != nil {
		return nil, err
	}
	if s.Merged, err = c.listMRs(url.Values{
		"scope":         {"created_by_me"},
		"state":         {"merged"},
		"updated_after": {prevMonthStart.Format(time.RFC3339)},
	}); err != nil {
		return nil, err
	}
	if s.Closed, err = c.listMRs(url.Values{
		"scope":         {"created_by_me"},
		"state":         {"closed"},
		"updated_after": {prevMonthStart.Format(time.RFC3339)},
	}); err != nil {
		return nil, err
	}
	if s.ReviewPending, err = c.listMRs(url.Values{
		"scope":       {"all"},
		"state":       {"opened"},
		"reviewer_id": {fmt.Sprint(me.ID)},
	}); err != nil {
		return nil, err
	}

	return s, nil
}
