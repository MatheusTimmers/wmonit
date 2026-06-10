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
	IID        int    `json:"iid"`
	Title      string `json:"title"`
	WebURL     string `json:"web_url"`
	References struct {
		Full string `json:"full"`
	} `json:"references"`
	UpdatedAt time.Time  `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at"`
}

type user struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

type Summary struct {
	Username      string
	OpenMRs       []MR
	Merged        []MR // mergeados desde o início do mês anterior
	ReviewPending []MR
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

func (c *Client) get(path string, q url.Values, out any) error {
	u := c.base + "/api/v4" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitLab %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
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

	q := url.Values{"scope": {"created_by_me"}, "state": {"opened"}, "per_page": {"50"}}
	if err := c.get("/merge_requests", q, &s.OpenMRs); err != nil {
		return nil, err
	}

	q = url.Values{
		"scope":         {"created_by_me"},
		"state":         {"merged"},
		"updated_after": {prevMonthStart.Format(time.RFC3339)},
		"per_page":      {"100"},
	}
	if err := c.get("/merge_requests", q, &s.Merged); err != nil {
		return nil, err
	}

	q = url.Values{
		"scope":       {"all"},
		"state":       {"opened"},
		"reviewer_id": {fmt.Sprint(me.ID)},
		"per_page":    {"50"},
	}
	if err := c.get("/merge_requests", q, &s.ReviewPending); err != nil {
		return nil, err
	}

	return s, nil
}
