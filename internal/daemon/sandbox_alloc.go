package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
)

func nextSandboxVMID(ctx context.Context, store *db.Store) (int, error) {
	if store == nil {
		return 0, errors.New("store is nil")
	}
	maxVMID, err := store.MaxSandboxVMID(ctx)
	if err != nil {
		return 0, err
	}
	if maxVMID < defaultSandboxVMIDStart {
		return defaultSandboxVMIDStart, nil
	}
	return maxVMID + 1, nil
}

func createSandboxWithRetry(ctx context.Context, store *db.Store, sandbox models.Sandbox) (models.Sandbox, error) {
	if store == nil {
		return models.Sandbox{}, errors.New("store is nil")
	}
	attempt := sandbox
	for i := 0; i < 5; i++ {
		err := store.CreateSandbox(ctx, attempt)
		if err == nil {
			return attempt, nil
		}
		if !isUniqueConstraint(err) {
			return models.Sandbox{}, err
		}
		attempt.VMID++
		if attempt.Name == sandbox.Name {
			attempt.Name = fmt.Sprintf("sandbox-%d", attempt.VMID)
		}
	}
	return models.Sandbox{}, errors.New("vmid allocation failed")
}
