package daemon

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	doctorBundleVersion     = 1
	doctorBundleNamePrefix  = "agentlab-doctor"
	doctorBundleContentType = "application/gzip"
)

type doctorMeta struct {
	Version int             `json:"version"`
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Related *doctorRelated  `json:"related,omitempty"`
	Notes   []string        `json:"notes,omitempty"`
	Errors  []doctorSection `json:"errors,omitempty"`
}

type doctorRelated struct {
	SandboxVMID *int    `json:"sandbox_vmid,omitempty"`
	JobID       *string `json:"job_id,omitempty"`
	SessionID   *string `json:"session_id,omitempty"`
	WorkspaceID *string `json:"workspace_id,omitempty"`
}

type doctorSection struct {
	Section string `json:"section"`
	Error   string `json:"error"`
}

type doctorBundleInput struct {
	Meta          doctorMeta
	Sandbox       *V1SandboxResponse
	Job           *V1JobResponse
	Session       *V1SessionResponse
	Workspace     *V1WorkspaceResponse
	SandboxEvents *V1EventsResponse
	JobEvents     *V1EventsResponse
	Artifacts     *V1ArtifactsResponse
	Proxmox       *doctorProxmoxInfo
}

type doctorFile struct {
	name string
	data []byte
}

type doctorProxmoxInfo struct {
	VMID        int                  `json:"vmid,omitempty"`
	Status      string               `json:"status,omitempty"`
	StatusError string               `json:"status_error,omitempty"`
	Config      *doctorOrderedConfig `json:"config,omitempty"`
	ConfigError string               `json:"config_error,omitempty"`
}

type doctorOrderedConfig struct {
	entries []doctorKV
}

type doctorKV struct {
	Key   string
	Value string
}

func (api *ControlAPI) handleSandboxDoctor(w http.ResponseWriter, r *http.Request, vmid int) {
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox store unavailable")
		return
	}
	ctx := r.Context()
	sandbox, err := api.store.GetSandbox(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load sandbox")
		return
	}

	meta := doctorMeta{
		Version: doctorBundleVersion,
		Kind:    "sandbox",
		ID:      fmt.Sprintf("%d", sandbox.VMID),
	}
	related := doctorRelated{
		SandboxVMID: &sandbox.VMID,
	}
	input := doctorBundleInput{Meta: meta}
	sandboxV1 := api.sandboxToV1(sandbox)
	input.Sandbox = &sandboxV1

	if sandbox.WorkspaceID != nil && strings.TrimSpace(*sandbox.WorkspaceID) != "" {
		workspace, err := api.store.GetWorkspace(ctx, *sandbox.WorkspaceID)
		if err == nil {
			workspaceV1 := workspaceToV1(workspace)
			input.Workspace = &workspaceV1
			related.WorkspaceID = &workspace.ID
		} else if !errors.Is(err, sql.ErrNoRows) {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "workspace", Error: err.Error()})
		}
	}

	job, err := api.store.GetJobBySandboxVMID(ctx, vmid)
	if err == nil {
		jobV1 := jobToV1(job)
		input.Job = &jobV1
		related.JobID = &job.ID
		if job.SandboxVMID != nil {
			related.SandboxVMID = job.SandboxVMID
		}
		if job.SessionID != nil && strings.TrimSpace(*job.SessionID) != "" {
			related.SessionID = job.SessionID
			session, err := api.store.GetSession(ctx, *job.SessionID)
			if err == nil {
				sessionV1 := api.sessionToV1(session)
				input.Session = &sessionV1
				if strings.TrimSpace(session.WorkspaceID) != "" {
					value := session.WorkspaceID
					related.WorkspaceID = &value
					if input.Workspace == nil {
						if workspace, err := api.store.GetWorkspace(ctx, session.WorkspaceID); err == nil {
							workspaceV1 := workspaceToV1(workspace)
							input.Workspace = &workspaceV1
						} else if !errors.Is(err, sql.ErrNoRows) {
							input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "workspace", Error: err.Error()})
						}
					}
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "session", Error: err.Error()})
			}
		}
		if job.WorkspaceID != nil && strings.TrimSpace(*job.WorkspaceID) != "" {
			related.WorkspaceID = job.WorkspaceID
			if input.Workspace == nil {
				if workspace, err := api.store.GetWorkspace(ctx, *job.WorkspaceID); err == nil {
					workspaceV1 := workspaceToV1(workspace)
					input.Workspace = &workspaceV1
				} else if !errors.Is(err, sql.ErrNoRows) {
					input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "workspace", Error: err.Error()})
				}
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "job", Error: err.Error()})
	}

	if events, err := api.store.ListEventsBySandboxTail(ctx, vmid, defaultEventsLimit); err == nil {
		input.SandboxEvents = eventsToV1Response(events)
	} else {
		input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "events.sandbox", Error: err.Error()})
	}
	if input.Job != nil {
		if events, err := api.store.ListEventsByJobTail(ctx, input.Job.ID, defaultEventsLimit); err == nil {
			input.JobEvents = eventsToV1Response(events)
		} else {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "events.job", Error: err.Error()})
		}
		if artifacts, err := api.store.ListArtifactsByJob(ctx, input.Job.ID); err == nil {
			input.Artifacts = artifactsToV1Response(input.Job.ID, artifacts)
		} else {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "artifacts", Error: err.Error()})
		}
	}

	input.Proxmox = api.proxmoxDoctorInfo(ctx, vmid)
	input.Meta.Related = relatedIfPresent(related)

	filename := fmt.Sprintf("%s-sandbox-%d.tar.gz", doctorBundleNamePrefix, vmid)
	api.writeDoctorBundleResponse(w, filename, input)
}

