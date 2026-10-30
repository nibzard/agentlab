package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type nativeStorageTestRunner struct{ libraries string }

func (r nativeStorageTestRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name != "/usr/bin/perl" {
		return "", errors.New("unexpected test command")
	}
	supplied := append([]string{"-I" + r.libraries}, args...)
	return (ExecRunner{}).Run(ctx, name, supplied...)
}

func nativeStorageFixture(t *testing.T) (*ShellBackend, string) {
	t.Helper()
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("Perl is required for the isolated storage helper test")
	}
	root := t.TempDir()
	libraries := filepath.Join(root, "lib")
	if err := os.MkdirAll(filepath.Join(libraries, "PVE"), 0700); err != nil {
		t.Fatal(err)
	}
	modules := map[string]string{
		"INotify.pm":        `package PVE::INotify; sub nodename { 'pve' } 1;`,
		"RPCEnvironment.pm": `package PVE::RPCEnvironment; our $initialized=0; sub setup_default_cli_env { $initialized=1; return 1; } 1;`,
		"Tools.pm":          nativeStorageToolsModule,
		"Storage.pm":        nativeStorageModule,
	}
	for name, code := range modules {
		if err := os.WriteFile(filepath.Join(libraries, "PVE", name), []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(root, "storage.json")
	t.Setenv("AGENTLAB_NATIVE_TEST_STATE", state)
	initial := map[string]any{"volumes": map[string]any{"tank/agentlab/vm-0-disk-1": map[string]any{"type": "volume", "guid": "101", "volsize": "10737418240", "encryption": "off", "contents": "original"}}, "snapshots": map[string]any{}, "log": []any{}}
	writeStorageWorld(t, state, initial)
	return &ShellBackend{Runner: nativeStorageTestRunner{libraries: libraries}, Node: "pve", workspaceCopyStateRoot: filepath.Join(root, "copy-state")}, state
}
func readStorageWorld(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var world map[string]any
	if err := json.Unmarshal(data, &world); err != nil {
		t.Fatal(err)
	}
	return world
}
func writeStorageWorld(t *testing.T, path string, world map[string]any) {
	t.Helper()
	data, err := json.Marshal(world)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeStorageSnapshotsUseInstalledLibraryContract(t *testing.T) {
	backend, state := nativeStorageFixture(t)
	ctx := context.Background()
	source := "local-zfs:vm-0-disk-1"
	if err := backend.VolumeSnapshotCreate(ctx, source, "saved"); err != nil {
		t.Fatal(err)
	}
	world := readStorageWorld(t, state)
	volume := world["volumes"].(map[string]any)["tank/agentlab/vm-0-disk-1"].(map[string]any)
	volume["contents"] = "changed"
	writeStorageWorld(t, state, world)
	if err := backend.VolumeSnapshotRestore(ctx, source, "saved"); err != nil {
		t.Fatal(err)
	}
	world = readStorageWorld(t, state)
	if got := world["volumes"].(map[string]any)["tank/agentlab/vm-0-disk-1"].(map[string]any)["contents"]; got != "original" {
		t.Fatalf("restore contents=%v", got)
	}
	if err := backend.VolumeSnapshotDelete(ctx, source, "saved"); err != nil {
		t.Fatal(err)
	}
	if len(readStorageWorld(t, state)["snapshots"].(map[string]any)) != 0 {
		t.Fatal("snapshot remained after delete")
	}
}

func TestNativeStorageCopyIsIndependentAndCleansTemporarySnapshots(t *testing.T) {
	backend, state := nativeStorageFixture(t)
	target := "local-zfs:vm-900000001-agentlab-workspace-copy"
	receipt, err := backend.CopyWorkspaceVolume(context.Background(), "local-zfs:vm-0-disk-1", "", target)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TargetGUID != "201" || receipt.SourceGUID != "101" || receipt.Cleanup != "verified" {
		t.Fatalf("incomplete receipt: %+v", receipt)
	}
	world := readStorageWorld(t, state)
	if len(world["snapshots"].(map[string]any)) != 0 {
		t.Fatal("temporary snapshots remained")
	}
	volumes := world["volumes"].(map[string]any)
	volumes["tank/agentlab/vm-900000001-agentlab-workspace-copy"].(map[string]any)["contents"] = "fork change"
	if volumes["tank/agentlab/vm-0-disk-1"].(map[string]any)["contents"] != "original" {
		t.Fatal("copy changed source")
	}
	found := false
	for _, raw := range world["log"].([]any) {
		command := raw.([]any)
		if _, pipeline := command[0].([]any); pipeline {
			encoded, _ := json.Marshal(command)
			if string(encoded) == "" {
				t.Fatal("missing copy pipeline")
			}
			found = true
			receiver := command[1].([]any)
			if len(receiver) != 6 || receiver[0] != "/usr/sbin/zfs" || receiver[1] != "receive" || receiver[2] != "-u" {
				t.Fatalf("unexpected receiver: %v", receiver)
			}
		}
	}
	if !found {
		t.Fatal("full stream was not copied")
	}
}

func TestNativeStorageCopyPreservesExistingTargetAndFailureResources(t *testing.T) {
	for _, mode := range []string{"existing", "partial", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			backend, state := nativeStorageFixture(t)
			world := readStorageWorld(t, state)
			targetDataset := "tank/agentlab/vm-900000001-agentlab-workspace-copy"
			switch mode {
			case "existing":
				world["volumes"].(map[string]any)[targetDataset] = map[string]any{"contents": "user data", "guid": "999", "type": "volume"}
			case "partial":
				world["fail_copy"] = true
			case "cleanup":
				world["fail_delete"] = true
			}
			writeStorageWorld(t, state, world)
			receipt, err := backend.CopyWorkspaceVolume(context.Background(), "local-zfs:vm-0-disk-1", "", "local-zfs:vm-900000001-agentlab-workspace-copy")
			if err == nil {
				t.Fatal("failed storage operation claimed success")
			}
			var storageError *StorageOperationError
			if !errors.As(err, &storageError) {
				t.Fatalf("missing receipt: %v", err)
			}
			world = readStorageWorld(t, state)
			if world["volumes"].(map[string]any)["tank/agentlab/vm-0-disk-1"].(map[string]any)["contents"] != "original" {
				t.Fatal("failure changed source")
			}
			target := world["volumes"].(map[string]any)[targetDataset].(map[string]any)
			if mode == "existing" && target["contents"] != "user data" {
				t.Fatal("pre-existing target overwritten")
			}
			if mode != "existing" && (receipt.Cleanup != "inspection_required" || len(world["snapshots"].(map[string]any)) == 0) {
				t.Fatalf("failed copy lost resource accounting: %+v", receipt)
			}
		})
	}
}

