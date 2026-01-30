package daemon

import (
	"database/sql"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
)

const (
	defaultAgentIPv4Mask = 16
	defaultAgentIPv6Mask = 64
)

// BootstrapAPI serves guest bootstrap payloads on the agent subnet.
type BootstrapAPI struct {
	store         *db.Store
	profiles      map[string]models.Profile
	secretsStore  secrets.Store
	secretsBundle string
	now           func() time.Time
	agentSubnet   *net.IPNet
}

func NewBootstrapAPI(store *db.Store, profiles map[string]models.Profile, secretsStore secrets.Store, secretsBundle, bootstrapListen string) *BootstrapAPI {
	bundle := strings.TrimSpace(secretsBundle)
	if bundle == "" {
		bundle = "default"
	}
	api := &BootstrapAPI{
		store:         store,
		profiles:      profiles,
		secretsStore:  secretsStore,
		secretsBundle: bundle,
		now:           time.Now,
	}
	api.agentSubnet = deriveAgentSubnet(bootstrapListen)
	return api
}

func (api *BootstrapAPI) Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/v1/bootstrap/fetch", api.handleBootstrapFetch)
}

func (api *BootstrapAPI) handleBootstrapFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	if !api.remoteAllowed(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "bootstrap access restricted to agent subnet")
		return
	}
	var req V1BootstrapFetchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	if req.VMID <= 0 {
		writeError(w, http.StatusBadRequest, "vmid must be positive")
		return
	}
	if api.store == nil {
		writeError(w, http.StatusServiceUnavailable, "bootstrap service unavailable")
		return
	}
	job, err := api.store.GetJobBySandboxVMID(r.Context(), req.VMID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load job")
		return
	}
	bundle, err := api.secretsStore.Load(r.Context(), api.secretsBundle)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load secrets bundle")
		return
	}
	claudeSettings, err := bundle.ClaudeSettingsJSON()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode claude settings")
		return
	}
	tokenHash, err := db.HashBootstrapToken(req.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid token")
		return
	}
	consumed, err := api.store.ConsumeBootstrapToken(r.Context(), tokenHash, req.VMID, api.now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate token")
		return
	}
	if !consumed {
		writeError(w, http.StatusForbidden, "invalid or expired bootstrap token")
		return
	}

	resp := V1BootstrapFetchResponse{
		Job: bootstrapJobFromModel(job),
	}
	if git := bootstrapGitFromBundle(bundle); git != nil {
		resp.Git = git
	}
	if len(bundle.Env) > 0 {
		resp.Env = bundle.Env
	}
	if claudeSettings != "" {
		resp.ClaudeSettingsJSON = claudeSettings
	}
	if artifact := bootstrapArtifactFromBundle(bundle); artifact != nil {
		resp.Artifact = artifact
	}
	if policy := bootstrapPolicyFromJob(job); policy != nil {
		resp.Policy = policy
	}

	writeJSON(w, http.StatusOK, resp)
}

func (api *BootstrapAPI) remoteAllowed(addr string) bool {
	if api.agentSubnet == nil {
		return true
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	return api.agentSubnet.Contains(ip)
}

func deriveAgentSubnet(listen string) *net.IPNet {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		host = listen
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		return nil
	}
	if ip4 := ip.To4(); ip4 != nil {
		mask := net.CIDRMask(defaultAgentIPv4Mask, 32)
		base := ip4.Mask(mask)
		return &net.IPNet{IP: base, Mask: mask}
	}
	mask := net.CIDRMask(defaultAgentIPv6Mask, 128)
	base := ip.Mask(mask)
	return &net.IPNet{IP: base, Mask: mask}
}

func bootstrapJobFromModel(job models.Job) V1BootstrapJob {
	mode := strings.TrimSpace(job.Mode)
	if mode == "" {
		mode = defaultJobMode
	}
	resp := V1BootstrapJob{
		ID:        job.ID,
		RepoURL:   job.RepoURL,
		Ref:       job.Ref,
		Task:      job.Task,
		Mode:      mode,
		Profile:   job.Profile,
		Keepalive: job.Keepalive,
	}
	if job.TTLMinutes > 0 {
		value := job.TTLMinutes
		resp.TTLMinutes = &value
	}
	return resp
}

func bootstrapPolicyFromJob(job models.Job) *V1BootstrapPolicy {
	mode := strings.TrimSpace(job.Mode)
	if mode == "" {
		mode = defaultJobMode
	}
	if mode == "" {
		return nil
	}
	return &V1BootstrapPolicy{Mode: mode}
}

func bootstrapGitFromBundle(bundle secrets.Bundle) *V1BootstrapGit {
	git := bundle.Git
	if strings.TrimSpace(git.Token) == "" &&
		strings.TrimSpace(git.Username) == "" &&
		strings.TrimSpace(git.SSHPrivateKey) == "" &&
		strings.TrimSpace(git.SSHPublicKey) == "" &&
		strings.TrimSpace(git.KnownHosts) == "" {
		return nil
	}
	return &V1BootstrapGit{
		Token:         git.Token,
		Username:      git.Username,
		SSHPrivateKey: git.SSHPrivateKey,
		SSHPublicKey:  git.SSHPublicKey,
		KnownHosts:    git.KnownHosts,
	}
}

func bootstrapArtifactFromBundle(bundle secrets.Bundle) *V1BootstrapArtifact {
	artifact := bundle.Artifact
	if strings.TrimSpace(artifact.Endpoint) == "" && strings.TrimSpace(artifact.Token) == "" {
		return nil
	}
	return &V1BootstrapArtifact{
		Endpoint: artifact.Endpoint,
		Token:    artifact.Token,
	}
}
