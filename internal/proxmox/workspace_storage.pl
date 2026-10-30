use strict;
use warnings;
use JSON::PP;
use Digest::SHA qw(sha256_hex);
use Fcntl qw(:DEFAULT :flock O_NOFOLLOW O_DIRECTORY);
use IO::Handle;
use File::Temp qw(tempfile);
use PVE::Storage;
use PVE::Tools;
use PVE::INotify;
use PVE::RPCEnvironment;
$ENV{PATH} = '/usr/sbin:/usr/bin:/sbin:/bin';
delete @ENV{qw(IFS CDPATH ENV BASH_ENV)};
my ($node, $op, $source, $snapshot, $target, $state_root, $expected_source, $expected_snapshot, $expected_target) = @ARGV;
my $result = { version => 1, operation => $op, source => $source,
    snapshot => $snapshot, target => $target, ok => JSON::PP::false,
    state => 'preflight', cleanup_status => 'not_required' };
sub capture {
    my ($args) = @_;
    my @lines;
    PVE::Tools::run_command($args, outfunc => sub { push @lines, $_[0] },
        errfunc => sub { });
    return @lines;
}
sub properties {
    my ($dataset) = @_;
    my %properties;
    for my $line (capture(['/usr/sbin/zfs', 'get', '-H', '-p', '-o',
            'property,value,source', 'type,guid,volsize,encryption,origin,org.agentlab:copy-id', $dataset])) {
        my ($key, $value, $origin) = split /\t/, $line, 3;
        $properties{$key} = $value;
        $properties{$key . "_source"} = $origin;
    }
    return \%properties;
}
# Inventory must succeed before an absent dataset or snapshot is established.
sub inventory {
    my ($dataset, $kind, $depth) = @_;
    return { map { $_ => 1 } capture(['/usr/sbin/zfs', 'list', '-H', '-o',
        'name', '-t', $kind, '-d', $depth, $dataset]) };
}
sub private_directory {
    my ($path) = @_;
    ($path) = $path =~ m{\A(/[-A-Za-z0-9_./]+)\z};
    die 'invalid state directory' unless defined($path) && $path !~ m{(?:^|/)\.\.?(?:/|$)};
    my $part = '';
    for my $segment (grep { length } split '/', $path) {
        $part .= "/$segment";
        if (!lstat($part)) { mkdir($part, 0700) or die 'cannot create state directory'; lstat($part) or die 'cannot inspect state directory'; }
        die 'unsafe state directory' unless -d _ && !-l _;
    }
    my @info = lstat($path);
    die 'unprotected state directory' unless $info[4] == $> && ($info[2] & 077) == 0;
    return $path;
}
sub private_file {
    my ($file) = @_;
    my @info = stat($file);
    die 'unprotected state file' unless @info && ($info[2] & 0170000) == 0100000 && $info[4] == $> && ($info[2] & 077) == 0 && $info[3] == 1;
}
sub checkpoint {
    my ($root, $path, $record) = @_;
    my ($file, $stage) = tempfile('.copy-XXXXXXXX', DIR => $root, UNLINK => 0);
    private_file($file);
    print $file JSON::PP->new->canonical->encode($record) or die 'cannot write state';
    $file->sync or die 'cannot sync state';
    close($file) or die 'cannot close state';
    rename($stage, $path) or die 'cannot publish state';
    sysopen(my $directory, $root, O_RDONLY | O_DIRECTORY | O_NOFOLLOW) or die 'cannot open state directory';
    $directory->sync or die 'cannot sync state directory';
    close $directory;
    $result->{state} = $record->{state};
    for my $identity (qw(source_guid snapshot_guid target_guid)) { $result->{$identity} = $record->{$identity} if $record->{$identity}; }
}
sub copy_volume {
    my ($cfg, $pool, $dataset, $destination) = @_;
    $result->{code} = 'workspace_copy_journal_unavailable';
    my $root = private_directory($state_root);
    my $key = sha256_hex(join "\0", 'agentlab-copy-v1', $node, $op, $source, $snapshot, $target);
    $result->{journal_id} = $key;
    my $path = "$root/$key.json";
    sysopen(my $lock, "$root/$key.lock", O_RDWR | O_CREAT | O_NOFOLLOW, 0600) or die 'cannot open state lock';
    private_file($lock);
    if (!flock($lock, LOCK_EX | LOCK_NB)) { $result->{code} = 'workspace_copy_active'; die 'copy operation is active'; }
    $result->{code} = 'workspace_copy_identity_changed';
    my $record;
    if (lstat($path)) {
        sysopen(my $file, $path, O_RDONLY | O_NOFOLLOW) or die 'cannot open state';
        private_file($file);
        my $raw = ''; my $count = read($file, $raw, 65537);
        die 'invalid state size' unless defined($count) && $count < 65537;
        close $file;
        $record = decode_json($raw);
        die 'invalid native journal' unless ref($record) eq 'HASH' && $record->{version} == 1 && $record->{state} =~ /\A(planned|snapshot_pending|copying|copied|verified)\z/;
        for my $binding (['node', $node], ['pool', $pool], ['operation', $op],
                ['source', $source], ['snapshot', $snapshot], ['target', $target]) {
            die 'copy binding changed' unless defined($record->{$binding->[0]}) && $record->{$binding->[0]} eq $binding->[1];
        }
        die 'invalid copy identity' unless $record->{nonce} =~ /\A[0-9a-f]{64}\z/;
        $result->{recovered} = JSON::PP::true;
    } else {
        die 'missing native journal for recorded storage identities' if length($expected_target) || length($expected_snapshot);
        if (inventory($pool, 'filesystem,volume', 1)->{$destination}) { $result->{code} = 'workspace_copy_target_exists'; die 'target exists'; }
        my $source_properties = properties($dataset);
        die 'invalid source' unless $source_properties->{type} eq 'volume' && $source_properties->{guid} =~ /\A[0-9]+\z/;
        die 'encrypted copy unsupported' unless $source_properties->{encryption} eq 'off';
        die 'expected source changed' if length($expected_source) && $source_properties->{guid} ne $expected_source;
        sysopen(my $random, '/dev/urandom', O_RDONLY) or die 'cannot create copy identity';
        my $bytes = ''; die 'cannot read copy identity' unless read($random, $bytes, 32) == 32; close $random;
        $record = {version => 1, node => $node, pool => $pool, operation => $op,
            source => $source, snapshot => $snapshot, target => $target,
            nonce => unpack('H*', $bytes), source_guid => $source_properties->{guid},
            state => 'planned'};
        checkpoint($root, $path, $record);
    }
    die 'source journal identity changed' if length($expected_source) && $record->{source_guid} ne $expected_source;
    die 'snapshot journal identity changed' if length($expected_snapshot) && (!$record->{snapshot_guid} || $record->{snapshot_guid} ne $expected_snapshot);
    die 'target journal identity changed' if length($expected_target) && (!$record->{target_guid} || $record->{target_guid} ne $expected_target);
    my ($tag) = $record->{nonce} =~ /\A([0-9a-f]{64})\z/;
    die 'invalid copy ownership' unless defined($tag);
    $result->{source_guid} = $record->{source_guid};
    $result->{cleanup_status} = 'inspection_required';
    my $targets = inventory($pool, 'filesystem,volume', 1);
    if ($record->{state} eq 'verified') {
        die 'verified target absent' unless $targets->{$destination};
        my $existing = properties($destination);
        die 'verified target changed' unless $existing->{type} eq 'volume' && $existing->{guid} eq $record->{target_guid}
            && $existing->{'org.agentlab:copy-id'} eq $tag && $existing->{'org.agentlab:copy-id_source'} eq 'local' && $existing->{origin} eq '-';
        die 'snapshot remains after cleanup' if inventory($destination, 'snapshot', 1)->{"$destination\@$snapshot"};
    } else {
        my $source_properties = properties($dataset);
        die 'source changed' unless $source_properties->{type} eq 'volume' && $source_properties->{guid} eq $record->{source_guid};
        my $source_snapshots = inventory($dataset, 'snapshot', 1);
        my $source_snapshot = "$dataset\@$snapshot";
        if (!$source_snapshots->{$source_snapshot} && !$record->{snapshot_guid}) {
            die 'missing requested snapshot' unless $op eq 'copy';
            $record->{state} = 'snapshot_pending'; checkpoint($root, $path, $record);
            # The tag is assigned in the same native operation as creation.
            PVE::Tools::run_command(['/usr/sbin/zfs', 'snapshot', '-o', "org.agentlab:copy-id=$tag", $source_snapshot], outfunc => sub {}, errfunc => sub {});
        }
        my $saved;
        if ($source_snapshots->{$source_snapshot} || !$record->{snapshot_guid} || $record->{state} ne 'copied') {
            $saved = properties($source_snapshot);
            die 'snapshot identity invalid' unless $saved->{type} eq 'snapshot' && $saved->{guid} =~ /\A[0-9]+\z/;
            die 'temporary snapshot is foreign' if $op eq 'copy' && ($saved->{'org.agentlab:copy-id'} ne $tag || $saved->{'org.agentlab:copy-id_source'} ne 'local');
            die 'snapshot changed' if $record->{snapshot_guid} && $record->{snapshot_guid} ne $saved->{guid};
            $record->{snapshot_guid} = $saved->{guid}; $record->{volsize} = $saved->{volsize};
            checkpoint($root, $path, $record);
        }
        $result->{snapshot_guid} = $record->{snapshot_guid};
        $result->{temporary_snapshot_created} = JSON::PP::true if $op eq 'copy' && $saved;
        if (!$targets->{$destination}) {
            die 'verified copy disappeared' if $record->{target_guid};
            $record->{state} = 'copying'; checkpoint($root, $path, $record);
            PVE::Tools::run_command([
                ['/usr/sbin/zfs', 'send', $source_snapshot],
                ['/usr/sbin/zfs', 'receive', '-u', '-o', "org.agentlab:copy-id=$tag", $destination],
            ], outfunc => sub {}, errfunc => sub {});
        }
        my $received = properties($destination);
        die 'target is foreign or incomplete' unless $received->{type} eq 'volume'
            && $received->{guid} =~ /\A[0-9]+\z/ && $received->{guid} ne $record->{source_guid}
            && $received->{volsize} eq $record->{volsize} && $received->{'org.agentlab:copy-id'} eq $tag
            && $received->{origin} eq '-' && $received->{'org.agentlab:copy-id_source'} eq 'local';
        die 'target changed' if $record->{target_guid} && $record->{target_guid} ne $received->{guid};
        my $received_snapshots = inventory($destination, 'snapshot', 1);
        if ($record->{state} ne 'copied') {
            if (!$received_snapshots->{"$destination\@$snapshot"}) { $result->{code} = 'workspace_copy_incomplete'; die 'copy completion is unknown'; }
            my $received_snapshot = properties("$destination\@$snapshot");
            die 'snapshot stream mismatch' unless $received_snapshot->{guid} eq $record->{snapshot_guid};
            $record->{target_guid} = $received->{guid}; $record->{state} = 'copied';
            checkpoint($root, $path, $record);
        }
        $result->{code} = 'workspace_copy_cleanup_blocked';
        if ($received_snapshots->{"$destination\@$snapshot"}) {
            my $received_snapshot = properties("$destination\@$snapshot");
            die 'cleanup snapshot changed' unless $received_snapshot->{guid} eq $record->{snapshot_guid};
            PVE::Storage::volume_snapshot_delete($cfg, $target, $snapshot, 0);
        }
        if ($op eq 'copy') {
            $source_snapshots = inventory($dataset, 'snapshot', 1);
            if ($source_snapshots->{$source_snapshot}) {
                my $current = properties($source_snapshot);
                die 'source cleanup identity changed' unless $current->{guid} eq $record->{snapshot_guid} && $current->{'org.agentlab:copy-id'} eq $tag && $current->{'org.agentlab:copy-id_source'} eq 'local';
                PVE::Storage::volume_snapshot_delete($cfg, $source, $snapshot, 0);
            }
        }
        die 'target snapshot cleanup unverified' if inventory($destination, 'snapshot', 1)->{"$destination\@$snapshot"};
        die 'source snapshot cleanup unverified' if $op eq 'copy' && inventory($dataset, 'snapshot', 1)->{$source_snapshot};
        my $after = properties($dataset);
        die 'source identity changed' unless $after->{guid} eq $record->{source_guid};
        $record->{state} = 'verified'; checkpoint($root, $path, $record);
    }
    $result->{source_guid} = $record->{source_guid};
    $result->{snapshot_guid} = $record->{snapshot_guid};
    $result->{target_guid} = $record->{target_guid};
    $result->{cleanup_status} = 'verified';
    $result->{temporary_snapshot_created} = JSON::PP::false;
    delete $result->{code};
}
my $success = eval {
    die 'invalid arguments' unless @ARGV == 9;
    for my $guid ($expected_source, $expected_snapshot, $expected_target) { die 'invalid expected identity' unless $guid =~ /\A[0-9]*\z/; }
    ($node) = $node =~ /\A([A-Za-z0-9][A-Za-z0-9_.-]*)\z/;
    die 'wrong node' unless defined($node) && $node eq PVE::INotify::nodename();
    ($op) = $op =~ /\A(snapshot|restore|delete|copy|copy-snapshot)\z/;
    die 'invalid operation' unless defined($op);
    my ($store, $volume) = $source =~ /\A([A-Za-z][A-Za-z0-9_-]*):(vm-[0-9]+-[A-Za-z0-9_.-]+)\z/;
    die 'invalid source' unless defined($store) && defined($volume);
    $source = "$store:$volume";
    ($snapshot) = $snapshot =~ /\A([A-Za-z0-9][A-Za-z0-9_.-]{0,63})\z/;
    die 'invalid snapshot' unless defined($snapshot);
    PVE::RPCEnvironment->setup_default_cli_env();
    my $cfg = PVE::Storage::config();
    my $storage = PVE::Storage::storage_check_enabled($cfg, $store, $node);
    if ($storage->{type} ne 'zfspool') { $result->{code} = 'workspace_storage_unsupported'; die 'unsupported storage'; }
    my ($pool) = $storage->{pool} =~ /\A([A-Za-z][A-Za-z0-9_.:-]*(?:\/[A-Za-z0-9][A-Za-z0-9_.:-]*)*)\z/;
    die 'invalid pool' unless defined($pool);
    my $dataset = "$pool/$volume";
    my $source_properties = properties($dataset);
    die 'not a volume' unless $source_properties->{type} eq 'volume';
    die 'invalid source identity' unless $source_properties->{guid} =~ /\A[0-9]+\z/;
    $result->{source_guid} = $source_properties->{guid};
    if ($op eq 'snapshot') {
        $result->{state} = 'mutating';
        PVE::Storage::volume_snapshot($cfg, $source, $snapshot);
        my $saved = properties("$dataset\@$snapshot");
        die 'snapshot not verified' unless $saved->{type} eq 'snapshot' && $saved->{guid} =~ /\A[0-9]+\z/;
        $result->{snapshot_guid} = $saved->{guid};
    } elsif ($op eq 'restore') {
        $result->{state} = 'mutating';
        PVE::Storage::volume_snapshot_rollback($cfg, $source, $snapshot);
    } elsif ($op eq 'delete') {
        $result->{state} = 'mutating';
        PVE::Storage::volume_snapshot_delete($cfg, $source, $snapshot, 0);
    } else {
        my ($target_store, $target_volume) = $target =~ /\A([A-Za-z][A-Za-z0-9_-]*):(vm-[1-9][0-9]{2,8}-agentlab-[A-Za-z0-9_-]+)\z/;
        die 'invalid target' unless defined($target_store) && $target_store eq $store;
        die 'same volume' if $source eq $target;
        $target = "$target_store:$target_volume";
        if ($source_properties->{encryption} ne 'off') { $result->{code} = 'workspace_storage_encryption_unsupported'; die 'encrypted copy unsupported'; }
        my $destination = "$pool/$target_volume";
        copy_volume($cfg, $pool, $dataset, $destination);
    }
    my $after = properties($dataset);
    die 'source identity changed' unless $after->{guid} eq $source_properties->{guid};
    $result->{state} = 'verified';
    $result->{ok} = JSON::PP::true;
    1;
};
if (!$success) {
    $result->{code} //= 'workspace_storage_operation_failed';
    # Preserve structured resource metadata. Raw command diagnostics can contain
    # server details and are not part of the public protocol.
}
print JSON::PP->new->canonical->encode($result);
