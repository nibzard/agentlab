package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/agentlab/agentlab/internal/db"
	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

// IdleStopConfig controls idle stop behavior.
type IdleStopConfig struct {
	Enabled        bool
	Interval       time.Duration
	DefaultMinutes int
	CPUThreshold   float64
}

// SSHSessionDetector reports whether a sandbox has active SSH sessions.
type SSHSessionDetector interface {
	HasActiveSSH(ctx context.Context, ip string) (bool, error)
}

// ConntrackSessionDetector uses conntrack to detect established SSH flows.
type ConntrackSessionDetector struct {
	Runner        proxmox.CommandRunner
	ConntrackPath string
}

func (d *ConntrackSessionDetector) HasActiveSSH(ctx context.Context, ip string) (bool, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false, errors.New("ip is required")
	}
	runner := d.Runner
	if runner == nil {
		runner = proxmox.ExecRunner{}
	}
	path := d.ConntrackPath
	if path == "" {
		path = "conntrack"
	}
	args := []string{"-L", "-p", "tcp", "--dport", "22", "--dst", ip}
	if strings.Contains(ip, ":") {
		args = append([]string{"-f", "ipv6"}, args...)
	}
	out, err := runner.Run(ctx, path, args...)
	if err != nil {
		return false, err
	}
	return hasEstablishedConntrack(out), nil
}

func hasEstablishedConntrack(out string) bool {
	out = strings.TrimSpace(out)
	if out == "" {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "ESTABLISHED") {
			return true
		}
	}
	return false
}

type idleStopEventPayload struct {
	IdleMinutes    int     `json:"idle_minutes"`
	IdleForMinutes int     `json:"idle_for_minutes"`
	LastActiveAt   string  `json:"last_active_at,omitempty"`
	CPUUsage       float64 `json:"cpu_usage"`
	CPUThreshold   float64 `json:"cpu_threshold"`
	SSHActive      bool    `json:"ssh_active"`
	Error          string  `json:"error,omitempty"`
}

// IdleStopper evaluates sandboxes for idle stop eligibility.
type IdleStopper struct {
	store      *db.Store
	backend    proxmox.Backend
	profiles   map[string]models.Profile
	manager    *SandboxManager
	detector   SSHSessionDetector
	logger     *log.Logger
	metrics    *Metrics
	now        func() time.Time
	cfg        IdleStopConfig
	lastActive map[int]time.Time
	mu         sync.Mutex
}

// NewIdleStopper constructs an idle stopper with defaults.
func NewIdleStopper(store *db.Store, backend proxmox.Backend, profiles map[string]models.Profile, manager *SandboxManager, detector SSHSessionDetector, logger *log.Logger, metrics *Metrics, cfg IdleStopConfig) *IdleStopper {
	if logger == nil {
		logger = log.Default()
	}
	return &IdleStopper{
		store:      store,
		backend:    backend,
		profiles:   profiles,
		manager:    manager,
		detector:   detector,
		logger:     logger,
		metrics:    metrics,
		now:        time.Now,
		cfg:        cfg,
		lastActive: make(map[int]time.Time),
	}
}

// Start runs idle stop evaluation on an interval until ctx is canceled.
func (s *IdleStopper) Start(ctx context.Context) {
	if !s.enabled() {
		return
	}
	s.Evaluate(ctx)
	ticker := time.NewTicker(s.cfg.Interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Evaluate(ctx)
			}
		}
	}()
}

// Evaluate checks all running sandboxes and stops those that are idle.
func (s *IdleStopper) Evaluate(ctx context.Context) {
	if !s.enabled() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sandboxes, err := s.store.ListSandboxes(ctx)
	if err != nil {
		s.logger.Printf("idle stop: list sandboxes error: %v", err)
		return
	}
	now := s.now().UTC()
	running := map[int]models.Sandbox{}
	for _, sb := range sandboxes {
		if sb.State == models.SandboxRunning {
			running[sb.VMID] = sb
		}
	}
	for vmid := range s.lastActive {
		if _, ok := running[vmid]; !ok {
			delete(s.lastActive, vmid)
		}
	}
	for _, sb := range running {
		s.evaluateSandbox(ctx, now, sb)
	}
}

