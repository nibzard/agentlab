// ABOUTME: This file provides a deterministic in-memory Proxmox backend for tests.
// It implements the Backend interface and simulates VM/volume lifecycle operations.
package proxmox

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// FakeBackend implements Backend with in-memory state for tests.
// It is deterministic and safe for concurrent use.
type FakeBackend struct {
	mu              sync.Mutex
	vms             map[VMID]*fakeVM
	volumes         map[string]*fakeVolume
	volumeSnapshots map[string]map[string]struct{}
	nextVolumeSeq   int
}

type fakeVM struct {
	vmid      VMID
	name      string
	status    Status
	config    VMConfig
	ip        string
	snapshots map[string]struct{}
	volumes   map[string]string // slot -> volumeID
}

type fakeVolume struct {
	id         string
	storage    string
	sizeGB     int
	path       string
	attachedVM VMID
	slot       string
	snapshots  map[string]struct{}
}

// NewFakeBackend returns a FakeBackend with empty state.
func NewFakeBackend() *FakeBackend {
	return &FakeBackend{
		vms:             make(map[VMID]*fakeVM),
		volumes:         make(map[string]*fakeVolume),
		volumeSnapshots: make(map[string]map[string]struct{}),
		nextVolumeSeq:   1,
	}
}

// AddTemplate seeds a template VM into the fake backend.
func (b *FakeBackend) AddTemplate(vmid VMID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.vms[vmid]; ok {
		return
	}
	b.vms[vmid] = &fakeVM{
		vmid:      vmid,
		name:      fmt.Sprintf("template-%d", vmid),
		status:    StatusStopped,
		config:    VMConfig{Name: fmt.Sprintf("template-%d", vmid)},
		snapshots: make(map[string]struct{}),
		volumes:   make(map[string]string),
	}
}

func (b *FakeBackend) Clone(_ context.Context, template VMID, target VMID, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.vms[template]; !ok {
		return ErrVMNotFound
	}
	if _, ok := b.vms[target]; ok {
		return fmt.Errorf("vm %d already exists", target)
	}
	b.vms[target] = &fakeVM{
		vmid:      target,
		name:      strings.TrimSpace(name),
		status:    StatusStopped,
		config:    VMConfig{Name: strings.TrimSpace(name)},
		snapshots: make(map[string]struct{}),
		volumes:   make(map[string]string),
	}
	return nil
}

func (b *FakeBackend) Configure(_ context.Context, vmid VMID, cfg VMConfig) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return ErrVMNotFound
	}
	if strings.TrimSpace(cfg.Name) != "" {
		vm.name = strings.TrimSpace(cfg.Name)
		vm.config.Name = strings.TrimSpace(cfg.Name)
	}
	if cfg.Cores > 0 {
		vm.config.Cores = cfg.Cores
	}
	if cfg.MemoryMB > 0 {
		vm.config.MemoryMB = cfg.MemoryMB
	}
	if strings.TrimSpace(cfg.Bridge) != "" {
		vm.config.Bridge = strings.TrimSpace(cfg.Bridge)
	}
	if strings.TrimSpace(cfg.NetModel) != "" {
		vm.config.NetModel = strings.TrimSpace(cfg.NetModel)
	}
	if cfg.Firewall != nil {
		vm.config.Firewall = cfg.Firewall
	}
	if strings.TrimSpace(cfg.FirewallGroup) != "" {
		vm.config.FirewallGroup = strings.TrimSpace(cfg.FirewallGroup)
	}
	if strings.TrimSpace(cfg.CloudInit) != "" {
		vm.config.CloudInit = strings.TrimSpace(cfg.CloudInit)
	}
	if strings.TrimSpace(cfg.CPUPinning) != "" {
		vm.config.CPUPinning = strings.TrimSpace(cfg.CPUPinning)
	}
	return nil
}

func (b *FakeBackend) Start(_ context.Context, vmid VMID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return ErrVMNotFound
	}
	vm.status = StatusRunning
	return nil
}

func (b *FakeBackend) Stop(_ context.Context, vmid VMID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return ErrVMNotFound
	}
	vm.status = StatusStopped
	return nil
}

