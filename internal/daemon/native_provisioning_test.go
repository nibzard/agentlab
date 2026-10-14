package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/require"
)

func TestNativeSSHKeysRejectsJobsBeforeProvisioning(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profiles := map[string]models.Profile{"default": {Name: "default", TemplateVM: 9000}}
	o := NewJobOrchestrator(store, profiles, backend, manager, nil, proxmox.SnippetStore{}, "ssh-ed25519 test agent@test", "", nil, nil, nil).WithNativeSSHKeys(true)
	api := NewControlAPI(store, profiles, manager, nil, o, "", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{"repo_url":"https://example.com/repo.git","profile":"default","task":"test"}`))
	api.handleJobCreate(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "standalone sandboxes only")
	var jobCount int
	require.NoError(t, store.DB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&jobCount))
	require.Zero(t, jobCount)
	validation := httptest.NewRecorder()
	api.handleJobValidatePlan(validation, httptest.NewRequest(http.MethodPost, "/v1/jobs/validate-plan", bytes.NewBufferString(`{"repo_url":"https://example.com/repo.git","profile":"default","task":"test"}`)))
	require.Equal(t, http.StatusOK, validation.Code)
	require.Contains(t, validation.Body.String(), "unsupported_provisioning_mode")
	now := time.Now().UTC()
	require.NoError(t, store.CreateJob(ctx, models.Job{ID: "native-job", RepoURL: "https://example.com/repo.git", Ref: "main", Profile: "default", Task: "test", Status: models.JobQueued, CreatedAt: now, UpdatedAt: now}))
	require.ErrorContains(t, o.Run(ctx, "native-job"), "jobs require bootstrap credentials")
	job, err := store.GetJob(ctx, "native-job")
	require.NoError(t, err)
	require.Equal(t, models.JobFailed, job.Status)
	require.Empty(t, backend.cloneCalls)
	require.Empty(t, backend.configureCalls)
	require.Empty(t, backend.destroyCalls)
	sandboxes, err := store.ListSandboxes(ctx)
	require.NoError(t, err)
	require.Empty(t, sandboxes)
}

func TestProvisionSandboxRejectsStaticIPReuseBeforeClone(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{guestIP: "10.77.0.22"}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profile := models.Profile{Name: "default", TemplateVM: 9000, RawYAML: "network:\n  ip_config0: ip=10.77.0.22/24,gw=10.77.0.1\n"}
	o := NewJobOrchestrator(store, map[string]models.Profile{"default": profile, "alias": profile}, backend, manager, nil, proxmox.SnippetStore{}, "ssh-ed25519 test agent@test", "", nil, nil, nil).WithNativeSSHKeys(true)
	now := time.Now().UTC()
	for i, name := range []string{"default", "alias"} {
		require.NoError(t, store.CreateSandbox(ctx, models.Sandbox{VMID: 1201 + i, Name: name, Profile: name, State: models.SandboxRequested, CreatedAt: now, LastUpdatedAt: now}))
	}
	_, err := o.ProvisionSandbox(ctx, 1201)
	require.NoError(t, err)
	_, err = o.ProvisionSandbox(ctx, 1202)
	require.ErrorContains(t, err, "static IP 10.77.0.22 is already reserved by sandbox 1201")
	require.Len(t, backend.cloneCalls, 1)
	require.Len(t, backend.startCalls, 1)
	// A stopped VM still owns its address, because it can be restarted.
	_, err = store.DB.Exec("UPDATE sandboxes SET state = 'STOPPED' WHERE vmid = 1201")
	require.NoError(t, err)
	_, err = o.ProvisionSandbox(ctx, 1202)
	require.ErrorContains(t, err, "already reserved")
	require.NoError(t, manager.Destroy(ctx, 1201))
	_, err = o.ProvisionSandbox(ctx, 1202)
	require.NoError(t, err)
	require.Len(t, backend.cloneCalls, 2)
}

func TestProvisionSandboxReleasesStaticIPAfterCloneFailure(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	backend := &orchestratorBackend{cloneErr: errors.New("clone rejected")}
	manager := NewSandboxManager(store, backend, log.New(io.Discard, "", 0))
	profile := models.Profile{Name: "default", TemplateVM: 9000, RawYAML: "network:\n  ip_config0: ip=10.77.0.22/24\n"}
	o := NewJobOrchestrator(store, map[string]models.Profile{"default": profile}, backend, manager, nil, proxmox.SnippetStore{}, "ssh-ed25519 test agent@test", "", nil, nil, nil).WithNativeSSHKeys(true)
	now := time.Now().UTC()
	require.NoError(t, store.CreateSandbox(ctx, models.Sandbox{VMID: 1201, Name: "first", Profile: "default", State: models.SandboxRequested, CreatedAt: now, LastUpdatedAt: now}))
	_, err := o.ProvisionSandbox(ctx, 1201)
	require.ErrorContains(t, err, "clone rejected")
	var count int
	require.NoError(t, store.DB.QueryRow("SELECT COUNT(*) FROM sandbox_static_ips").Scan(&count))
	require.Zero(t, count)
	require.Empty(t, backend.destroyCalls)
}

func TestProfileStaticIPs(t *testing.T) {
	for _, tc := range []struct {
		config  string
		want    []string
		invalid bool
	}{
		{"ip=dhcp,ip6=auto", nil, false},
		{"ip=10.77.0.22/24,gw=10.77.0.1,ip6=2001:0db8::22/64", []string{"10.77.0.22", "2001:db8::22"}, false},
		{"ip=invalid", nil, true},
		{"ip6=10.77.0.22/24", nil, true},
	} {
		t.Run(tc.config, func(t *testing.T) {
			ips, err := profileStaticIPs(tc.config)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, ips)
			}
		})
	}
}

func TestSandboxCreateStaticIPConflictResponse(t *testing.T) {
	api, backend, _ := newSandboxCreateGuardAPI(t, nil)
	profile := api.profiles["default"]
	profile.RawYAML = "network:\n  ip_config0: ip=10.77.0.22/24\n"
	api.profiles["default"] = profile
	api.jobOrchestrator.WithNativeSSHKeys(true)
	first := postSandboxes(t, api, nil, `{"profile":"default","vmid":1201}`)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	second := postSandboxes(t, api, nil, `{"profile":"default","vmid":1202}`)
	require.Equal(t, http.StatusConflict, second.Code, second.Body.String())
	require.Contains(t, second.Body.String(), "sandbox 1201")
	require.Len(t, backend.cloneCalls, 1)
}