func (api *ControlAPI) handleJobDoctor(w http.ResponseWriter, r *http.Request, jobID string) {
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "job store unavailable")
		return
	}
	ctx := r.Context()
	job, err := api.store.GetJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load job")
		return
	}

	meta := doctorMeta{
		Version: doctorBundleVersion,
		Kind:    "job",
		ID:      job.ID,
	}
	related := doctorRelated{
		JobID: &job.ID,
	}
	input := doctorBundleInput{Meta: meta}
	jobV1 := jobToV1(job)
	input.Job = &jobV1

	if job.SandboxVMID != nil && *job.SandboxVMID > 0 {
		related.SandboxVMID = job.SandboxVMID
		sandbox, err := api.store.GetSandbox(ctx, *job.SandboxVMID)
		if err == nil {
			sandboxV1 := api.sandboxToV1(sandbox)
			input.Sandbox = &sandboxV1
		} else if !errors.Is(err, sql.ErrNoRows) {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "sandbox", Error: err.Error()})
		}
	}
	if job.SessionID != nil && strings.TrimSpace(*job.SessionID) != "" {
		related.SessionID = job.SessionID
		session, err := api.store.GetSession(ctx, *job.SessionID)
		if err == nil {
			sessionV1 := api.sessionToV1(session)
			input.Session = &sessionV1
			if strings.TrimSpace(session.WorkspaceID) != "" {
				value := session.WorkspaceID
				related.WorkspaceID = &value
				if input.Workspace == nil {
					if workspace, err := api.store.GetWorkspace(ctx, session.WorkspaceID); err == nil {
						workspaceV1 := workspaceToV1(workspace)
						input.Workspace = &workspaceV1
					} else if !errors.Is(err, sql.ErrNoRows) {
						input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "workspace", Error: err.Error()})
					}
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "session", Error: err.Error()})
		}
	}
	if job.WorkspaceID != nil && strings.TrimSpace(*job.WorkspaceID) != "" {
		related.WorkspaceID = job.WorkspaceID
		if input.Workspace == nil {
			if workspace, err := api.store.GetWorkspace(ctx, *job.WorkspaceID); err == nil {
				workspaceV1 := workspaceToV1(workspace)
				input.Workspace = &workspaceV1
			} else if !errors.Is(err, sql.ErrNoRows) {
				input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "workspace", Error: err.Error()})
			}
		}
	}

	if events, err := api.store.ListEventsByJobTail(ctx, job.ID, defaultEventsLimit); err == nil {
		input.JobEvents = eventsToV1Response(events)
	} else {
		input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "events.job", Error: err.Error()})
	}
	if input.Sandbox != nil {
		if events, err := api.store.ListEventsBySandboxTail(ctx, input.Sandbox.VMID, defaultEventsLimit); err == nil {
			input.SandboxEvents = eventsToV1Response(events)
		} else {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "events.sandbox", Error: err.Error()})
		}
	}
	if artifacts, err := api.store.ListArtifactsByJob(ctx, job.ID); err == nil {
		input.Artifacts = artifactsToV1Response(job.ID, artifacts)
	} else {
		input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "artifacts", Error: err.Error()})
	}

	if job.SandboxVMID != nil && *job.SandboxVMID > 0 {
		input.Proxmox = api.proxmoxDoctorInfo(ctx, *job.SandboxVMID)
	} else {
		input.Proxmox = &doctorProxmoxInfo{StatusError: "sandbox_vmid is not set", ConfigError: "sandbox_vmid is not set"}
	}
	input.Meta.Related = relatedIfPresent(related)

	filename := fmt.Sprintf("%s-job-%s.tar.gz", doctorBundleNamePrefix, job.ID)
	api.writeDoctorBundleResponse(w, filename, input)
}

