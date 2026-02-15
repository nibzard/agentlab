package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"gopkg.in/yaml.v3"
)

const defaultArtifactGCInterval = 10 * time.Minute

type artifactRetentionResult struct {
	duration   time.Duration
	configured bool
	err        error
}

// ArtifactGC removes expired artifacts based on profile retention settings.
type ArtifactGC struct {
	store      *db.Store
	profiles   map[string]models.Profile
	rootDir    string
	logger     *log.Logger
	redactor   *Redactor
	now        func() time.Time
	gcInterval time.Duration
}

// NewArtifactGC constructs an artifact GC worker with defaults.
func NewArtifactGC(store *db.Store, profiles map[string]models.Profile, rootDir string, logger *log.Logger, redactor *Redactor) *ArtifactGC {
	if logger == nil {
		logger = log.Default()
	}
	if redactor == nil {
		redactor = NewRedactor(nil)
	}
	return &ArtifactGC{
		store:      store,
		profiles:   profiles,
		rootDir:    strings.TrimSpace(rootDir),
		logger:     logger,
		redactor:   redactor,
		now:        time.Now,
		gcInterval: defaultArtifactGCInterval,
	}
}

// Start runs the artifact GC immediately and on an interval until ctx is done.
func (g *ArtifactGC) Start(ctx context.Context) {
	if g == nil || g.store == nil || g.gcInterval <= 0 {
		return
	}
	g.run(ctx)
	ticker := time.NewTicker(g.gcInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.run(ctx)
			}
		}
	}()
}

func (g *ArtifactGC) run(ctx context.Context) {
	candidates, err := g.store.ListArtifactRetentionCandidates(ctx)
	if err != nil {
		g.logf("artifact GC list error: %v", err)
		return
	}
	if len(candidates) == 0 {
		return
	}
	retentionCache := make(map[string]artifactRetentionResult)
	now := g.now().UTC()
	deleted := 0
	for _, record := range candidates {
		retention, ok := g.retentionForProfile(record.JobProfile, retentionCache)
		if !ok {
			continue
		}
		if !isTerminalJobStatus(record.JobStatus) {
			continue
		}
		if record.SandboxState != "" && record.SandboxState != models.SandboxDestroyed {
			continue
		}
		base := record.JobUpdatedAt
		if base.IsZero() {
			base = record.Artifact.CreatedAt
		}
		if base.IsZero() {
			continue
		}
		if base.Add(retention).After(now) {
			continue
		}
		if err := g.deleteArtifact(ctx, record); err != nil {
			g.logf("artifact GC delete job=%s artifact=%d: %v", record.Artifact.JobID, record.Artifact.ID, err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		g.logf("artifact GC removed %d artifacts", deleted)
	}
}

func (g *ArtifactGC) retentionForProfile(profileName string, cache map[string]artifactRetentionResult) (time.Duration, bool) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return 0, false
	}
	if cached, ok := cache[profileName]; ok {
		if cached.err != nil {
			g.logf("artifact GC profile %s retention error: %v", profileName, cached.err)
		}
		return cached.duration, cached.configured && cached.duration > 0
	}
	result := artifactRetentionResult{}
	profile, ok := g.profiles[profileName]
	if !ok {
		result.err = fmt.Errorf("unknown profile %q", profileName)
		cache[profileName] = result
		g.logf("artifact GC profile %s retention error: %v", profileName, result.err)
		return 0, false
	}
	duration, configured, err := parseArtifactRetention(profile.RawYAML)
	result.duration = duration
	result.configured = configured
	result.err = err
	cache[profileName] = result
	if err != nil {
		g.logf("artifact GC profile %s retention error: %v", profileName, err)
		return 0, false
	}
	if !configured || duration <= 0 {
		return 0, false
	}
	return duration, true
}