const nativeStorageToolsModule = `package PVE::Tools;
use strict; use warnings; use JSON::PP;
sub path { my ($p)=$ENV{AGENTLAB_NATIVE_TEST_STATE}=~m{\A(/[-A-Za-z0-9_./]+)\z}; die 'invalid fixture' unless $p; return $p; }
sub load { open my $f,'<',path() or die $!; local $/; return decode_json(<$f>); }
sub save { my ($w)=@_; open my $f,'>',path() or die $!; print $f encode_json($w); close $f; }
sub run_command {
 my ($cmd,%options)=@_; my $w=load(); push @{$w->{log}},$cmd; save($w);
 if (ref($cmd->[0])) {
  my $snap=$cmd->[0]->[-1]; my $target=$cmd->[1]->[-1]; die 'target exists' if $w->{volumes}->{$target};
  my $copy=decode_json(encode_json($w->{snapshots}->{$snap})); die 'missing snapshot' unless $copy;
  $copy->{type}='volume'; $copy->{guid}='201'; $copy->{origin}='-';
  my ($tag)=$cmd->[1]->[-2]=~/^org[.]agentlab:copy-id=(.+)$/; $copy->{'org.agentlab:copy-id'}=$tag; $w->{volumes}->{$target}=$copy;
  my ($name)=$snap=~/\@(.+)$/; $w->{snapshots}->{$target.'@'.$name}=decode_json(encode_json($w->{snapshots}->{$snap}));
  delete $w->{snapshots}->{$target.'@'.$name} if $w->{partial_copy}; save($w);
  kill 9,$$ if ($w->{exit_after}//'') eq 'receive';
  die 'copy interrupted' if $w->{fail_copy} || $w->{partial_copy}; return;
 }
 my $action=$cmd->[1]; my $dataset=$cmd->[-1];
 if ($action eq 'get') {
  my $v=$w->{volumes}->{$dataset} || $w->{snapshots}->{$dataset}; die 'absent dataset' unless $v;
  if ($dataset eq 'tank/agentlab/vm-0-disk-1' && scalar(keys %{$w->{volumes}})>1 && !keys %{$w->{snapshots}}) { $w->{final_source_reads}++; save($w); kill 9,$$ if ($w->{exit_after}//'') eq 'verified_reply' && $w->{final_source_reads}>=2; }
  for my $key (qw(type guid volsize encryption origin org.agentlab:copy-id)) { $options{outfunc}->($key."\t".($v->{$key}//'-')."\t".($v->{property_origin}//'local')); }
 } elsif ($action eq 'list') {
  my $kind=$cmd->[6];
  my $collection=$kind eq 'snapshot' ? $w->{snapshots} : $w->{volumes};
  for my $name (sort keys %$collection) { $options{outfunc}->($name) if $kind ne 'snapshot' || index($name,$dataset.'@')==0; }
 } elsif ($action eq 'snapshot') {
  die 'snapshot exists' if $w->{snapshots}->{$dataset}; my ($parent)=$dataset=~/^(.*)@/;
  my $v=decode_json(encode_json($w->{volumes}->{$parent})); $v->{type}='snapshot'; $v->{guid}='301';
  my ($tag)=$cmd->[-2]=~/^org[.]agentlab:copy-id=(.+)$/; $v->{'org.agentlab:copy-id'}=$tag;
  $w->{snapshots}->{$dataset}=$v; save($w); kill 9,$$ if ($w->{exit_after}//'') eq 'snapshot';
 } else { die 'unexpected command'; }
}
1;`
const nativeStorageModule = `package PVE::Storage;
use strict; use warnings; use JSON::PP; use PVE::Tools;
sub config { die "RPC environment not initialized" unless $PVE::RPCEnvironment::initialized; return {}; }
sub storage_check_enabled { return {type=>'zfspool',pool=>'tank/agentlab'}; }
sub dataset { my ($id)=@_; $id=~s/^[^:]+://; return 'tank/agentlab/'.$id; }
sub volume_snapshot {
 my ($cfg,$id,$name)=@_; my $w=PVE::Tools::load(); my $dataset=dataset($id); my $key=$dataset.'@'.$name;
 die 'snapshot exists' if $w->{snapshots}->{$key};
 my $copy=decode_json(encode_json($w->{volumes}->{$dataset})); $copy->{type}='snapshot'; $copy->{guid}='301';
 $w->{snapshots}->{$key}=$copy; PVE::Tools::save($w);
}
sub volume_snapshot_rollback {
 my ($cfg,$id,$name)=@_; my $w=PVE::Tools::load(); my $dataset=dataset($id);
 my $saved=$w->{snapshots}->{$dataset.'@'.$name}; die 'missing snapshot' unless $saved;
 $w->{volumes}->{$dataset}->{contents}=$saved->{contents}; PVE::Tools::save($w);
}
sub volume_snapshot_delete {
 my ($cfg,$id,$name,$running)=@_; die 'invalid running flag' unless defined($running) && $running==0;
 my $w=PVE::Tools::load(); die 'cleanup blocked' if $w->{fail_delete};
 my $dataset=dataset($id); my $key=$dataset.'@'.$name; die 'snapshot absent' unless exists $w->{snapshots}->{$key};
 delete $w->{snapshots}->{$key}; PVE::Tools::save($w);
 my $phase=$dataset =~ /agentlab-workspace-copy$/ ? 'target_cleanup' : 'source_cleanup'; kill 9,$$ if ($w->{exit_after}//'') eq $phase;
}
1;`

