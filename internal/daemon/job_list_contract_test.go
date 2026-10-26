package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/models"
)

func TestJobListFiltersScopeBeforePaginationAcrossStoragePages(t *testing.T) {
	plane := newAuthzPlane(t)
	ctx := context.Background()
	workspace := "ws-1001"
	want := []string{}
	for i := 0; i < 430; i++ {
		vmid := 1002
		if i%101 == 0 {
			vmid = 1001
		}
		id := fmt.Sprintf("matrix-%04d", i)
		if vmid == 1001 {
			want = append(want, id)
		}
		if err := plane.api.store.CreateJob(ctx, models.Job{ID: id, RepoURL: "https://example.com/repo", Ref: "main", Profile: "matrix", WorkspaceID: &workspace, Status: models.JobCompleted, SandboxVMID: &vmid, CreatedAt: time.Now(), ResultJSON: `{"status":"COMPLETED","result":{"exit_code":0}}`}); err != nil {
			t.Fatal(err)
		}
	}
	// An unbound job with the same filters is not in any sandbox-scoped grant.
	if err := plane.api.store.CreateJob(ctx, models.Job{ID: "matrix-unbound", RepoURL: "repo", Ref: "main", Profile: "matrix", WorkspaceID: &workspace, Status: models.JobCompleted}); err != nil {
		t.Fatal(err)
	}
	identity := &auth.RequestIdentity{Token: &auth.Token{Claims: auth.TokenClaims{Commands: []string{"job.read"}, Scope: []string{"sandbox:1001"}}}}
	query := "/v1/jobs?limit=2&state=completed&profile=matrix&workspace=" + workspace
	got := []string{}
	cursor := ""
	for pageNumber := 0; pageNumber < 5; pageNumber++ {
		code, body := plane.serveAuthz(t, identity, "GET", query+"&after="+url.QueryEscape(cursor), "")
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		var page V1JobsResponse
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		for _, job := range page.Jobs {
			got = append(got, job.ID)
			if job.SandboxVMID == nil || *job.SandboxVMID != 1001 {
				t.Fatal("scope leak")
			}
			if job.Outcome == nil || job.Outcome.ExitCode == nil || *job.Outcome.ExitCode != 0 {
				t.Fatal("collection lost recorded outcome")
			}
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor || len(page.Jobs) != 2 || page.NextCursor != page.Jobs[1].ID {
			t.Fatal("unstable cursor")
		}
		cursor = page.NextCursor
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pagination: got %v want %v", got, want)
	}
	for _, filter := range []string{"state=FAILED", "profile=other", "workspace=other"} {
		code, body := plane.serveAuthz(t, identity, "GET", "/v1/jobs?"+filter, "")
		var page V1JobsResponse
		if code != 200 || json.Unmarshal(body, &page) != nil || len(page.Jobs) != 0 || page.Jobs == nil || page.NextCursor != "" {
			t.Fatalf("empty filter %s: %d %s", filter, code, body)
		}
	}
}