func (api *ControlAPI) handleSessionDoctor(w http.ResponseWriter, r *http.Request, id string) {
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "session store unavailable")
		return
	}
	ctx := r.Context()
	session, err := api.resolveSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	meta := doctorMeta{
		Version: doctorBundleVersion,
		Kind:    "session",
		ID:      session.ID,
	}
	related := doctorRelated{
		SessionID: &session.ID,
	}
	input := doctorBundleInput{Meta: meta}
	sessionV1 := api.sessionToV1(session)
	input.Session = &sessionV1
	if strings.TrimSpace(session.WorkspaceID) != "" {
		value := session.WorkspaceID
		related.WorkspaceID = &value
		if workspace, err := api.store.GetWorkspace(ctx, session.WorkspaceID); err == nil {
			workspaceV1 := workspaceToV1(workspace)
			input.Workspace = &workspaceV1
		} else if !errors.Is(err, sql.ErrNoRows) {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "workspace", Error: err.Error()})
		}
	}
	if session.CurrentVMID != nil && *session.CurrentVMID > 0 {
		related.SandboxVMID = session.CurrentVMID
		sandbox, err := api.store.GetSandbox(ctx, *session.CurrentVMID)
		if err == nil {
			sandboxV1 := api.sandboxToV1(sandbox)
			input.Sandbox = &sandboxV1
		} else if !errors.Is(err, sql.ErrNoRows) {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "sandbox", Error: err.Error()})
		}
		if events, err := api.store.ListEventsBySandboxTail(ctx, *session.CurrentVMID, defaultEventsLimit); err == nil {
			input.SandboxEvents = eventsToV1Response(events)
		} else {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "events.sandbox", Error: err.Error()})
		}
		if job, err := api.store.GetJobBySandboxVMID(ctx, *session.CurrentVMID); err == nil {
			jobV1 := jobToV1(job)
			input.Job = &jobV1
			related.JobID = &job.ID
			if job.SessionID != nil {
				related.SessionID = job.SessionID
			}
			if events, err := api.store.ListEventsByJobTail(ctx, job.ID, defaultEventsLimit); err == nil {
				input.JobEvents = eventsToV1Response(events)
			} else {
				input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "events.job", Error: err.Error()})
			}
			if artifacts, err := api.store.ListArtifactsByJob(ctx, job.ID); err == nil {
				input.Artifacts = artifactsToV1Response(job.ID, artifacts)
			} else {
				input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "artifacts", Error: err.Error()})
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			input.Meta.Errors = append(input.Meta.Errors, doctorSection{Section: "job", Error: err.Error()})
		}
		input.Proxmox = api.proxmoxDoctorInfo(ctx, *session.CurrentVMID)
	} else {
		input.Proxmox = &doctorProxmoxInfo{StatusError: "session has no current sandbox", ConfigError: "session has no current sandbox"}
	}

	input.Meta.Related = relatedIfPresent(related)

	filename := fmt.Sprintf("%s-session-%s.tar.gz", doctorBundleNamePrefix, session.ID)
	api.writeDoctorBundleResponse(w, filename, input)
}