func TestNativeStorageRefusesWrongNodeAndEncryptedCopy(t *testing.T) {
	for _, mode := range []string{"node", "encryption"} {
		t.Run(mode, func(t *testing.T) {
			backend, state := nativeStorageFixture(t)
			if mode == "node" {
				backend.Node = "other-node"
			} else {
				world := readStorageWorld(t, state)
				world["volumes"].(map[string]any)["tank/agentlab/vm-0-disk-1"].(map[string]any)["encryption"] = "aes-256-gcm"
				writeStorageWorld(t, state, world)
			}
			_, err := backend.CopyWorkspaceVolume(context.Background(), "local-zfs:vm-0-disk-1", "", "local-zfs:vm-900000001-agentlab-workspace-copy")
			if err == nil {
				t.Fatal("unsupported copy ran")
			}
			world := readStorageWorld(t, state)
			if len(world["snapshots"].(map[string]any)) != 0 || len(world["volumes"].(map[string]any)) != 1 {
				t.Fatal("preflight mutated storage")
			}
		})
	}
}

func TestNativeStorageRecoversAfterHelperTermination(t *testing.T) {
	for _, phase := range []string{"snapshot", "receive", "target_cleanup", "source_cleanup", "verified_reply"} {
		t.Run(phase, func(t *testing.T) {
			backend, state := nativeStorageFixture(t)
			world := readStorageWorld(t, state)
			world["exit_after"] = phase
			writeStorageWorld(t, state, world)
			source, target := "local-zfs:vm-0-disk-1", "local-zfs:vm-900000001-agentlab-workspace-copy"
			_, err := backend.CopyWorkspaceVolume(context.Background(), source, "", target)
			if err == nil {
				t.Fatal("helper did not terminate at its native checkpoint")
			}
			world = readStorageWorld(t, state)
			delete(world, "exit_after")
			writeStorageWorld(t, state, world)
			expected := StorageOperation{Version: 1, Operation: "copy", Source: source, Target: target, Snapshot: CopySnapshotName(target)}
			receipt, err := backend.RecoverWorkspaceVolume(context.Background(), expected)
			if err != nil {
				t.Fatalf("recovery failed: %v; receipt=%+v", err, receipt)
			}
			if !receipt.Recovered || receipt.Cleanup != "verified" || receipt.TargetGUID != "201" {
				t.Fatalf("recovery lacked native proof: %+v", receipt)
			}
			world = readStorageWorld(t, state)
			if len(world["volumes"].(map[string]any)) != 2 || len(world["snapshots"].(map[string]any)) != 0 {
				t.Fatal("recovery duplicated a volume or leaked a temporary snapshot")
			}
			copies := 0
			for _, raw := range world["log"].([]any) {
				command := raw.([]any)
				if _, ok := command[0].([]any); ok {
					copies++
				}
			}
			if copies != 1 {
				t.Fatalf("receive executed %d times", copies)
			}
			if _, err := backend.RecoverWorkspaceVolume(context.Background(), receipt); err != nil {
				t.Fatal("verified copy did not replay", err)
			}
		})
	}
}

