package daemon

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentlab/agentlab/internal/auth"
	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/proxmox"
	tb "github.com/agentlab/agentlab/internal/templatebuild"
)

func (api *ControlAPI) registerTemplates(mux *http.ServeMux) {
	for _, path := range []string{"/v1/templates", "/v1/templates/", "/v1/template-builds", "/v1/template-builds/"} {
		mux.HandleFunc(path, api.handleTemplates)
	}
}
func templateError(w http.ResponseWriter, status int, code, message string) {
	resp := daemonErrorResponse(status, message)
	resp.Code = code
	writeJSON(w, status, resp)
}
func templatePagination(r *http.Request) (offset, limit int, ok bool) {
	limit = 100
	ok = true
	for k, v := range map[string]*int{"offset": &offset, "limit": &limit} {
		if raw := r.URL.Query().Get(k); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 0 || (k == "limit" && (n < 1 || n > 500)) {
				return 0, 0, false
			}
			*v = n
		}
	}
	return
}
func (api *ControlAPI) handleTemplates(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	parts := strings.Split(path, "/")
	perm := "template.read"
	expected := "GET"
	if path == "templates/validate-plan" {
		perm = "template.validate"
		expected = "POST"
	} else if path == "template-builds" && r.Method == "POST" {
		perm = "template.create"
		expected = "POST"
	} else if len(parts) == 3 && parts[2] == "cancel" {
		perm = "template.cancel"
		expected = "POST"
	} else if len(parts) == 3 && parts[2] == "activate" {
		perm = "template.activate"
		expected = "POST"
	}
	if !api.authorize(w, r, perm, nil, true) {
		return
	}
	if r.Method != expected {
		writeMethodNotAllowed(w, []string{expected})
		return
	}
	m := api.templateBuilds
	if m == nil {
		templateError(w, 503, "template_build_unsupported", "template builds unavailable; upgrade or configure a local Proxmox daemon")
		return
	}
	ctx := r.Context()
	switch path {
	case "templates/capabilities":
		writeJSON(w, 200, m.Capabilities(ctx))
		return
	case "templates/validate-plan":
		var p tb.Plan
		if e := decodeJSON(w, r, &p); e != nil {
			writeJSONDecodeError(w, e)
			return
		}
		v := m.Validate(ctx, p)
		status := 200
		if !v.OK {
			status = 422
		}
		writeJSON(w, status, v)
		return
	case "template-builds":
		if r.Method == "POST" {
			var p tb.Plan
			if e := decodeJSON(w, r, &p); e != nil {
				writeJSONDecodeError(w, e)
				return
			}
			if p.RequestID == "" || r.Header.Get("Idempotency-Key") != p.RequestID {
				templateError(w, 400, "template_invalid_plan", "Idempotency-Key must match request_id UUID")
				return
			}
			caller := "local"
			if id := auth.FromContext(ctx); id != nil {
				caller = id.Fingerprint
				if caller == "" {
					caller = id.Method
				}
				if caller == "" {
					caller = "authenticated"
				}
			}
			b, e := m.Submit(ctx, p, caller)
			if e != nil {
				api.templateFailure(w, e)
				return
			}
			w.Header().Set("Location", "/v1/template-builds/"+b.ID)
			writeJSON(w, 202, b)
			return
		}
		offset, limit, ok := templatePagination(r)
		if !ok {
			writeError(w, 400, "invalid pagination")
			return
		}
		state := r.URL.Query().Get("state")
		if state != "" && state != "QUEUED" && state != "RUNNING" && state != "SUCCEEDED" && state != "FAILED" && state != "CANCELLED" {
			writeError(w, 400, "invalid state")
			return
		}
		bs, e := api.store.TemplateBuilds(ctx, state, offset, limit)
		if e != nil {
			api.templateFailure(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"builds": bs, "next_offset": offset + len(bs)})
		return
	case "templates":
		offset, limit, ok := templatePagination(r)
		if !ok {
			writeError(w, 400, "invalid pagination")
			return
		}
		ms, e := api.store.TemplateManifests(ctx, offset, limit)
		if e != nil {
			api.templateFailure(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"templates": ms, "next_offset": offset + len(ms)})
		return
	}
	if parts[0] == "template-builds" && len(parts) >= 2 && len(parts) <= 3 {
		b, e := api.store.TemplateBuild(ctx, parts[1])
		if e != nil {
			api.templateFailure(w, e)
			return
		}
		if len(parts) == 2 {
			writeJSON(w, 200, b)
			return
		}
		switch parts[2] {
		case "cancel":
			b, e = m.Cancel(ctx, b.ID)
			if e != nil {
				api.templateFailure(w, e)
				return
			}
			writeJSON(w, 200, b)
			return
		case "logs":
			after := int64(0)
			if raw := r.URL.Query().Get("after"); raw != "" {
				after, e = strconv.ParseInt(raw, 10, 64)
				if e != nil || after < 0 {
					writeError(w, 400, "invalid log cursor")
					return
				}
			}
			_, limit, ok := templatePagination(r)
			if !ok {
				writeError(w, 400, "invalid log limit")
				return
			}
			ls, e := api.store.TemplateLogs(ctx, b.ID, after, limit)
			if e != nil {
				api.templateFailure(w, e)
				return
			}
			writeJSON(w, 200, ls)
			return
		}
	}
	if parts[0] == "templates" && len(parts) >= 2 && len(parts) <= 3 {
		vmid, e := strconv.Atoi(parts[1])
		if e != nil || vmid <= 0 {
			writeError(w, 400, "invalid template vmid")
			return
		}
		manifest, e := api.store.TemplateManifest(ctx, vmid)
		if e != nil {
			api.templateFailure(w, e)
			return
		}
		if len(parts) == 3 && parts[2] == "activate" {
			api.activateTemplate(w, r, manifest)
			return
		}
		if len(parts) == 2 {
			if api.backend == nil {
				templateError(w, 503, "template_build_unsupported", "Proxmox backend unavailable")
				return
			}
			config, e := api.backend.VMConfig(ctx, proxmox.VMID(vmid))
			if errors.Is(e, proxmox.ErrVMNotFound) {
				writeJSON(w, 200, map[string]any{"manifest": manifest, "exists": false, "drift": true})
				return
			}
			if e != nil {
				templateError(w, 503, "template_worker_unavailable", "cannot inspect template on the configured node; check Proxmox backend connectivity")
				return
			}
			drift := tb.VerifyDrift(config, manifest)
			writeJSON(w, 200, map[string]any{"manifest": manifest, "exists": true, "drift": drift != nil})
			return
		}
	}
	writeError(w, 404, "template resource not found")
}
func (api *ControlAPI) templateFailure(w http.ResponseWriter, e error) {
	var p *templatePlanError
	switch {
	case errors.As(e, &p):
		status := 422
		for _, i := range p.Validation.Errors {
			if i.Code == "template_vmid_conflict" {
				status = 409
			}
		}
		writeJSON(w, status, p.Validation)
	case errors.Is(e, sql.ErrNoRows):
		templateError(w, 404, "template_not_found", "template or build not found")
	case errors.Is(e, db.ErrTemplateConflict), errors.Is(e, db.ErrTemplateIdempotency):
		templateError(w, 409, "template_vmid_conflict", e.Error())
	case errors.Is(e, db.ErrTemplateQueueFull):
		w.Header().Set("Retry-After", "10")
		templateError(w, 503, "template_worker_unavailable", "build queue is full; retry later")
	default:
		templateError(w, 503, "template_worker_unavailable", "template operation unavailable; inspect daemon diagnostics")
	}
}