func (api *ControlAPI) writeDoctorBundleResponse(w http.ResponseWriter, filename string, input doctorBundleInput) {
	redactor := api.doctorRedactor()
	files, err := buildDoctorFiles(redactor, input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build doctor bundle")
		return
	}
	if len(files) == 0 {
		writeError(w, http.StatusInternalServerError, "doctor bundle is empty")
		return
	}
	w.Header().Set("Content-Type", doctorBundleContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if err := writeDoctorBundle(w, files); err != nil {
		if api.logger != nil {
			api.logger.Printf("doctor bundle write failed: %v", err)
		}
	}
}

func (api *ControlAPI) doctorRedactor() *Redactor {
	if api == nil || api.redactor == nil {
		return NewRedactor(nil)
	}
	return api.redactor
}

func (api *ControlAPI) proxmoxDoctorInfo(ctx context.Context, vmid int) *doctorProxmoxInfo {
	info := &doctorProxmoxInfo{VMID: vmid}
	if vmid <= 0 {
		info.StatusError = "vmid must be positive"
		info.ConfigError = "vmid must be positive"
		return info
	}
	if api == nil || api.backend == nil {
		info.StatusError = "proxmox backend unavailable"
		info.ConfigError = "proxmox backend unavailable"
		return info
	}
	status, err := api.backend.Status(ctx, proxmox.VMID(vmid))
	if err != nil {
		info.StatusError = err.Error()
	} else {
		info.Status = string(status)
	}
	config, err := api.backend.VMConfig(ctx, proxmox.VMID(vmid))
	if err != nil {
		info.ConfigError = err.Error()
	} else {
		info.Config = newDoctorOrderedConfig(config)
	}
	return info
}

func eventsToV1Response(events []db.Event) *V1EventsResponse {
	resp := &V1EventsResponse{Events: make([]V1Event, 0, len(events))}
	var lastID int64
	for _, ev := range events {
		if ev.ID > lastID {
			lastID = ev.ID
		}
		resp.Events = append(resp.Events, eventToV1(ev))
	}
	if lastID > 0 {
		resp.LastID = lastID
	}
	return resp
}

func artifactsToV1Response(jobID string, artifacts []db.Artifact) *V1ArtifactsResponse {
	resp := &V1ArtifactsResponse{JobID: jobID, Artifacts: make([]V1Artifact, 0, len(artifacts))}
	for _, artifact := range artifacts {
		resp.Artifacts = append(resp.Artifacts, artifactToV1(artifact))
	}
	return resp
}

func relatedIfPresent(related doctorRelated) *doctorRelated {
	if related.SandboxVMID == nil && related.JobID == nil && related.SessionID == nil && related.WorkspaceID == nil {
		return nil
	}
	return &related
}

func buildDoctorFiles(redactor *Redactor, input doctorBundleInput) ([]doctorFile, error) {
	var files []doctorFile
	addJSON := func(name string, value any) error {
		payload, err := marshalDoctorJSON(redactor, value)
		if err != nil {
			return err
		}
		files = append(files, doctorFile{name: name, data: payload})
		return nil
	}
	if err := addJSON("meta.json", input.Meta); err != nil {
		return nil, err
	}
	if input.Sandbox != nil {
		if err := addJSON("records/sandbox.json", input.Sandbox); err != nil {
			return nil, err
		}
	}
	if input.Job != nil {
		if err := addJSON("records/job.json", input.Job); err != nil {
			return nil, err
		}
	}
	if input.Session != nil {
		if err := addJSON("records/session.json", input.Session); err != nil {
			return nil, err
		}
	}
	if input.Workspace != nil {
		if err := addJSON("records/workspace.json", input.Workspace); err != nil {
			return nil, err
		}
	}
	if input.SandboxEvents != nil {
		if err := addJSON("events/sandbox.json", input.SandboxEvents); err != nil {
			return nil, err
		}
	}
	if input.JobEvents != nil {
		if err := addJSON("events/job.json", input.JobEvents); err != nil {
			return nil, err
		}
	}
	if input.Artifacts != nil {
		if err := addJSON("artifacts/job.json", input.Artifacts); err != nil {
			return nil, err
		}
	}
	if input.Proxmox != nil {
		if err := addJSON("proxmox.json", input.Proxmox); err != nil {
			return nil, err
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].name < files[j].name
	})
	return files, nil
}

func marshalDoctorJSON(redactor *Redactor, value any) ([]byte, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	if redactor != nil {
		payload = []byte(redactor.Redact(string(payload)))
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		payload = append(payload, '\n')
	}
	return payload, nil
}

func writeDoctorBundle(w io.Writer, files []doctorFile) error {
	gz := gzip.NewWriter(w)
	gz.Header.Name = doctorBundleNamePrefix
	gz.Header.ModTime = time.Unix(0, 0)
	gz.Header.OS = 255
	writer := tar.NewWriter(gz)
	for _, file := range files {
		hdr := &tar.Header{
			Name:       file.name,
			Mode:       0o644,
			Size:       int64(len(file.data)),
			ModTime:    time.Unix(0, 0),
			AccessTime: time.Unix(0, 0),
			ChangeTime: time.Unix(0, 0),
			Typeflag:   tar.TypeReg,
			Uid:        0,
			Gid:        0,
			Uname:      "",
			Gname:      "",
		}
		if err := writer.WriteHeader(hdr); err != nil {
			_ = writer.Close()
			_ = gz.Close()
			return err
		}
		if _, err := writer.Write(file.data); err != nil {
			_ = writer.Close()
			_ = gz.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return nil
}

func newDoctorOrderedConfig(config map[string]string) *doctorOrderedConfig {
	if len(config) == 0 {
		return nil
	}
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]doctorKV, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, doctorKV{Key: key, Value: config[key]})
	}
	return &doctorOrderedConfig{entries: entries}
}

func (c doctorOrderedConfig) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, entry := range c.entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, err := json.Marshal(entry.Key)
		if err != nil {
			return nil, err
		}
		valBytes, err := json.Marshal(entry.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		buf.Write(valBytes)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
