package integrations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentlab/agentlab/internal/db"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	integStore, err := NewStore(store, key)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	cleanup := func() {
		store.Close()
		os.RemoveAll(dir)
	}
	return integStore, cleanup
}

func TestStoreCreateAndGet(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	integ := &Integration{
		Name:       "myapi",
		Type:       TypeHTTPProxy,
		Target:     "https://api.example.com",
		Secret:     "sk-test-secret-12345",
		SecretType: "bearer",
		AttachMode: AttachAutoAll,
	}

	if err := s.Create(ctx, integ); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if integ.ID == 0 {
		t.Error("Create() did not set ID")
	}

	got, err := s.Get(ctx, "myapi")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Name != "myapi" {
		t.Errorf("Get() name = %v, want myapi", got.Name)
	}
	if got.Secret != "sk-test-secret-12345" {
		t.Errorf("Get() secret = %v, want sk-test-secret-12345", got.Secret)
	}
	if got.Type != TypeHTTPProxy {
		t.Errorf("Get() type = %v, want http-proxy", got.Type)
	}
	if got.Target != "https://api.example.com" {
		t.Errorf("Get() target = %v, want https://api.example.com", got.Target)
	}
	if got.AttachMode != AttachAutoAll {
		t.Errorf("Get() attach_mode = %v, want auto:all", got.AttachMode)
	}
}

func TestStoreDuplicateName(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	integ := &Integration{
		Name:       "myapi",
		Type:       TypeHTTPProxy,
		Target:     "https://api.example.com",
		Secret:     "sk-test",
		AttachMode: AttachAutoAll,
	}
	if err := s.Create(ctx, integ); err != nil {
		t.Fatalf("first Create(): %v", err)
	}

	integ2 := &Integration{
		Name:       "myapi",
		Type:       TypeGitProxy,
		Secret:     "ghp-test",
		AttachMode: AttachAutoAll,
	}
	err := s.Create(ctx, integ2)
	if err != ErrDuplicateName {
		t.Errorf("second Create() error = %v, want ErrDuplicateName", err)
	}
}

func TestStoreList(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	integ1 := &Integration{
		Name:       "api1",
		Type:       TypeHTTPProxy,
		Target:     "https://api1.example.com",
		Secret:     "sk-1",
		AttachMode: AttachAutoAll,
	}
	integ2 := &Integration{
		Name:       "api2",
		Type:       TypeHTTPProxy,
		Target:     "https://api2.example.com",
		Secret:     "sk-2",
		AttachMode: AttachSandbox,
		AttachSelector: "mybox",
	}
	if err := s.Create(ctx, integ1); err != nil {
		t.Fatalf("Create api1: %v", err)
	}
	if err := s.Create(ctx, integ2); err != nil {
		t.Fatalf("Create api2: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() count = %d, want 2", len(list))
	}
	// Should be sorted by name
	if list[0].Name != "api1" || list[1].Name != "api2" {
		t.Errorf("List() order = %s, %s; want api1, api2", list[0].Name, list[1].Name)
	}
	// Verify secrets are decrypted
	if list[0].Secret != "sk-1" {
		t.Errorf("List()[0] secret = %v, want sk-1", list[0].Secret)
	}
}

func TestStoreDelete(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	integ := &Integration{
		Name:       "myapi",
		Type:       TypeHTTPProxy,
		Target:     "https://api.example.com",
		Secret:     "sk-test",
		AttachMode: AttachAutoAll,
	}
	if err := s.Create(ctx, integ); err != nil {
		t.Fatalf("Create(): %v", err)
	}

	if err := s.Delete(ctx, "myapi"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	_, err := s.Get(ctx, "myapi")
	if err != ErrNotFound {
		t.Errorf("Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestStoreDeleteNotFound(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	err := s.Delete(context.Background(), "nonexistent")
	if err != ErrNotFound {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

func TestStoreGetNotFound(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	_, err := s.Get(context.Background(), "nonexistent")
	if err != ErrNotFound {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestStoreListForSandbox(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create integrations with different attachment modes.
	autoInteg := &Integration{
		Name:       "auto-api",
		Type:       TypeHTTPProxy,
		Target:     "https://api.example.com",
		Secret:     "sk-auto",
		AttachMode: AttachAutoAll,
	}
	sandboxInteg := &Integration{
		Name:           "box-api",
		Type:           TypeHTTPProxy,
		Target:         "https://box.example.com",
		Secret:         "sk-box",
		AttachMode:     AttachSandbox,
		AttachSelector: "mybox",
	}
	tagInteg := &Integration{
		Name:           "tag-api",
		Type:           TypeHTTPProxy,
		Target:         "https://tag.example.com",
		Secret:         "sk-tag",
		AttachMode:     AttachTag,
		AttachSelector: "production",
	}
	otherSandboxInteg := &Integration{
		Name:           "other-api",
		Type:           TypeHTTPProxy,
		Target:         "https://other.example.com",
		Secret:         "sk-other",
		AttachMode:     AttachSandbox,
		AttachSelector: "otherbox",
	}

	for _, i := range []*Integration{autoInteg, sandboxInteg, tagInteg, otherSandboxInteg} {
		if err := s.Create(ctx, i); err != nil {
			t.Fatalf("Create %s: %v", i.Name, err)
		}
	}

	// Sandbox "mybox" with tag "production" should match auto, box-api, and tag-api.
	matched, err := s.ListForSandbox(ctx, "mybox", []string{"production"})
	if err != nil {
		t.Fatalf("ListForSandbox() error: %v", err)
	}
	if len(matched) != 3 {
		t.Fatalf("ListForSandbox() count = %d, want 3", len(matched))
	}

	names := make(map[string]bool)
	for _, m := range matched {
		names[m.Name] = true
	}
	for _, want := range []string{"auto-api", "box-api", "tag-api"} {
		if !names[want] {
			t.Errorf("expected %s in matched results", want)
		}
	}
	if names["other-api"] {
		t.Error("other-api should not match sandbox mybox")
	}
}

func TestEncryptionKeyHex(t *testing.T) {
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey(): %v", err)
	}
	hexKey := EncryptionKeyHex(key)
	parsed, err := ParseEncryptionKeyHex(hexKey)
	if err != nil {
		t.Fatalf("ParseEncryptionKeyHex(): %v", err)
	}
	if len(parsed) != 32 {
		t.Errorf("parsed key length = %d, want 32", len(parsed))
	}
	for i := range key {
		if key[i] != parsed[i] {
			t.Error("parsed key does not match original")
			break
		}
	}
}

func TestEncryptionKeyHexInvalid(t *testing.T) {
	_, err := ParseEncryptionKeyHex("not-valid-hex")
	if err == nil {
		t.Error("expected error for invalid hex")
	}

	_, err = ParseEncryptionKeyHex("00")
	if err == nil {
		t.Error("expected error for short key")
	}
}

func TestNewStoreInvalidKey(t *testing.T) {
	_, err := NewStore(nil, []byte("short"))
	if err == nil {
		t.Error("expected error for short key")
	}
}
