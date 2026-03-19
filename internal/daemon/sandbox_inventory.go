package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/agentlab/agentlab/internal/models"
	"github.com/agentlab/agentlab/internal/proxmox"
)

const (
	sandboxDriftMissingInProxmox          = "missing_in_proxmox"
	sandboxDriftMissingAgentIP            = "missing_agent_ip"
	sandboxDriftRestoredAfterDestroy      = "restored_after_destroy"
	sandboxDriftStoppedWhileMarkedRunning = "stopped_while_marked_running"
	sandboxDriftStuckBeforeReady          = "stuck_before_ready"
	sandboxDriftUnmanagedProxmoxVM        = "unmanaged_proxmox_vm"
	sandboxDriftRunningStateMismatch      = "proxmox_running_state_mismatch"
	sandboxDriftStoppedStateMismatch      = "proxmox_stopped_state_mismatch"
)

type sandboxInventoryRecord struct {
	sandbox *models.Sandbox
	vm      *proxmox.VMSummary
	peer    *tailnetPeer
	drift   []string
}

type tailnetPeer struct {
	HostName     string
	DNSName      string
	TailscaleIPs []string
}

func (api *ControlAPI) handleSandboxInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, []string{http.MethodGet})
		return
	}
	records, err := api.collectSandboxInventory(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, V1SandboxInventoryResponse{Sandboxes: inventoryRecordsToV1(records)})
}

