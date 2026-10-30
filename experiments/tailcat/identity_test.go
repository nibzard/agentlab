package tailcatexperiment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityPersistsWithoutSecretStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "identity.json")
	first, err := OpenIdentity(path, "server")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenIdentity(path, "server")
	if err != nil {
		t.Fatal(err)
	}
	if first.PublicKey() != reopened.PublicKey() || !first.record.Shared.Equal(reopened.record.Shared) {
		t.Fatal("identity changed after reopening")
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	private, _ := first.record.Node.MarshalText()
	shared, _ := first.record.Shared.MarshalText()
	for _, data := range [][]byte{raw, []byte(first.String()), []byte(fmt.Sprintf("%#v", first)), []byte(fmt.Sprintf("%#v", *first))} {
		if bytes.Contains(data, private) || bytes.Contains(data, shared) {
			t.Fatal("status exposed transport secrets")
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private identity permissions changed")
	}
	if _, err := OpenIdentity(path, "client"); err == nil {
		t.Fatal("identity role changed without a new key")
	}
}
func TestIdentityPreservesUnexpectedFiles(t *testing.T) {
	for _, mode := range []string{"corrupt", "permissions", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "state")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "identity.json")
			if mode == "symlink" {
				target := filepath.Join(root, "owned-file")
				if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			} else {
				permissions := os.FileMode(0600)
				if mode == "permissions" {
					permissions = 0644
				}
				if err := os.WriteFile(path, []byte("preserve"), permissions); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := OpenIdentity(path, "server"); err == nil {
				t.Fatal("unexpected identity was replaced")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "preserve" {
				t.Fatal("unexpected identity contents changed")
			}
		})
	}
	_, err := OpenIdentity(filepath.Join(t.TempDir(), "identity.json"), strings.Repeat("private", 10))
	if err == nil {
		t.Fatal("unknown role accepted")
	}
}