func (g *ArtifactGC) deleteArtifact(ctx context.Context, record db.ArtifactRetentionRecord) error {
	root := strings.TrimSpace(g.rootDir)
	if root == "" {
		return errors.New("artifact root is not configured")
	}
	relPath, err := sanitizeArtifactPath(record.Artifact.Path)
	if err != nil {
		return err
	}
	jobDir := filepath.Join(root, record.Artifact.JobID)
	targetPath, err := safeJoin(jobDir, relPath)
	if err != nil {
		return err
	}
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove artifact file: %w", err)
	}
	if err := g.store.DeleteArtifact(ctx, record.Artifact.ID); err != nil {
		return err
	}
	g.recordDeletionEvent(ctx, record)
	_ = os.Remove(jobDir)
	return nil
}

func (g *ArtifactGC) recordDeletionEvent(ctx context.Context, record db.ArtifactRetentionRecord) {
	if g.store == nil {
		return
	}
	name := strings.TrimSpace(record.Artifact.Name)
	if name == "" {
		name = filepath.Base(record.Artifact.Path)
	}
	message := "artifact GC removed"
	if name != "" {
		message = fmt.Sprintf("artifact GC removed %s", name)
	}
	jobID := strings.TrimSpace(record.Artifact.JobID)
	var jobIDPtr *string
	if jobID != "" {
		jobIDPtr = &jobID
	}
	payload := map[string]any{
		"name":  name,
		"vmid": record.SandboxVMID,
		"path": record.Artifact.Path,
	}
	_ = emitEvent(ctx, NewStoreEventRecorder(g.store), EventKindArtifactGC, record.SandboxVMID, jobIDPtr, message, payload)
}

func (g *ArtifactGC) logf(format string, args ...any) {
	if g == nil || g.logger == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if g.redactor != nil {
		msg = g.redactor.Redact(msg)
	}
	g.logger.Print(msg)
}

type profileArtifactRetentionSpec struct {
	Artifacts profileArtifactRetentionConfig `yaml:"artifacts"`
}

type profileArtifactRetentionConfig struct {
	TTLMinutes       *int `yaml:"ttl_minutes"`
	RetentionMinutes *int `yaml:"retention_minutes"`
	RetentionHours   *int `yaml:"retention_hours"`
	RetentionDays    *int `yaml:"retention_days"`
}

func parseArtifactRetention(raw string) (time.Duration, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	var spec profileArtifactRetentionSpec
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return 0, false, err
	}
	if spec.Artifacts.TTLMinutes != nil {
		return durationFromMinutes(*spec.Artifacts.TTLMinutes, "artifacts.ttl_minutes")
	}
	if spec.Artifacts.RetentionMinutes != nil {
		return durationFromMinutes(*spec.Artifacts.RetentionMinutes, "artifacts.retention_minutes")
	}
	if spec.Artifacts.RetentionHours != nil {
		return durationFromHours(*spec.Artifacts.RetentionHours, "artifacts.retention_hours")
	}
	if spec.Artifacts.RetentionDays != nil {
		return durationFromDays(*spec.Artifacts.RetentionDays, "artifacts.retention_days")
	}
	return 0, false, nil
}

func durationFromMinutes(value int, field string) (time.Duration, bool, error) {
	if value < 0 {
		return 0, true, fmt.Errorf("%s must be non-negative", field)
	}
	if value == 0 {
		return 0, true, nil
	}
	return time.Duration(value) * time.Minute, true, nil
}

func durationFromHours(value int, field string) (time.Duration, bool, error) {
	if value < 0 {
		return 0, true, fmt.Errorf("%s must be non-negative", field)
	}
	if value == 0 {
		return 0, true, nil
	}
	return time.Duration(value) * time.Hour, true, nil
}

func durationFromDays(value int, field string) (time.Duration, bool, error) {
	if value < 0 {
		return 0, true, fmt.Errorf("%s must be non-negative", field)
	}
	if value == 0 {
		return 0, true, nil
	}
	return time.Duration(value) * 24 * time.Hour, true, nil
}

func isTerminalJobStatus(status models.JobStatus) bool {
	switch status {
	case models.JobCompleted, models.JobFailed, models.JobTimeout:
		return true
	default:
		return false
	}
}
