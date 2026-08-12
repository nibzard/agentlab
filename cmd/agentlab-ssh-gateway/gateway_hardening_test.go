//go:build sshgateway

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// fakeChannel is a minimal ssh.Channel used to exercise trackedChannel without
// spinning up a full SSH transport.
type fakeChannel struct {
	rw io.ReadWriter
}

func (f *fakeChannel) Read(p []byte) (int, error)  { return f.rw.Read(p) }
func (f *fakeChannel) Write(p []byte) (int, error) { return f.rw.Write(p) }
func (f *fakeChannel) Close() error                { return nil }
func (f *fakeChannel) CloseWrite() error           { return nil }
func (f *fakeChannel) SendRequest(string, bool, []byte) (bool, error) {
	return false, nil
}
func (f *fakeChannel) Stderr() io.ReadWriter { return f.rw }

// TestTrackedChannel_BidirectionalActivityResetsIdle proves that both reads
// (sandbox output) and writes (client keystrokes) refresh the idle deadline, so
// an otherwise-idle-but-active session is not terminated (review M7).
func TestTrackedChannel_BidirectionalActivityResetsIdle(t *testing.T) {
	tracker := newActivityTracker()
	// Simulate a connection that has been idle well past the deadline.
	tracker.last.Store(time.Now().Add(-10 * time.Minute).UnixNano())
	require.Greater(t, tracker.idleFor(), 9*time.Minute, "precondition: connection is idle")

	ch := &trackedChannel{Channel: &fakeChannel{rw: bytes.NewBuffer(nil)}, tracker: tracker}

	// A write (e.g. client keystrokes forwarded downstream) resets the clock.
	_, err := ch.Write([]byte("ls -la\r"))
	require.NoError(t, err)
	assert.Less(t, tracker.idleFor(), time.Second, "write must reset idle deadline")

	// Simulate idle again, then a read (sandbox output) must also reset it.
	tracker.last.Store(time.Now().Add(-10 * time.Minute).UnixNano())
	in := bytes.NewBufferString("hello from sandbox")
	tc := &trackedChannel{Channel: &fakeChannel{rw: in}, tracker: tracker}
	buf := make([]byte, 32)
	n, err := tc.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "hello from sandbox", string(buf[:n]))
	assert.Less(t, tracker.idleFor(), time.Second, "read must reset idle deadline")
}

// TestLoadHostSigner_GeneratedIdentitySurvivesRestart proves that a missing
// gateway host key is generated and atomically persisted (mode 0600), and that a
// subsequent start loads the same identity rather than generating a new one
// (review M7).
func TestLoadHostSigner_GeneratedIdentitySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "ssh_gateway_host_ed25519")
	logger := log.New(io.Discard, "", 0)

	signer1, err := loadHostSigner(path, logger)
	require.NoError(t, err)
	fp1 := ssh.FingerprintSHA256(signer1.PublicKey())

	// File persisted with mode 0600.
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "host key must be 0600")

	// A restart must load the persisted identity, not mint a new one.
	signer2, err := loadHostSigner(path, logger)
	require.NoError(t, err)
	assert.Equal(t, fp1, ssh.FingerprintSHA256(signer2.PublicKey()),
		"gateway identity must survive restart")
}

// TestLoadHostSigner_EphemeralWhenPathEmpty preserves the documented behavior
// that an empty path yields an in-memory key without touching the filesystem.
func TestLoadHostSigner_EphemeralWhenPathEmpty(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	signer, err := loadHostSigner("", logger)
	require.NoError(t, err)
	assert.NotNil(t, signer.PublicKey())
}

// TestHostKeyPinStore_TOFUAndRotation proves the per-VMID host-key rotation
// policy (review M7):
//   - the first key observed for a VMID is pinned (TOFU),
//   - a differing key for the same VMID is rejected as impersonation,
//   - rotate() drops the pin so a recreated VM may present a fresh key,
//   - pins are independent per VMID.
func TestHostKeyPinStore_TOFUAndRotation(t *testing.T) {
	pins := newHostKeyPinStore()
	logger := log.New(io.Discard, "", 0)
	vmid := 4011

	keyA := testSSHPublicKey(t)
	keyB := testSSHPublicKey(t)
	require.NotEqual(t,
		ssh.FingerprintSHA256(keyA), ssh.FingerprintSHA256(keyB),
		"precondition: distinct keys")

	cb := pins.callback(vmid, logger)

	// First observed key is pinned (TOFU).
	require.NoError(t, cb("sbx:22", nil, keyA))
	assert.True(t, pins.hasPin(vmid))

	// A different host presenting a different key is rejected.
	err := cb("sbx:22", nil, keyB)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "changed")

	// The originally pinned key is still accepted.
	require.NoError(t, cb("sbx:22", nil, keyA))

	// After rotate(), a recreated VM may present a fresh key.
	pins.rotate(vmid)
	assert.False(t, pins.hasPin(vmid))
	require.NoError(t, cb("sbx:22", nil, keyB), "post-rotation new key must pin")
	assert.True(t, pins.hasPin(vmid))

	// And the old key is now the one rejected.
	require.Error(t, cb("sbx:22", nil, keyA))
}

// TestHostKeyPinStore_IndependentPerVMID proves that pinning keyA to one VMID
// does not constrain a different VMID, which must pin its own first-seen key.
func TestHostKeyPinStore_IndependentPerVMID(t *testing.T) {
	pins := newHostKeyPinStore()
	logger := log.New(io.Discard, "", 0)
	keyA := testSSHPublicKey(t)

	require.NoError(t, pins.callback(100, logger)("a:22", nil, keyA))
	// A different VMID is unpinned; its first key (even keyA) is accepted.
	require.NoError(t, pins.callback(200, logger)("b:22", nil, keyA))
	assert.True(t, pins.hasPin(100))
	assert.True(t, pins.hasPin(200))
}

// TestWritePrivateKeyAtomic_Mode0600 covers the atomic-write helper directly,
// including that the parent directory is created with mode 0700.
func TestWritePrivateKeyAtomic_Mode0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "key")
	require.NoError(t, writePrivateKeyAtomic(path, []byte("secret")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("secret"), data)

	// No leftover temp file.
	_, err = os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err), "temp file must be renamed away")
}

func testSSHPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	return sshPub
}