func (b *FakeBackend) Destroy(_ context.Context, vmid VMID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return ErrVMNotFound
	}
	for slot, volid := range vm.volumes {
		if vol, ok := b.volumes[volid]; ok {
			if vol.attachedVM == vmid {
				vol.attachedVM = 0
				vol.slot = ""
			}
		}
		delete(vm.volumes, slot)
	}
	delete(b.vms, vmid)
	return nil
}

func (b *FakeBackend) SnapshotCreate(_ context.Context, vmid VMID, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return ErrVMNotFound
	}
	if vm.snapshots == nil {
		vm.snapshots = make(map[string]struct{})
	}
	vm.snapshots[strings.TrimSpace(name)] = struct{}{}
	return nil
}

func (b *FakeBackend) SnapshotRollback(_ context.Context, vmid VMID, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return ErrVMNotFound
	}
	if _, ok := vm.snapshots[strings.TrimSpace(name)]; !ok {
		return fmt.Errorf("snapshot %q not found", name)
	}
	return nil
}

func (b *FakeBackend) SnapshotDelete(_ context.Context, vmid VMID, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return ErrVMNotFound
	}
	delete(vm.snapshots, strings.TrimSpace(name))
	return nil
}

func (b *FakeBackend) SnapshotList(_ context.Context, vmid VMID) ([]Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return nil, ErrVMNotFound
	}
	snapshots := make([]Snapshot, 0, len(vm.snapshots))
	for name := range vm.snapshots {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		snapshots = append(snapshots, Snapshot{Name: trimmed})
	}
	return snapshots, nil
}

func (b *FakeBackend) Status(_ context.Context, vmid VMID) (Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return StatusUnknown, ErrVMNotFound
	}
	return vm.status, nil
}

func (b *FakeBackend) CurrentStats(_ context.Context, vmid VMID) (VMStats, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.vms[vmid]; !ok {
		return VMStats{}, ErrVMNotFound
	}
	return VMStats{CPUUsage: 0.01}, nil
}

func (b *FakeBackend) GuestIP(_ context.Context, vmid VMID) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return "", ErrVMNotFound
	}
	if vm.ip == "" {
		vm.ip = fakeIPForVM(vmid)
	}
	return vm.ip, nil
}

func (b *FakeBackend) VMConfig(_ context.Context, vmid VMID) (map[string]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return nil, ErrVMNotFound
	}
	cfg := map[string]string{
		"name":   vm.name,
		"bridge": vm.config.Bridge,
	}
	return cfg, nil
}

func (b *FakeBackend) CreateVolume(_ context.Context, storage, name string, sizeGB int) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	storage = strings.TrimSpace(storage)
	if storage == "" {
		storage = "local"
	}
	normalized := strings.TrimSpace(name)
	if normalized == "" {
		normalized = fmt.Sprintf("vol-%d", b.nextVolumeSeq)
	}
	id := fmt.Sprintf("%s:vm-%s-disk-1", storage, normalized)
	if _, ok := b.volumes[id]; ok {
		id = fmt.Sprintf("%s:vm-%s-disk-%d", storage, normalized, b.nextVolumeSeq)
	}
	b.nextVolumeSeq++
	vol := &fakeVolume{
		id:        id,
		storage:   storage,
		sizeGB:    sizeGB,
		path:      filepath.Join("/fake", strings.ReplaceAll(id, ":", "/")),
		snapshots: make(map[string]struct{}),
	}
	b.volumes[id] = vol
	return id, nil
}

func (b *FakeBackend) AttachVolume(_ context.Context, vmid VMID, volumeID, slot string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return ErrVMNotFound
	}
	vol, ok := b.volumes[volumeID]
	if !ok {
		return ErrVolumeNotFound
	}
	if vol.attachedVM != 0 && vol.attachedVM != vmid {
		return fmt.Errorf("volume %s already attached", volumeID)
	}
	slot = strings.TrimSpace(slot)
	if slot == "" {
		slot = "scsi1"
	}
	vm.volumes[slot] = volumeID
	vol.attachedVM = vmid
	vol.slot = slot
	return nil
}

