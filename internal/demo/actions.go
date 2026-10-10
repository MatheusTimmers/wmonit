package demo

import (
	"context"
	"time"

	"github.com/timmers/wmonit/internal/gitlab"
)

func (GitLabSource) MRStatus(ctx context.Context, projectID, iid int) (*gitlab.MRStatus, error) {
	st := &gitlab.MRStatus{
		State:                       "opened",
		DetailedMergeStatus:         "mergeable",
		BlockingDiscussionsResolved: true,
		HeadPipeline:                &gitlab.Pipeline{ID: iid * 10, Status: "success"},
		At:                          time.Now(),
	}
	switch iid {
	case 412:
		st.HeadPipeline.Status = "failed"
		st.FailedJobs = []string{"test-integration"}
		st.BlockingDiscussionsResolved = false
		st.DetailedMergeStatus = "ci_must_pass"
	case 9470:
		st.ApprovalsLeft = 1
		st.DetailedMergeStatus = "not_approved"
	case 9460:
		st.State = "merged"
	}
	return st, nil
}

func (GitLabSource) CreateMR(ctx context.Context, n gitlab.NewMR) (*gitlab.MR, error) {
	m := mr(999, 1, n.Title, n.SourceBranch, n.Project+"!999", time.Now(), time.Now(), nil)
	return &m, nil
}

func (JiraSource) TransitionTo(ctx context.Context, key, target string) (string, error) {
	return target, nil
}
