package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/agentlab/agentlab/internal/db"
)

// ExposureCleaner removes host-level exposure rules and DB rows for a sandbox.
type ExposureCleaner struct {
	store     *db.Store
	publisher ExposurePublisher
	logger    *log.Logger
}

// NewExposureCleaner creates a cleanup helper for exposures.
func NewExposureCleaner(store *db.Store, publisher ExposurePublisher, logger *log.Logger) *ExposureCleaner {
	if logger == nil {
		logger = log.Default()
	}
	return &ExposureCleaner{store: store, publisher: publisher, logger: logger}
}

// CleanupByVMID removes exposures for the sandbox VMID (best-effort).
func (c *ExposureCleaner) CleanupByVMID(ctx context.Context, vmid int) error {
	if c == nil || c.store == nil {
		return nil
	}
	exposures, err := c.store.ListExposuresByVMID(ctx, vmid)
	if err != nil {
		return err
	}
	for _, exposure := range exposures {
		if c.publisher != nil {
			if err := c.publisher.Unpublish(ctx, exposure.Name, exposure.Port); err != nil && !errors.Is(err, ErrServeRuleNotFound) {
				c.logger.Printf("exposure cleanup: failed to unpublish %s (vmid=%d): %v", exposure.Name, exposure.VMID, err)
				payload, _ := json.Marshal(map[string]any{
					"name":  exposure.Name,
					"vmid":  exposure.VMID,
					"port":  exposure.Port,
					"error": err.Error(),
				})
				_ = c.store.RecordEvent(ctx, "exposure.cleanup.failed", &exposure.VMID, nil, fmt.Sprintf("exposure %s cleanup failed", exposure.Name), string(payload))
			}
		}
		if err := c.store.DeleteExposure(ctx, exposure.Name); err != nil && !errors.Is(err, sql.ErrNoRows) {
			c.logger.Printf("exposure cleanup: failed to delete %s (vmid=%d): %v", exposure.Name, exposure.VMID, err)
			continue
		}
		payload, _ := json.Marshal(map[string]any{
			"name":      exposure.Name,
			"vmid":      exposure.VMID,
			"port":      exposure.Port,
			"target_ip": exposure.TargetIP,
			"url":       exposure.URL,
			"state":     exposure.State,
		})
		_ = c.store.RecordEvent(ctx, "exposure.delete", &exposure.VMID, nil, fmt.Sprintf("exposure %s deleted (cleanup)", exposure.Name), string(payload))
	}
	return nil
}
