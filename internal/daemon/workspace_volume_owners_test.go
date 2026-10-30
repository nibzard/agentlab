package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
	"github.com/stretchr/testify/require"
)

type volumeOwnerBackend struct {
	*allocationBackend
	inventory      []proxmox.VMID
	inventoryError error
	preflightError bool
}

func (b *volumeOwnerBackend) VolumeOwnerVMIDs(context.Context) ([]proxmox.VMID, error) {
	return b.inventory, b.inventoryError
}
func (b *volumeOwnerBackend) CreateVolume(ctx context.Context, storage, name string, size int) (string, error) {
	if b.preflightError {
		return "", &proxmox.VolumePreflightError{Cause: errors.New("storage status unavailable")}
	}
	return b.allocationBackend.CreateVolume(ctx, storage, name, size)
}

func TestWorkspaceVolumeOwnerInventoryFailureStopsBeforeAllocation(t *testing.T) {
	api, store, underlying, _ := workspaceSubmissionAPI(t)
	backend := &volumeOwnerBackend{allocationBackend: underlying, inventoryError: errors.New("cluster inventory unavailable")}
	api.workspaceMgr = NewWorkspaceManager(store, backend, nil)
	first := submitWorkspaceJob(api, "inventory-required", "safe-owner")
	require.Equal(t, 409, first.Code, first.Body.String())
	require.Contains(t, first.Body.String(), "workspace_allocation_preflight_failed")
	require.Zero(t, underlying.creates.Load())
	var count int
	require.NoError(t, store.DB.QueryRow(`SELECT COUNT(*) FROM workspace_volume_owners`).Scan(&count))
	require.Zero(t, count)
	backend.inventoryError = nil
	next := submitWorkspaceJob(api, "inventory-required", "safe-owner")
	require.Equal(t, 201, next.Code, next.Body.String())
	require.Equal(t, int32(1), underlying.creates.Load())
}

func TestWorkspaceVolumeOwnerDoesNotRedirectAfterExternalReuse(t *testing.T) {
	api, store, underlying, _ := workspaceSubmissionAPI(t)
	backend := &volumeOwnerBackend{allocationBackend: underlying, preflightError: true}
	api.workspaceMgr = NewWorkspaceManager(store, backend, nil)
	first := submitWorkspaceJob(api, "retry-owner", "same-owner")
	require.Equal(t, 409, first.Code, first.Body.String())
	ownerRows, err := store.DB.Query(`SELECT vmid,workspace_id FROM workspace_volume_owners`)
	require.NoError(t, err)
	require.True(t, ownerRows.Next())
	var ownerID int
	var workspaceID string
	require.NoError(t, ownerRows.Scan(&ownerID, &workspaceID))
	require.NoError(t, ownerRows.Close())
	backend.preflightError = false
	backend.inventory = []proxmox.VMID{proxmox.VMID(ownerID)}
	blocked := submitWorkspaceJob(api, "retry-owner", "same-owner")
	require.Equal(t, 409, blocked.Code, blocked.Body.String())
	require.Zero(t, underlying.creates.Load())
	stable, err := store.WorkspaceVolumeOwner(context.Background(), workspaceID)
	require.NoError(t, err)
	require.Equal(t, ownerID, stable.VMID)
	// Removing the external conflict permits the original reservation to proceed.
	backend.inventory = nil
	resumed := submitWorkspaceJob(api, "retry-owner", "same-owner")
	require.Equal(t, 201, resumed.Code, resumed.Body.String())
	require.Equal(t, int32(1), underlying.creates.Load())
	require.Zero(t, underlying.deletes.Load())
}

func TestWorkspaceOwnerReservationMutationBlocksKnownReceipt(t *testing.T) {
	api, store, backend, _ := workspaceSubmissionAPI(t)
	ctx := context.Background()
	_, err := store.ReserveJobSubmission(ctx, db.JobSubmission{Owner: "test", KeyHash: "key", InputHash: "inputs", JobID: "job-identity", ExpiresAt: api.now().Add(time.Hour)}, api.now())
	require.NoError(t, err)
	_, err = store.ReserveJobWorkspaceAllocation(ctx, db.JobWorkspaceAllocation{JobID: "job-identity", WorkspaceID: "workspace-test-owner", Name: "test-owner", Storage: "local-zfs", SizeGB: 10})
	require.NoError(t, err)
	owner, err := store.ReserveWorkspaceVolumeOwner(ctx, "workspace-test-owner", nil)
	require.NoError(t, err)
	require.NoError(t, store.BeginJobWorkspaceAllocation(ctx, "job-identity"))
	require.NoError(t, store.RecordJobWorkspaceVolume(ctx, "job-identity", "local-zfs:"+owner.AllocationName, true))
	_, err = store.DB.Exec(`UPDATE vmid_reservations SET owner='someone-else' WHERE vmid=?`, owner.VMID)
	require.NoError(t, err)
	_, err = api.workspaceMgr.CreateForSubmission(ctx, db.JobSubmission{JobID: "job-identity"}, "test-owner", "local-zfs", 10)
	var retained *workspaceAllocationError
	require.ErrorAs(t, err, &retained)
	require.ErrorIs(t, err, db.ErrWorkspaceVolumeOwner)
	require.Zero(t, backend.creates.Load())
	workspaces, err := store.ListWorkspaces(ctx)
	require.NoError(t, err)
	require.Empty(t, workspaces)
	// The public response accounts for the encoded storage owner separately.
	response := workspaceToV1(models.Workspace{VolumeID: "local-zfs:" + owner.AllocationName})
	require.Equal(t, owner.VMID, response.OwnerVMID)
	require.Equal(t, owner.AllocationName, response.AllocationName)
}