func TestNativeStorageRecoveryPreservesPartialAndChangedTargets(t *testing.T) {
	for _, mode := range []string{"partial", "changed_guid", "inherited_tag"} {
		t.Run(mode, func(t *testing.T) {
			backend, state := nativeStorageFixture(t)
			world := readStorageWorld(t, state)
			if mode == "partial" {
				world["partial_copy"] = true
			} else {
				world["exit_after"] = "target_cleanup"
			}
			writeStorageWorld(t, state, world)
			source, target := "local-zfs:vm-0-disk-1", "local-zfs:vm-900000001-agentlab-workspace-copy"
			_, err := backend.CopyWorkspaceVolume(context.Background(), source, "", target)
			if err == nil {
				t.Fatal("fixture did not interrupt the copy")
			}
			world = readStorageWorld(t, state)
			delete(world, "exit_after")
			delete(world, "partial_copy")
			volume := world["volumes"].(map[string]any)["tank/agentlab/vm-900000001-agentlab-workspace-copy"].(map[string]any)
			if mode == "changed_guid" {
				volume["guid"] = "999"
				volume["contents"] = "external data"
			}
			if mode == "inherited_tag" {
				volume["property_origin"] = "inherited from tank/agentlab"
			}
			writeStorageWorld(t, state, world)
			expected := StorageOperation{Version: 1, Operation: "copy", Source: source, Target: target, Snapshot: CopySnapshotName(target)}
			_, err = backend.RecoverWorkspaceVolume(context.Background(), expected)
			if err == nil {
				t.Fatal("unverified target was adopted")
			}
			after := readStorageWorld(t, state)
			if len(after["volumes"].(map[string]any)) != 2 || len(after["snapshots"].(map[string]any)) != 1 {
				t.Fatal("blocked recovery changed retained resources")
			}
			if mode == "changed_guid" && after["volumes"].(map[string]any)["tank/agentlab/vm-900000001-agentlab-workspace-copy"].(map[string]any)["contents"] != "external data" {
				t.Fatal("external target data changed")
			}
		})
	}
}

