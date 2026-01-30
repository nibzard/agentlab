package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

// WorkspaceRebindResult captures the rebind outcome for API responses.
type WorkspaceRebindResult struct {
	Workspace models.Workspace
	Sandbox   models.Sandbox
	OldVMID   *int
}

// RebindWorkspace provisions a new sandbox for the workspace and attaches the volume.
func (o *JobOrchestrator) RebindWorkspace(ctx context.Context, workspaceID, profileName string, ttlMinutes *int, keepOld bool) (result WorkspaceRebindResult, err error) {
	if o == nil || o.store == nil {
		return result, errors.New("workspace rebind unavailable")
	}
	if o.sandboxManager == nil {
		return result, errors.New("sandbox manager unavailable")
	}
	if o.workspaceMgr == nil {
		return result, errors.New("workspace manager unavailable")
	}
	if o.backend == nil {
		return result, errors.New("proxmox backend unavailable")
	}
	if o.sshPublicKey == "" {
		return result, errors.New("ssh public key unavailable")
	}
	if o.controllerURL == "" {
		return result, errors.New("controller URL unavailable")
	}

	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return result, errors.New("profile is required")
	}
	profile, ok := o.profile(profileName)
	if !ok {
		return result, fmt.Errorf("unknown profile %q", profileName)
	}
	if err := validateProfileForProvisioning(profile); err != nil {
		return result, err
	}

	workspace, err := o.workspaceMgr.Resolve(ctx, workspaceID)
	if err != nil {
		return result, err
	}
	result.Workspace = workspace

	var oldVMID *int
	if workspace.AttachedVM != nil && *workspace.AttachedVM > 0 {
		value := *workspace.AttachedVM
		oldVMID = &value
	}
	result.OldVMID = oldVMID

	newVMID, err := nextSandboxVMID(ctx, o.store)
	if err != nil {
		return result, err
	}

	now := o.now().UTC()
	var leaseExpires time.Time
	if ttlMinutes != nil && *ttlMinutes > 0 {
		leaseExpires = now.Add(time.Duration(*ttlMinutes) * time.Minute)
	}

	sandbox := models.Sandbox{
		VMID:          newVMID,
		Name:          fmt.Sprintf("sandbox-%d", newVMID),
		Profile:       profileName,
		State:         models.SandboxRequested,
		Keepalive:     true,
		LeaseExpires:  leaseExpires,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}

	created, err := createSandboxWithRetry(ctx, o.store, sandbox)
	if err != nil {
		return result, err
	}

	var (
		snippet        proxmox.CloudInitSnippet
		snippetCreated bool
		attachedToNew  bool
		detachedOld    bool
		ipAddress      string
	)

	defer func() {
		if err == nil {
			return
		}
		if attachedToNew {
			_, _ = o.workspaceMgr.Detach(ctx, workspace.ID)
		}
		if detachedOld && oldVMID != nil {
			_, _ = o.workspaceMgr.Attach(ctx, workspace.ID, *oldVMID)
		}
		if created.VMID > 0 {
			_ = o.sandboxManager.Destroy(ctx, created.VMID)
		}
		if snippetCreated {
			o.cleanupSnippet(created.VMID)
		}
	}()

	if err = o.sandboxManager.Transition(ctx, created.VMID, models.SandboxProvisioning); err != nil {
		return result, err
	}
	if err = o.backend.Clone(ctx, proxmox.VMID(profile.TemplateVM), proxmox.VMID(created.VMID), created.Name); err != nil {
		return result, err
	}

	token, tokenHash, expiresAt, err := o.bootstrapToken()
	if err != nil {
		return result, err
	}
	if err = o.store.CreateBootstrapToken(ctx, tokenHash, created.VMID, expiresAt); err != nil {
		return result, err
	}

	snippet, err = o.snippetStore.Create(proxmox.SnippetInput{
		VMID:           proxmox.VMID(created.VMID),
		Hostname:       created.Name,
		SSHPublicKey:   o.sshPublicKey,
		BootstrapToken: token,
		ControllerURL:  o.controllerURL,
	})
	if err != nil {
		return result, err
	}
	o.rememberSnippet(snippet)
	snippetCreated = true

	if err = o.backend.Configure(ctx, proxmox.VMID(created.VMID), proxmox.VMConfig{
		Name:      created.Name,
		CloudInit: snippet.StoragePath,
	}); err != nil {
		return result, err
	}

	if oldVMID != nil {
		if _, err = o.workspaceMgr.Detach(ctx, workspace.ID); err != nil {
			if errors.Is(err, ErrWorkspaceNotAttached) {
				oldVMID = nil
				result.OldVMID = nil
			} else {
				return result, err
			}
		} else {
			detachedOld = true
		}
	}

	workspace, err = o.workspaceMgr.Attach(ctx, workspace.ID, created.VMID)
	if err != nil {
		return result, err
	}
	attachedToNew = true
	result.Workspace = workspace

	if err = o.sandboxManager.Transition(ctx, created.VMID, models.SandboxBooting); err != nil {
		return result, err
	}
	if err = o.backend.Start(ctx, proxmox.VMID(created.VMID)); err != nil {
		return result, err
	}

	ipAddress, err = o.backend.GuestIP(ctx, proxmox.VMID(created.VMID))
	if err != nil {
		return result, err
	}
	if ipAddress != "" {
		if err = o.store.UpdateSandboxIP(ctx, created.VMID, ipAddress); err != nil {
			return result, err
		}
	}

	if err = o.sandboxManager.Transition(ctx, created.VMID, models.SandboxReady); err != nil {
		return result, err
	}
	if err = o.sandboxManager.Transition(ctx, created.VMID, models.SandboxRunning); err != nil {
		return result, err
	}

	if !keepOld && oldVMID != nil {
		if destroyErr := o.sandboxManager.Destroy(ctx, *oldVMID); destroyErr != nil && !errors.Is(destroyErr, ErrSandboxNotFound) {
			if o.logger != nil {
				o.logger.Printf("workspace rebind destroy old vmid=%d: %v", *oldVMID, destroyErr)
			}
		}
	}

	updated, loadErr := o.store.GetSandbox(ctx, created.VMID)
	if loadErr != nil {
		updated = created
		updated.State = models.SandboxRunning
		if ipAddress != "" {
			updated.IP = ipAddress
		}
	}
	if updated.WorkspaceID == nil && workspace.ID != "" {
		id := workspace.ID
		updated.WorkspaceID = &id
	}
	result.Sandbox = updated
	return result, nil
}