func (api *ControlAPI) handleSandboxReconcile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, []string{http.MethodPost})
		return
	}
	var req V1SandboxReconcileRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	before, err := api.collectSandboxInventory(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := V1SandboxReconcileResponse{
		DryRun:  !req.Apply,
		Checked: len(before),
		Drifted: countDriftedInventory(before),
		Results: inventoryRecordsToV1(before),
	}
	if !req.Apply {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if err := api.applySandboxReconcile(r.Context(), before); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	after, err := api.collectSandboxInventory(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp.DryRun = false
	resp.Checked = len(after)
	resp.Drifted = countDriftedInventory(after)
	resp.Reconciled = countReconciledInventory(before, after)
	resp.Results = inventoryRecordsToV1(after)
	writeJSON(w, http.StatusOK, resp)
}

func (api *ControlAPI) collectSandboxInventory(ctx context.Context) ([]sandboxInventoryRecord, error) {
	if api == nil || api.store == nil {
		return nil, fmt.Errorf("sandbox inventory unavailable: store not configured")
	}
	if api.backend == nil {
		return nil, fmt.Errorf("sandbox inventory unavailable: proxmox backend not configured")
	}
	sandboxes, err := api.store.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	vms, err := api.backend.ListVMs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list proxmox vms: %w", err)
	}
	peers := api.loadTailscalePeerInventory(ctx)

	sandboxByVMID := make(map[int]models.Sandbox, len(sandboxes))
	for _, sb := range sandboxes {
		sandboxByVMID[sb.VMID] = sb
	}
	liveByVMID := make(map[int]proxmox.VMSummary, len(vms))
	records := make([]sandboxInventoryRecord, 0, len(vms))
	for _, vm := range vms {
		liveByVMID[int(vm.VMID)] = vm
		record := sandboxInventoryRecord{
			vm:    cloneVMSummary(vm),
			drift: []string{},
		}
		if sb, ok := sandboxByVMID[int(vm.VMID)]; ok {
			record.sandbox = cloneSandbox(sb)
			record.drift = detectSandboxInventoryDrift(record.vm, record.sandbox)
			if peer, ok := matchTailnetPeer(peers, sandboxInventoryName(record.vm, record.sandbox)); ok {
				record.peer = &peer
			}
		} else {
			record.drift = []string{sandboxDriftUnmanagedProxmoxVM}
			if peer, ok := matchTailnetPeer(peers, record.vm.Name); ok {
				record.peer = &peer
			}
		}
		records = append(records, record)
	}
	for _, sb := range sandboxes {
		if _, ok := liveByVMID[sb.VMID]; ok {
			continue
		}
		record := sandboxInventoryRecord{
			sandbox: cloneSandbox(sb),
			drift:   detectSandboxInventoryDrift(nil, cloneSandbox(sb)),
		}
		if len(record.drift) == 0 {
			continue
		}
		if peer, ok := matchTailnetPeer(peers, sb.Name); ok {
			record.peer = &peer
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		leftVMID := inventoryVMID(records[i])
		rightVMID := inventoryVMID(records[j])
		if leftVMID == rightVMID {
			return sandboxInventoryName(records[i].vm, records[i].sandbox) < sandboxInventoryName(records[j].vm, records[j].sandbox)
		}
		return leftVMID < rightVMID
	})
	return records, nil
}

func (api *ControlAPI) applySandboxReconcile(ctx context.Context, records []sandboxInventoryRecord) error {
	needsManager := false
	restored := make([]sandboxInventoryRecord, 0)
	for _, record := range records {
		if hasInventoryDrift(record.drift, sandboxDriftMissingInProxmox, sandboxDriftMissingAgentIP, sandboxDriftStoppedWhileMarkedRunning, sandboxDriftStuckBeforeReady) {
			needsManager = true
		}
		if hasInventoryDrift(record.drift, sandboxDriftRestoredAfterDestroy) {
			restored = append(restored, record)
		}
	}
	if needsManager && api.sandboxManager != nil {
		if err := api.sandboxManager.ReconcileState(ctx); err != nil {
			return fmt.Errorf("reconcile sandbox state: %w", err)
		}
	}
	for _, record := range restored {
		if err := api.adoptRestoredSandbox(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (api *ControlAPI) adoptRestoredSandbox(ctx context.Context, record sandboxInventoryRecord) error {
	if api == nil || api.store == nil || api.backend == nil || record.sandbox == nil || record.vm == nil {
		return nil
	}
	targetState := sandboxStateFromProxmoxStatus(record.vm.Status)
	if err := api.store.ForceSetSandboxState(ctx, record.sandbox.VMID, targetState); err != nil {
		return fmt.Errorf("adopt restored sandbox %d: %w", record.sandbox.VMID, err)
	}
	if err := api.store.UpdateSandboxLease(ctx, record.sandbox.VMID, time.Time{}); err != nil {
		return fmt.Errorf("clear restored sandbox %d lease: %w", record.sandbox.VMID, err)
	}
	if targetState == models.SandboxRunning {
		ipCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ip, err := api.backend.GuestIP(ipCtx, proxmox.VMID(record.sandbox.VMID))
		cancel()
		if err == nil && strings.TrimSpace(ip) != "" {
			_ = api.store.UpdateSandboxIP(ctx, record.sandbox.VMID, strings.TrimSpace(ip))
		}
	}
	return nil
}

func (api *ControlAPI) loadTailscalePeerInventory(ctx context.Context) map[string]tailnetPeer {
	if api == nil {
		return nil
	}
	lookup := api.tailscalePeers
	if lookup == nil {
		lookup = defaultTailscalePeerInventory
	}
	peers, err := lookup(ctx)
	if err != nil {
		if api.logger != nil {
			api.logger.Printf("sandbox inventory: tailscale peer lookup failed: %v", err)
		}
		return nil
	}
	return peers
}

func defaultTailscalePeerInventory(ctx context.Context) (map[string]tailnetPeer, error) {
	runner := proxmox.ExecRunner{}
	ctx, cancel := withOptionalTimeout(ctx, defaultTailscaleCommandTimeout)
	defer cancel()
	output, err := runner.Run(ctx, defaultTailscaleCommand, "status", "--json")
	if err != nil {
		return nil, fmt.Errorf("tailscale status failed: %w", err)
	}
	var status struct {
		Peer map[string]struct {
			DNSName      string   `json:"DNSName"`
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Peer"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		return nil, fmt.Errorf("parse tailscale status: %w", err)
	}
	peers := make(map[string]tailnetPeer)
	for _, raw := range status.Peer {
		peer := tailnetPeer{
			HostName:     strings.TrimSpace(raw.HostName),
			DNSName:      strings.TrimSuffix(strings.TrimSpace(raw.DNSName), "."),
			TailscaleIPs: dedupeNonEmpty(raw.TailscaleIPs),
		}
		for _, key := range inventoryLookupKeys(peer.HostName, peer.DNSName) {
			if _, exists := peers[key]; !exists {
				peers[key] = peer
			}
		}
	}
	return peers, nil
}

func inventoryRecordsToV1(records []sandboxInventoryRecord) []V1SandboxInventoryEntry {
	out := make([]V1SandboxInventoryEntry, 0, len(records))
	for _, record := range records {
		entry := V1SandboxInventoryEntry{
			VMID:    inventoryVMID(record),
			Name:    sandboxInventoryName(record.vm, record.sandbox),
			Managed: record.sandbox != nil,
			Drift:   append([]string(nil), record.drift...),
		}
		if record.vm != nil {
			entry.ProxmoxStatus = string(record.vm.Status)
		} else if record.sandbox != nil {
			entry.ProxmoxStatus = "missing"
		}
		if record.sandbox != nil {
			entry.Profile = record.sandbox.Profile
			entry.AgentlabState = string(record.sandbox.State)
			entry.AgentlabIP = strings.TrimSpace(record.sandbox.IP)
		}
		if record.peer != nil {
			entry.TailscaleDNS = record.peer.DNSName
			entry.TailscaleIPs = append([]string(nil), record.peer.TailscaleIPs...)
		}
		out = append(out, entry)
	}
	return out
}

func detectSandboxInventoryDrift(vm *proxmox.VMSummary, sandbox *models.Sandbox) []string {
	if sandbox == nil {
		if vm == nil {
			return nil
		}
		return []string{sandboxDriftUnmanagedProxmoxVM}
	}
	if vm == nil {
		switch sandbox.State {
		case models.SandboxDestroyed, models.SandboxCompleted, models.SandboxRequested:
			return nil
		default:
			return []string{sandboxDriftMissingInProxmox}
		}
	}
	drift := make([]string, 0, 2)
	switch {
	case sandbox.State == models.SandboxDestroyed:
		drift = append(drift, sandboxDriftRestoredAfterDestroy)
	case vm.Status == proxmox.StatusStopped && sandbox.State == models.SandboxRunning:
		drift = append(drift, sandboxDriftStoppedWhileMarkedRunning)
	case vm.Status == proxmox.StatusRunning && isProvisioningSandboxState(sandbox.State):
		drift = append(drift, sandboxDriftStuckBeforeReady)
	}
	if vm.Status == proxmox.StatusRunning && strings.TrimSpace(sandbox.IP) == "" {
		drift = append(drift, sandboxDriftMissingAgentIP)
	}
	if vm.Status == proxmox.StatusRunning && isUnexpectedRunningState(sandbox.State) {
		drift = append(drift, sandboxDriftRunningStateMismatch)
	}
	if vm.Status == proxmox.StatusStopped && isUnexpectedStoppedState(sandbox.State) {
		drift = append(drift, sandboxDriftStoppedStateMismatch)
	}
	return dedupeNonEmpty(drift)
}

func countDriftedInventory(records []sandboxInventoryRecord) int {
	total := 0
	for _, record := range records {
		if len(record.drift) > 0 {
			total++
		}
	}
	return total
}

func countReconciledInventory(before []sandboxInventoryRecord, after []sandboxInventoryRecord) int {
	afterByVMID := make(map[int][]string, len(after))
	for _, record := range after {
		afterByVMID[inventoryVMID(record)] = record.drift
	}
	count := 0
	for _, record := range before {
		if len(record.drift) == 0 {
			continue
		}
		driftAfter, ok := afterByVMID[inventoryVMID(record)]
		if !ok {
			count++
			continue
		}
		if !sameStringSlice(record.drift, driftAfter) {
			count++
		}
	}
	return count
}

func matchTailnetPeer(peers map[string]tailnetPeer, names ...string) (tailnetPeer, bool) {
	for _, name := range names {
		for _, key := range inventoryLookupKeys(name) {
			if peer, ok := peers[key]; ok {
				return peer, true
			}
		}
	}
	return tailnetPeer{}, false
}

func inventoryLookupKeys(values ...string) []string {
	keys := make([]string, 0, len(values)*2)
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSuffix(strings.TrimSpace(value), ".")
		if value == "" {
			continue
		}
		for _, candidate := range []string{strings.ToLower(value), inventoryLabel(value)} {
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			keys = append(keys, candidate)
		}
	}
	return keys
}

func inventoryLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	if idx := strings.IndexByte(value, '.'); idx > 0 {
		return value[:idx]
	}
	return value
}

func inventoryVMID(record sandboxInventoryRecord) int {
	if record.sandbox != nil {
		return record.sandbox.VMID
	}
	if record.vm != nil {
		return int(record.vm.VMID)
	}
	return 0
}

func sandboxInventoryName(vm *proxmox.VMSummary, sandbox *models.Sandbox) string {
	if vm != nil && strings.TrimSpace(vm.Name) != "" {
		return strings.TrimSpace(vm.Name)
	}
	if sandbox != nil {
		return strings.TrimSpace(sandbox.Name)
	}
	return ""
}

func sandboxStateFromProxmoxStatus(status proxmox.Status) models.SandboxState {
	switch status {
	case proxmox.StatusRunning:
		return models.SandboxRunning
	case proxmox.StatusStopped:
		return models.SandboxStopped
	default:
		return models.SandboxStopped
	}
}

func isProvisioningSandboxState(state models.SandboxState) bool {
	switch state {
	case models.SandboxRequested, models.SandboxProvisioning, models.SandboxBooting:
		return true
	default:
		return false
	}
}

func isUnexpectedRunningState(state models.SandboxState) bool {
	switch state {
	case models.SandboxStopped, models.SandboxSuspended, models.SandboxFailed, models.SandboxTimeout, models.SandboxCompleted:
		return true
	default:
		return false
	}
}

func isUnexpectedStoppedState(state models.SandboxState) bool {
	switch state {
	case models.SandboxProvisioning, models.SandboxBooting, models.SandboxReady, models.SandboxSuspended:
		return true
	default:
		return false
	}
}

func hasInventoryDrift(drift []string, targets ...string) bool {
	for _, code := range drift {
		for _, target := range targets {
			if code == target {
				return true
			}
		}
	}
	return false
}

func sameStringSlice(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for idx := range left {
		if left[idx] != right[idx] {
			return false
		}
	}
	return true
}

func dedupeNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cloneSandbox(sb models.Sandbox) *models.Sandbox {
	copy := sb
	return &copy
}

func cloneVMSummary(vm proxmox.VMSummary) *proxmox.VMSummary {
	copy := vm
	return &copy
}