func (b *FakeBackend) DetachVolume(_ context.Context, vmid VMID, slot string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vm, ok := b.vms[vmid]
	if !ok {
		return ErrVMNotFound
	}
	slot = strings.TrimSpace(slot)
	if slot == "" {
		return nil
	}
	volid, ok := vm.volumes[slot]
	if !ok {
		return nil
	}
	if vol, ok := b.volumes[volid]; ok {
		if vol.attachedVM == vmid {
			vol.attachedVM = 0
			vol.slot = ""
		}
	}
	delete(vm.volumes, slot)
	return nil
}

func (b *FakeBackend) DeleteVolume(_ context.Context, volumeID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.volumes[volumeID]; !ok {
		return ErrVolumeNotFound
	}
	delete(b.volumes, volumeID)
	delete(b.volumeSnapshots, volumeID)
	return nil
}

func (b *FakeBackend) VolumeInfo(_ context.Context, volumeID string) (VolumeInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	vol, ok := b.volumes[volumeID]
	if !ok {
		return VolumeInfo{}, ErrVolumeNotFound
	}
	return VolumeInfo{
		VolumeID: vol.id,
		Storage:  vol.storage,
		Path:     vol.path,
	}, nil
}

func (b *FakeBackend) VolumeSnapshotCreate(_ context.Context, volumeID, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vol, ok := b.volumes[volumeID]
	if !ok {
		return ErrVolumeNotFound
	}
	if vol.snapshots == nil {
		vol.snapshots = make(map[string]struct{})
	}
	vol.snapshots[strings.TrimSpace(name)] = struct{}{}
	return nil
}

func (b *FakeBackend) VolumeSnapshotRestore(_ context.Context, volumeID, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vol, ok := b.volumes[volumeID]
	if !ok {
		return ErrVolumeNotFound
	}
	if _, ok := vol.snapshots[strings.TrimSpace(name)]; !ok {
		return fmt.Errorf("snapshot %q not found", name)
	}
	return nil
}

func (b *FakeBackend) VolumeSnapshotDelete(_ context.Context, volumeID, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	vol, ok := b.volumes[volumeID]
	if !ok {
		return ErrVolumeNotFound
	}
	delete(vol.snapshots, strings.TrimSpace(name))
	return nil
}

func (b *FakeBackend) VolumeClone(_ context.Context, sourceVolumeID, targetVolumeID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	source, ok := b.volumes[sourceVolumeID]
	if !ok {
		return ErrVolumeNotFound
	}
	if _, ok := b.volumes[targetVolumeID]; ok {
		return fmt.Errorf("volume %s already exists", targetVolumeID)
	}
	b.volumes[targetVolumeID] = &fakeVolume{
		id:        targetVolumeID,
		storage:   source.storage,
		sizeGB:    source.sizeGB,
		path:      filepath.Join("/fake", strings.ReplaceAll(targetVolumeID, ":", "/")),
		snapshots: make(map[string]struct{}),
	}
	return nil
}

func (b *FakeBackend) VolumeCloneFromSnapshot(_ context.Context, sourceVolumeID, snapshotName, targetVolumeID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	source, ok := b.volumes[sourceVolumeID]
	if !ok {
		return ErrVolumeNotFound
	}
	if _, ok := source.snapshots[strings.TrimSpace(snapshotName)]; !ok {
		return fmt.Errorf("snapshot %q not found", snapshotName)
	}
	if _, ok := b.volumes[targetVolumeID]; ok {
		return fmt.Errorf("volume %s already exists", targetVolumeID)
	}
	b.volumes[targetVolumeID] = &fakeVolume{
		id:        targetVolumeID,
		storage:   source.storage,
		sizeGB:    source.sizeGB,
		path:      filepath.Join("/fake", strings.ReplaceAll(targetVolumeID, ":", "/")),
		snapshots: make(map[string]struct{}),
	}
	return nil
}

func (b *FakeBackend) ValidateTemplate(_ context.Context, template VMID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.vms[template]; !ok {
		return ErrVMNotFound
	}
	return nil
}

func fakeIPForVM(vmid VMID) string {
	octet := int(vmid)%250 + 2
	return fmt.Sprintf("10.77.0.%d", octet)
}
