package daemon

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/secrets"
	"github.com/agentlab/agentlab/internal/tailscale/admin"
)

const (
	defaultAgentIPv4Mask     = 16
	defaultAgentIPv6Mask     = 64
	defaultArtifactTokenTTL  = 6 * time.Hour
	artifactTokenBytes       = 16
	maxArtifactTokenAttempts = 5
	// tailscaleMintKeyTTL is the lifetime of a per-VM auth key minted at
	// bootstrap. Single-use + ephemeral means an undelivered key self-revokes.
	tailscaleMintKeyTTL = time.Hour
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
	rateLimiter      *IPRateLimiter
	tailscaleMinter  TailscaleKeyMinter
	logger           *log.Logger
}

func NewBootstrapAPI(store *db.Store, profiles map[string]models.Profile, secretsStore secrets.Store, secretsBundle string, agentSubnet *net.IPNet, artifactEndpoint string, artifactTokenTTL time.Duration, redactor *Redactor, rateLimiter *IPRateLimiter) *BootstrapAPI {
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
		agentSubnet:      agentSubnet,
		rateLimiter:      rateLimiter,
		tailscaleMinter:  daemonTailscaleMinter{},
		logger:           log.Default(),
	}
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
	if api.rateLimiter != nil && !api.rateLimiter.Allow(r.RemoteAddr) {
		writeRateLimitExceeded(w)
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
	tokenHash, err := db.HashBootstrapToken(req.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid token")
		return
	}
	valid, err := api.store.ValidateBootstrapToken(r.Context(), tokenHash, req.VMID, api.now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate token")
		return
	}
	if !valid {
		writeError(w, http.StatusForbidden, "invalid or expired bootstrap token")
		return
	}
	bundle, err := api.secretsStore.Load(r.Context(), api.secretsBundle)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load secrets bundle")
		return
	}
	if api.redactor != nil {
		api.redactor.AddKeys(envKeys(bundle.Env)...)
		if authKey := bundle.GetTailscaleAuthKey(); authKey != "" {
			api.redactor.AddValues(authKey)
		}
		if adminKey := bundle.GetTailscaleAdminAPIKey(); adminKey != "" {
			api.redactor.AddValues(adminKey)
		}
	}
	claudeSettings, err := bundle.ClaudeSettingsJSON()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode claude settings")
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
	var profile *models.Profile
	if api.profiles != nil {
		if stored, ok := api.profiles[job.Profile]; ok {
			profile = &stored
		}
	}
	policy, err := bootstrapPolicyFromJobAndProfile(job, profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid profile behavior policy")
		return
	}
	if policy != nil {
		resp.Policy = policy
	}

	consumed, err := api.store.ConsumeBootstrapToken(r.Context(), tokenHash, req.VMID, api.now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to consume token")
		return
	}
	if !consumed {
		writeError(w, http.StatusForbidden, "invalid or expired bootstrap token")
		return
	}

	// Mint/deliver Tailscale enrollment AFTER the single-use token is consumed,
	// so a per-VM key is only ever created for a token that was actually
	// delivered to this guest (no orphaned keys on a lost consume race).
	ts, err := api.tailscaleForBootstrap(r.Context(), bundle, req.VMID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to provision tailscale enrollment")
		return
	}
	if ts != nil {
		resp.Tailscale = ts
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

func bootstrapPolicyFromJobAndProfile(job models.Job, profile *models.Profile) (*V1BootstrapPolicy, error) {
	mode := strings.TrimSpace(job.Mode)
	if mode == "" {
		mode = defaultJobMode
	}
	var policy V1BootstrapPolicy
	hasPolicy := false
	if mode != "" {
		policy.Mode = mode
		hasPolicy = true
	}
	if profile != nil {
		cfg, err := parseProfileInnerSandbox(profile.RawYAML)
		if err != nil {
			return nil, err
		}
		if cfg.Name != "" {
			policy.InnerSandbox = cfg.Name
			if len(cfg.Args) > 0 {
				policy.InnerSandboxArgs = cfg.Args
			}
			hasPolicy = true
		}
	}
	if !hasPolicy {
		return nil, nil
	}
	return &policy, nil
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

// bootstrapTailscaleFromBundle maps the bundle's TailscaleBundle to the bootstrap
// response, resolving the per-VM hostname ({vmid} template, default agentlab-{vmid}).
// Returns nil when no auth key is configured, leaving the guest to skip enrollment.
func bootstrapTailscaleFromBundle(bundle secrets.Bundle, vmid int) *V1BootstrapTailscale {
	authKey := bundle.GetTailscaleAuthKey()
	if authKey == "" {
		return nil
	}
	return &V1BootstrapTailscale{
		AuthKey:   authKey,
		Hostname:  bundle.GetTailscaleHostname(vmid),
		Tags:      bundle.GetTailscaleTags(),
		ExtraArgs: bundle.GetTailscaleExtraArgs(),
	}
}

// TailscaleKeyMinter mints a per-VM Tailscale auth key. The default
// implementation calls the Tailscale Admin API; tests inject a fake to avoid
// network access and assert mint behavior deterministically.
type TailscaleKeyMinter interface {
	MintAuthKey(ctx context.Context, req TailscaleMintRequest) (TailscaleMintResult, error)
}

// TailscaleMintRequest carries the inputs to a per-VM auth-key mint.
type TailscaleMintRequest struct {
	AdminAPIKey string
	Tailnet     string
	Tags        []string
	Description string
}

// TailscaleMintResult is the freshly minted key. Key is the transient
// tskey-auth-... value delivered to one guest and never persisted.
type TailscaleMintResult struct {
	Key     string
	ID      string
	Expires string
}

// daemonTailscaleMinter is the default TailscaleKeyMinter: it builds a one-shot
// Tailscale Admin API client from the bundle credential and mints a single-use,
// ephemeral, preauthorized, tagged auth key.
type daemonTailscaleMinter struct{}

func (daemonTailscaleMinter) MintAuthKey(ctx context.Context, req TailscaleMintRequest) (TailscaleMintResult, error) {
	client, err := admin.NewClient(req.AdminAPIKey, req.Tailnet)
	if err != nil {
		return TailscaleMintResult{}, err
	}
	resp, err := client.CreateKey(ctx, admin.CreateKeyRequest{
		Capabilities: admin.KeyCapabilities{Devices: admin.KeyDeviceCapabilities{Create: admin.KeyCreateCapabilities{
			Reusable:      false,
			Ephemeral:     true,
			Preauthorized: true,
			Tags:          admin.NormalizeTags(req.Tags),
		}}},
		ExpirySeconds: int64(tailscaleMintKeyTTL.Seconds()),
		Description:   req.Description,
	})
	if err != nil {
		return TailscaleMintResult{}, err
	}
	return TailscaleMintResult{Key: resp.Key, ID: resp.ID, Expires: resp.Expires}, nil
}

// tailscaleForBootstrap resolves the Tailscale enrollment payload for a VM. When
// an Admin API key is configured (and a minter is wired) it mints a fresh
// per-VM key; on mint failure it falls back to any stored shared key. With no
// admin key it returns the legacy shared-key payload, or nil when Tailscale is
// unconfigured so the guest skips enrollment.
func (api *BootstrapAPI) tailscaleForBootstrap(ctx context.Context, bundle secrets.Bundle, vmid int) (*V1BootstrapTailscale, error) {
	if !bundle.TailscaleMintingConfigured() {
		// No admin key: legacy shared-key path (or nil when unconfigured).
		return bootstrapTailscaleFromBundle(bundle, vmid), nil
	}
	if api.tailscaleMinter == nil {
		// Minting is configured but no minter is wired. NewBootstrapAPI always
		// installs one, so this is unreachable on a real daemon — but if a future
		// change drops it, degrade loudly to the shared key instead of silently.
		if api.logger != nil {
			api.logger.Printf("tailscale minting configured for vmid=%d but no minter is wired; falling back to shared authkey", vmid)
		}
		return bootstrapTailscaleFromBundle(bundle, vmid), nil
	}
	result, err := api.tailscaleMinter.MintAuthKey(ctx, TailscaleMintRequest{
		AdminAPIKey: bundle.GetTailscaleAdminAPIKey(),
		Tailnet:     bundle.GetTailscaleTailnet(),
		Tags:        bundle.GetTailscaleTags(),
		Description: fmt.Sprintf("agentlab vmid=%d", vmid),
	})
	if err == nil {
		if api.redactor != nil {
			api.redactor.AddValues(result.Key)
		}
		return &V1BootstrapTailscale{
			AuthKey:   result.Key,
			Hostname:  bundle.GetTailscaleHostname(vmid),
			Tags:      bundle.GetTailscaleTags(),
			ExtraArgs: bundle.GetTailscaleExtraArgs(),
		}, nil
	}
	// Mint failed: degrade to the shared key if one is available, so a
	// transient Tailscale-API outage does not strand the VM.
	if ts := bootstrapTailscaleFromBundle(bundle, vmid); ts != nil {
		if api.logger != nil {
			api.logger.Printf("tailscale key mint failed for vmid=%d; falling back to shared authkey", vmid)
		}
		return ts, nil
	}
	return nil, fmt.Errorf("mint tailscale auth key: %w", err)
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
