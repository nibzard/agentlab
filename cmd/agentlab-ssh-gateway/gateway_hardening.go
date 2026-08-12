//go:build sshgateway
// +build sshgateway

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// activityTracker records the last time bidirectional activity was observed on
// an SSH connection. The idle watchdog closes a connection whose tracker has
// not been touched within the configured idle deadline (review M7).
type activityTracker struct {
	last atomic.Int64
}

func newActivityTracker() *activityTracker {
	a := &activityTracker{}
	a.touch()
	return a
}

func (a *activityTracker) touch() {
	a.last.Store(time.Now().UnixNano())
}

func (a *activityTracker) idleFor() time.Duration {
	return time.Since(time.Unix(0, a.last.Load()))
}

// watchIdle periodically closes conn when no activity has been observed for
// longer than idle. It returns once the connection is closed or the gateway is
// shutting down (ctx canceled). The connection's own Close triggers the
// watchdog to exit via the conn-close signal handled by the caller.
func (s *server) watchIdle(conn *ssh.ServerConn, tracker *activityTracker, done <-chan struct{}) {
	if s.cfg.idleTimeout <= 0 {
		return
	}
	interval := s.cfg.idleTimeout / 2
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if tracker.idleFor() > s.cfg.idleTimeout {
				s.logger.Printf("closing idle session from %s after %s of inactivity",
					conn.RemoteAddr(), s.cfg.idleTimeout)
				_ = conn.Close()
				return
			}
		}
	}
}

// trackedChannel wraps an ssh.Channel so reads and writes refresh the
// connection's activity tracker, making the idle deadline bidirectional
// (review M7): either client keystrokes or sandbox output resets it.
type trackedChannel struct {
	ssh.Channel
	tracker *activityTracker
}

func (t *trackedChannel) Read(p []byte) (int, error) {
	n, err := t.Channel.Read(p)
	if n > 0 {
		t.tracker.touch()
	}
	return n, err
}

func (t *trackedChannel) Write(p []byte) (int, error) {
	n, err := t.Channel.Write(p)
	if n > 0 {
		t.tracker.touch()
	}
	return n, err
}

// hostKeyPinStore records the first observed sandbox host key for each VMID
// (TOFU) and rejects any later differing key, so a host reusing an agent-subnet
// address cannot impersonate the target sandbox (review M7).
//
// Rotation policy: a freshly allocated VMID has no pin, so its first-presented
// key is recorded. An existing VMID whose VM is recreated must have its pin
// cleared via rotate() before the new key is accepted; otherwise the mismatch
// is treated as impersonation and the connection is refused.
type hostKeyPinStore struct {
	mu   sync.Mutex
	pins map[int]string // vmid -> marshaled public key wire format
}

func newHostKeyPinStore() *hostKeyPinStore {
	return &hostKeyPinStore{pins: make(map[int]string)}
}

// callback returns an ssh.HostKeyCallback that enforces the per-VMID pin.
func (s *hostKeyPinStore) callback(vmid int, logger *log.Logger) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		wire := string(key.Marshal())
		s.mu.Lock()
		defer s.mu.Unlock()
		existing, ok := s.pins[vmid]
		if !ok {
			s.pins[vmid] = wire
			if logger != nil {
				logger.Printf("pinning sandbox host key for vmid %d (%s)", vmid, ssh.FingerprintSHA256(key))
			}
			return nil
		}
		if existing != wire {
			expected := "unknown"
			if pub, err := ssh.ParsePublicKey([]byte(existing)); err == nil {
				expected = ssh.FingerprintSHA256(pub)
			}
			return fmt.Errorf("sandbox host key for vmid %d changed: expected %s, got %s (possible impersonation)",
				vmid, expected, ssh.FingerprintSHA256(key))
		}
		return nil
	}
}

// rotate drops the pinned host key for a VMID so a recreated VM may present a
// fresh key (review M7 rotation policy).
func (s *hostKeyPinStore) rotate(vmid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pins, vmid)
}

// hasPin reports whether a host key is already pinned for the VMID.
func (s *hostKeyPinStore) hasPin(vmid int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pins[vmid]
	return ok
}

// generateHostSignerPEM creates a fresh ed25519 key pair and returns both the
// SSH signer and the OpenSSH-format PEM bytes (for persistence).
func generateHostSignerPEM() (ssh.Signer, []byte, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate host key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("create host signer: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal host key: %w", err)
	}
	return signer, pem.EncodeToMemory(block), nil
}

// writePrivateKeyAtomic writes private-key bytes to path via a temp file and
// rename, always with mode 0600, so a generated gateway identity is persisted
// safely and survives restart (review M7).
func writePrivateKeyAtomic(path string, data []byte) error {
	if path == "" {
		return fmt.Errorf("host key path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	// Chmod after write in case the umask loosened the mode.
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
