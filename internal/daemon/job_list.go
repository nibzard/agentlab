package daemon

import (
	"net/http"
	"strconv"
	"strings"
)

type V1JobsResponse struct {
	Jobs       []V1JobResponse `json:"jobs"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func (api *ControlAPI) handleJobList(w http.ResponseWriter, r *http.Request) {
	if !api.authorize(w, r, permJobRead, nil, false) {
		return
	}
	q := r.URL.Query()
	limit := 50
	if value := q.Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 200 {
			writeError(w, 400, "limit must be between 1 and 200")
			return
		}
		limit = n
	}
	cursor := q.Get("after")
	result := V1JobsResponse{Jobs: make([]V1JobResponse, 0)}
	allowed := sandboxScopeFilter(r)
	for {
		jobs, err := api.store.ListJobsPage(r.Context(), cursor, strings.ToUpper(q.Get("state")), q.Get("profile"), q.Get("workspace"), 200)
		if err != nil {
			writeError(w, 500, "failed to list jobs", err)
			return
		}
		for _, job := range jobs {
			cursor = job.ID
			if allowed != nil && (job.SandboxVMID == nil || !allowed(*job.SandboxVMID)) {
				continue
			}
			if len(result.Jobs) == limit {
				result.NextCursor = result.Jobs[len(result.Jobs)-1].ID
				writeJSON(w, 200, result)
				return
			}
			result.Jobs = append(result.Jobs, jobToV1(job))
		}
		if len(jobs) < 200 {
			break
		}
	}
	writeJSON(w, 200, result)
}