func (s *IdleStopper) evaluateSandbox(ctx context.Context, now time.Time, sb models.Sandbox) {
	profile, ok := s.profiles[sb.Profile]
	if !ok {
		s.logger.Printf("idle stop: vmid=%d unknown profile %q", sb.VMID, sb.Profile)
		return
	}
	idleMinutes, err := idleStopMinutesForProfile(profile, s.cfg.DefaultMinutes)
	if err != nil {
		s.logger.Printf("idle stop: vmid=%d parse profile %q: %v", sb.VMID, sb.Profile, err)
		return
	}
	if idleMinutes <= 0 {
		return
	}

	lastActive := s.lastActive[sb.VMID]
	if sb.LastUpdatedAt.After(lastActive) {
		lastActive = sb.LastUpdatedAt
	}
	if sb.LastUsedAt.After(lastActive) {
		lastActive = sb.LastUsedAt
	}
	if lastActive.IsZero() || lastActive.After(now) {
		lastActive = now
	}

	if s.hasActiveJob(ctx, sb.VMID) {
		s.lastActive[sb.VMID] = now
		return
	}

	ip := strings.TrimSpace(sb.IP)
	if ip == "" {
		guestIP, err := s.backend.GuestIP(ctx, proxmox.VMID(sb.VMID))
		if err != nil {
			s.logger.Printf("idle stop: vmid=%d guest ip error: %v", sb.VMID, err)
			s.lastActive[sb.VMID] = now
			return
		}
		ip = strings.TrimSpace(guestIP)
		if ip != "" {
			_ = s.store.UpdateSandboxIP(ctx, sb.VMID, ip)
		}
	}
	if ip == "" {
		s.lastActive[sb.VMID] = lastActive
		return
	}
	if s.detector == nil {
		s.lastActive[sb.VMID] = lastActive
		return
	}
	sshActive, err := s.detector.HasActiveSSH(ctx, ip)
	if err != nil {
		s.logger.Printf("idle stop: vmid=%d ssh detect error: %v", sb.VMID, err)
		s.lastActive[sb.VMID] = now
		return
	}
	if sshActive {
		s.lastActive[sb.VMID] = now
		return
	}

	stats, err := s.backend.CurrentStats(ctx, proxmox.VMID(sb.VMID))
	if err != nil {
		s.logger.Printf("idle stop: vmid=%d stats error: %v", sb.VMID, err)
		s.lastActive[sb.VMID] = now
		return
	}
	if stats.CPUUsage > s.cfg.CPUThreshold {
		s.lastActive[sb.VMID] = now
		return
	}

	idleFor := now.Sub(lastActive)
	idleWindow := time.Duration(idleMinutes) * time.Minute
	if idleFor < idleWindow {
		s.lastActive[sb.VMID] = lastActive
		return
	}

	stopErr := s.manager.Stop(ctx, sb.VMID)
	payload := idleStopEventPayload{
		IdleMinutes:    idleMinutes,
		IdleForMinutes: int(idleFor.Minutes()),
		LastActiveAt:   lastActive.UTC().Format(time.RFC3339Nano),
		CPUUsage:       stats.CPUUsage,
		CPUThreshold:   s.cfg.CPUThreshold,
		SSHActive:      false,
	}
	if stopErr != nil {
		payload.Error = stopErr.Error()
	}
	s.recordIdleStopEvent(ctx, sb.VMID, payload, stopErr)
	if stopErr != nil {
		if s.metrics != nil {
			s.metrics.IncSandboxIdleStop("failed")
		}
		s.lastActive[sb.VMID] = now
		return
	}
	if s.metrics != nil {
		s.metrics.IncSandboxIdleStop("success")
	}
	delete(s.lastActive, sb.VMID)
}

func (s *IdleStopper) hasActiveJob(ctx context.Context, vmid int) bool {
	job, err := s.store.GetJobBySandboxVMID(ctx, vmid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		if s.logger != nil {
			s.logger.Printf("idle stop: vmid=%d job lookup error: %v", vmid, err)
		}
		return true
	}
	return job.Status == models.JobRunning || job.Status == models.JobQueued
}

func (s *IdleStopper) recordIdleStopEvent(ctx context.Context, vmid int, payload idleStopEventPayload, stopErr error) {
	if s == nil || s.store == nil {
		return
	}
	msg := fmt.Sprintf("idle stop after %dm", payload.IdleForMinutes)
	if stopErr != nil {
		msg = fmt.Sprintf("idle stop failed: %s", stopErr.Error())
	}
	data, err := json.Marshal(payload)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("idle stop: vmid=%d payload json error: %v", vmid, err)
		}
	}
	eventCtx := ctx
	if eventCtx == nil || eventCtx.Err() != nil {
		var cancel context.CancelFunc
		eventCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	_ = s.store.RecordEvent(eventCtx, "sandbox.idle_stop", &vmid, nil, msg, string(data))
}

func (s *IdleStopper) enabled() bool {
	if s == nil || s.store == nil || s.backend == nil || s.manager == nil {
		return false
	}
	if !s.cfg.Enabled {
		return false
	}
	if s.cfg.Interval <= 0 {
		return false
	}
	return true
}
