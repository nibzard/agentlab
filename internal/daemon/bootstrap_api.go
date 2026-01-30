package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
)

const (
	defaultAgentIPv4Mask     = 16
	defaultAgentIPv6Mask     = 64
	defaultArtifactTokenTTL  = 6 * time.Hour
	artifactTokenBytes       = 16
	maxArtifactTokenAttempts = 5
)

// BootstrapAPI serves guest bootstrap payloads on the agent subnet.
type BootstrapAPI struct {
	store            *db.Store
	profiles         map[string]models.Profile
	secretsStore     secrets.Store
	secretsBundle    string
	artifactEndpoint string
	artifactTokenTTL time.Duration
	now              func() time.Time
	rand             io.Reader
	agentSubnet      *net.IPNet
	redactor         *Redactor
}

func NewBootstrapAPI(store *db.Store, profiles map[string]models.Profile, secretsStore secrets.Store, secretsBundle, bootstrapListen, artifactEndpoint string, artifactTokenTTL time.Duration, redactor *Redactor) *BootstrapAPI {
	bundle := strings.TrimSpace(secretsBundle)
	if bundle == "" {
		bundle = "default"
	}
	if artifactTokenTTL <= 0 {
		artifactTokenTTL = defaultArtifactTokenTTL
	}
	if redactor == nil {
		redactor = NewRedactor(nil)
	}
	api := &BootstrapAPI{
		store:            store,
		profiles:         profiles,
		secretsStore:     secretsStore,
		secretsBundle:    bundle,
		artifactEndpoint: strings.TrimSpace(artifactEndpoint),
		artifactTokenTTL: artifactTokenTTL,
		now:              time.Now,
		rand:             rand.Reader,
		redactor:         redactor,
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
	if api.redactor != nil {
		api.redactor.AddKeys(envKeys(bundle.Env)...)
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
	if api.artifactEndpoint != "" {
		token, err := api.issueArtifactToken(r.Context(), job.ID, req.VMID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to issue artifact token")
			return
		}
		resp.Artifact = &V1BootstrapArtifact{
			Endpoint: api.artifactEndpoint,
			Token:    token,
		}
	} else if artifact := bootstrapArtifactFromBundle(bundle); artifact != nil {
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

func (api *BootstrapAPI) issueArtifactToken(ctx context.Context, jobID string, vmid int) (string, error) {
	if api == nil || api.store == nil {
		return "", errors.New("artifact token store unavailable")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return "", errors.New("job id is required")
	}
	if vmid <= 0 {
		return "", errors.New("vmid must be positive")
	}
	for i := 0; i < maxArtifactTokenAttempts; i++ {
		token, hash, expires, err := api.newArtifactToken()
		if err != nil {
			return "", err
		}
		if err := api.store.CreateArtifactToken(ctx, hash, jobID, vmid, expires); err != nil {
			if isUniqueConstraint(err) {
				continue
			}
			return "", err
		}
		if api.redactor != nil {
			api.redactor.AddValues(token)
		}
		return token, nil
	}
	return "", errors.New("failed to allocate artifact token")
}

func (api *BootstrapAPI) newArtifactToken() (string, string, time.Time, error) {
	buf := make([]byte, artifactTokenBytes)
	if _, err := io.ReadFull(api.randReader(), buf); err != nil {
		return "", "", time.Time{}, err
	}
	token := hex.EncodeToString(buf)
	hash, err := db.HashArtifactToken(token)
	if err != nil {
		return "", "", time.Time{}, err
	}
	expires := api.now().UTC().Add(api.artifactTokenTTL)
	return token, hash, expires, nil
}

func (api *BootstrapAPI) randReader() io.Reader {
	if api != nil && api.rand != nil {
		return api.rand
	}
	return rand.Reader
}