func TestNativeStorageRecoveryKeepsNamedSourceSnapshot(t *testing.T) {
	backend, state := nativeStorageFixture(t)
	ctx := context.Background()
	source, target := "local-zfs:vm-0-disk-1", "local-zfs:vm-900000001-agentlab-workspace-copy"
	if err := backend.VolumeSnapshotCreate(ctx, source, "saved"); err != nil {
		t.Fatal(err)
	}
	world := readStorageWorld(t, state)
	world["exit_after"] = "receive"
	writeStorageWorld(t, state, world)
	if _, err := backend.CopyWorkspaceVolume(ctx, source, "saved", target); err == nil {
		t.Fatal("copy did not terminate")
	}
	world = readStorageWorld(t, state)
	delete(world, "exit_after")
	writeStorageWorld(t, state, world)
	receipt, err := backend.RecoverWorkspaceVolume(ctx, StorageOperation{Version: 1, Operation: "copy-snapshot", Source: source, Snapshot: "saved", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Cleanup != "verified" || !receipt.Recovered {
		t.Fatalf("incomplete receipt: %+v", receipt)
	}
	snapshots := readStorageWorld(t, state)["snapshots"].(map[string]any)
	if len(snapshots) != 1 || snapshots["tank/agentlab/vm-0-disk-1@saved"] == nil {
		t.Fatal("named source snapshot changed")
	}
}

func TestNativeStorageRefusesUnsafeJournalRoot(t *testing.T) {
	for _, mode := range []string{"symlink", "public"} {
		t.Run(mode, func(t *testing.T) {
			backend, state := nativeStorageFixture(t)
			root := backend.workspaceCopyStateRoot
			if mode == "symlink" {
				destination := filepath.Join(filepath.Dir(root), "foreign")
				if err := os.Mkdir(destination, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, root); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(root, 0755); err != nil {
					t.Fatal(err)
				}
			}
			_, err := backend.CopyWorkspaceVolume(context.Background(), "local-zfs:vm-0-disk-1", "", "local-zfs:vm-900000001-agentlab-workspace-copy")
			if err == nil {
				t.Fatal("unsafe journal accepted")
			}
			world := readStorageWorld(t, state)
			if len(world["volumes"].(map[string]any)) != 1 || len(world["snapshots"].(map[string]any)) != 0 {
				t.Fatal("journal preflight changed native resources")
			}
		})
	}
}

func TestNativeStorageRecoveryRejectsRecordedIdentityChanges(t *testing.T) {
	backend, state := nativeStorageFixture(t)
	ctx := context.Background()
	source, target := "local-zfs:vm-0-disk-1", "local-zfs:vm-900000001-agentlab-workspace-copy"
	receipt, err := backend.CopyWorkspaceVolume(ctx, source, "", target)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source", "snapshot", "target"} {
		changed := receipt
		switch field {
		case "source":
			changed.SourceGUID = "999"
		case "snapshot":
			changed.SnapshotGUID = "999"
		case "target":
			changed.TargetGUID = "999"
		}
		if _, err := backend.RecoverWorkspaceVolume(ctx, changed); err == nil {
			t.Fatalf("changed %s identity accepted", field)
		}
	}
	world := readStorageWorld(t, state)
	if world["volumes"].(map[string]any)["tank/agentlab/vm-900000001-agentlab-workspace-copy"].(map[string]any)["guid"] != "201" {
		t.Fatal("identity rejection changed target")
	}
	if _, err := backend.RecoverWorkspaceVolume(ctx, receipt); err != nil {
		t.Fatal("identity rejection changed valid journal", err)
	}
}
