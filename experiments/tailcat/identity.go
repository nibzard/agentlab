package tailcatexperiment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// Identity keeps transport secrets out of normal status formatting. The disk
// record is private; public status contains only the node public key and role.
type Identity struct{ record identityRecord }
type identityRecord struct {
	Version int                  `json:"version"`
	Role    string               `json:"role"`
	Node    key.NodePrivate      `json:"node_private"`
	Shared  tailcat.PresharedKey `json:"preshared_key"`
}

func (identity Identity) PublicKey() key.NodePublic { return identity.record.Node.Public() }
func (identity Identity) String() string {
	return "Tailcat " + identity.record.Role + " identity " + identity.PublicKey().String()
}
func (identity Identity) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"version": 1, "role": identity.record.Role, "public_key": identity.PublicKey().String()})
}
func privateIdentityFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return errors.New("cannot inspect the private identity file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.New("preserve the unexpected file and restore a private owned identity")
	}
	return nil
}

// OpenIdentity loads or creates a private persistent identity before networking.
// It never replaces an existing key or changes its role after a failed read.
func OpenIdentity(filename, role string) (*Identity, error) {
	if !filepath.IsAbs(filename) || (role != "server" && role != "client") {
		return nil, errors.New("provide an absolute identity path and server or client role")
	}
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, errors.New("cannot create the private identity directory")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("select a private regular identity directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("the identity directory must belong to the current user")
	}
	lock, err := os.OpenFile(filename+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, errors.New("cannot open the private identity lock")
	}
	defer lock.Close()
	if err := privateIdentityFile(lock); err != nil {
		return nil, err
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return nil, errors.New("another process owns this identity operation")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	file, err := os.OpenFile(filename, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err == nil {
		defer file.Close()
		if err := privateIdentityFile(file); err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(file, 4097))
		if err != nil || len(data) > 4096 {
			return nil, errors.New("invalid private identity record")
		}
		var record identityRecord
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var extra any
		if decoder.Decode(&record) != nil || decoder.Decode(&extra) != io.EOF || record.Version != 1 || record.Role != role || record.Node.IsZero() || record.Shared.IsZero() {
			return nil, errors.New("preserve the invalid or mismatched identity record")
		}
		return &Identity{record: record}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("preserve the unreadable identity file before retrying")
	}
	record := identityRecord{Version: 1, Role: role, Node: key.NewNode(), Shared: tailcat.NewPresharedKey()}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, errors.New("cannot encode the private identity")
	}
	stage, err := os.CreateTemp(directory, ".identity-")
	if err != nil {
		return nil, errors.New("cannot stage the private identity")
	}
	defer os.Remove(stage.Name())
	if _, err := stage.Write(data); err != nil {
		stage.Close()
		return nil, errors.New("cannot write the private identity")
	}
	if err := stage.Sync(); err != nil {
		stage.Close()
		return nil, errors.New("cannot sync the private identity")
	}
	if err := stage.Close(); err != nil {
		return nil, errors.New("cannot close the private identity")
	}
	// Linking publishes without overwriting any unexpected entry.
	if err := os.Link(stage.Name(), filename); err != nil {
		return nil, errors.New("identity publication stopped; preserve the existing record")
	}
	if err := os.Remove(stage.Name()); err != nil {
		return nil, errors.New("identity is saved but its private stage needs inspection")
	}
	parent, err := os.Open(directory)
	if err != nil {
		return nil, errors.New("identity is saved but directory durability is unverified")
	}
	defer parent.Close()
	if parent.Sync() != nil {
		return nil, errors.New("identity is saved but directory durability is unverified")
	}
	return &Identity{record: record}, nil
}

func (identity Identity) GoString() string { return identity.String() }

func (identity *Identity) Listen(ctx context.Context, allowed []key.NodePublic, region *tailcfg.DERPRegion) (*tailcat.Server, net.Listener, error) {
	if identity.record.Role != "server" {
		return nil, nil, errors.New("use a server identity to listen")
	}
	return Listen(ctx, identity.record.Node, identity.record.Shared, allowed, region)
}
func (identity *Identity) Client(peer tailcat.Addr) (*tailcat.Client, error) {
	if identity.record.Role != "client" || peer == "" {
		return nil, errors.New("use a client identity and protected peer address")
	}
	return &tailcat.Client{Server: peer, Key: identity.record.Node, Logf: func(string, ...any) {}}, nil
}
