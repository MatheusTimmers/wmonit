package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Pipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
}

type MRStatus struct {
	State                       string    `json:"state"`
	Draft                       bool      `json:"draft"`
	HasConflicts                bool      `json:"has_conflicts"`
	DetailedMergeStatus         string    `json:"detailed_merge_status"`
	BlockingDiscussionsResolved bool      `json:"blocking_discussions_resolved"`
	HeadPipeline                *Pipeline `json:"head_pipeline"`
	WebURL                      string    `json:"web_url"`
	ApprovalsLeft               int       `json:"-"`
	FailedJobs                  []string  `json:"-"`
	At                          time.Time `json:"-"`
}

func (s *MRStatus) CI() string {
	if s == nil || s.HeadPipeline == nil {
		return ""
	}
	return s.HeadPipeline.Status
}

func (s *MRStatus) Problems() []string {
	if s == nil {
		return nil
	}
	var out []string
	switch s.State {
	case "merged":
		return []string{"MR mergeado"}
	case "closed":
		return []string{"MR fechado"}
	}
	switch s.CI() {
	case "failed":
		p := "CI falhou"
		if len(s.FailedJobs) > 0 {
			p += " (" + strings.Join(s.FailedJobs, ", ") + ")"
		}
		out = append(out, p)
	case "running", "pending", "created", "preparing", "waiting_for_resource":
		out = append(out, "CI rodando")
	case "canceled":
		out = append(out, "CI cancelado")
	}
	if s.HasConflicts {
		out = append(out, "conflito com a branch de destino")
	}
	if !s.BlockingDiscussionsResolved {
		out = append(out, "threads abertas")
	}
	if s.ApprovalsLeft > 0 {
		out = append(out, fmt.Sprintf("faltam %d aprovação(ões)", s.ApprovalsLeft))
	}
	if s.Draft {
		out = append(out, "draft")
	}
	switch s.DetailedMergeStatus {
	case "need_rebase":
		out = append(out, "precisa de rebase")
	case "requested_changes":
		out = append(out, "mudanças solicitadas")
	}
	return out
}

func (s *MRStatus) Ready() bool {
	return s != nil && s.State == "opened" && s.DetailedMergeStatus == "mergeable" && len(s.Problems()) == 0
}

func (c *Client) MRStatus(ctx context.Context, projectID, iid int) (*MRStatus, error) {
	var st MRStatus
	base := fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, iid)
	if err := c.get(ctx, base, nil, &st); err != nil {
		return nil, err
	}
	var ap struct {
		ApprovalsLeft int `json:"approvals_left"`
	}
	if c.get(ctx, base+"/approvals", nil, &ap) == nil {
		st.ApprovalsLeft = ap.ApprovalsLeft
	}
	if st.CI() == "failed" {
		var jobs []struct {
			Name  string `json:"name"`
			Stage string `json:"stage"`
		}
		q := url.Values{"scope[]": {"failed"}, "per_page": {"20"}}
		if c.get(ctx, fmt.Sprintf("/projects/%d/pipelines/%d/jobs", projectID, st.HeadPipeline.ID), q, &jobs) == nil {
			for _, j := range jobs {
				st.FailedJobs = append(st.FailedJobs, j.Name)
			}
		}
	}
	st.At = time.Now()
	return &st, nil
}

type NewMR struct {
	Project      string
	SourceBranch string
	TargetBranch string
	Title        string
	Description  string
	Draft        bool
}

func (c *Client) CreateMR(ctx context.Context, n NewMR) (*MR, error) {
	if c.base == "" || c.token == "" {
		return nil, fmt.Errorf("GitLab não configurado")
	}
	title := n.Title
	if n.Draft && !strings.HasPrefix(strings.ToLower(title), "draft:") {
		title = "Draft: " + title
	}
	body := map[string]any{
		"source_branch":        n.SourceBranch,
		"target_branch":        n.TargetBranch,
		"title":                title,
		"description":          n.Description,
		"remove_source_branch": true,
	}
	if n.TargetBranch == "" {
		var p struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := c.get(ctx, "/projects/"+url.PathEscape(n.Project), nil, &p); err != nil {
			return nil, err
		}
		body["target_branch"] = p.DefaultBranch
	}
	var mr MR
	if err := c.post(ctx, "/projects/"+url.PathEscape(n.Project)+"/merge_requests", body, &mr); err != nil {
		return nil, err
	}
	return &mr, nil
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/api/v4"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("GitLab %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func ProjectPath(remote string) string {
	r := strings.TrimSpace(remote)
	r = strings.TrimSuffix(r, ".git")
	if u, err := url.Parse(r); err == nil && u.Host != "" {
		return strings.Trim(u.Path, "/")
	}
	if i := strings.Index(r, ":"); i >= 0 && !strings.Contains(r[:i], "/") {
		return strings.Trim(r[i+1:], "/")
	}
	return ""
}
