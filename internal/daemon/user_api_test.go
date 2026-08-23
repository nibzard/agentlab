package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentlab/agentlab/internal/user"
)

// addUserKeyResponse is the JSON body that addUserKey returns.
type addUserKeyResponse struct {
	Status      string `json:"status"`
	Fingerprint string `json:"fingerprint"`
}

// postUserKey drives the addUserKey handler directly and returns the HTTP
// status code with the decoded response body.
func postUserKey(t *testing.T, api *UserAPI, userName, key string) (int, addUserKeyResponse) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"key": key})
	if err != nil {
		t.Fatalf("marshal key body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/users/"+userName+"/keys", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.addUserKey(rec, req, userName)
	var decoded addUserKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return rec.Code, decoded
}

// TestAddUserKeyFingerprint verifies that addUserKey parses the SSH public key
// and stores the real SHA256 fingerprint, not the raw key line (review T38).
func TestAddUserKeyFingerprint(t *testing.T) {
	store := newTestStore(t)
	registry := user.NewRegistry(user.NewStore(store))
	api := NewUserAPI(registry)

	ctx := context.Background()

	// Create a test user with a valid ed25519 key.
	initialKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPSPxu+tRlshDnHNEutP6HI+rleoK9CLSV4Gc6jASu2E test@example.com"
	testUser, err := registry.AddUser(ctx, "testuser", initialKey, user.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Test key: another valid ed25519 public key.
	testKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJClvqWh9q+SJCw4CgR4mSDfvqZQRLsjQLqSLKqYqbv3 test@example.com"

	// Add the key through the HTTP handler.
	code, addResp := postUserKey(t, api, "testuser", testKey)
	if code != http.StatusOK {
		t.Fatalf("add key: got %d, want 200", code)
	}
	fingerprint := addResp.Fingerprint

	// Verify fingerprint is in SHA256:abc format, not the raw key.
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Errorf("fingerprint %q does not start with SHA256:", fingerprint)
	}

	// Retrieve the stored keys for the user.
	keys, err := registry.Store().ListSSHKeys(ctx, testUser.ID)
	if err != nil {
		t.Fatalf("list SSH keys: %v", err)
	}

	if len(keys) != 2 {
		t.Fatalf("expected 2 keys (initial + added), got %d", len(keys))
	}

	// Find the newly added key.
	var storedKey *user.SSHKey
	for _, k := range keys {
		if k.Fingerprint == fingerprint {
			storedKey = &k
			break
		}
	}
	if storedKey == nil {
		t.Fatalf("added key with fingerprint %q not found in stored keys", fingerprint)
	}

	if storedKey.Fingerprint != fingerprint {
		t.Errorf("stored fingerprint %q != returned fingerprint %q", storedKey.Fingerprint, fingerprint)
	}

	// Verify we can remove the key by its fingerprint.
	removeReq := httptest.NewRequest("DELETE", "/v1/users/testuser/keys?fingerprint="+fingerprint, nil)
	removeRec := httptest.NewRecorder()
	api.removeUserKey(removeRec, removeReq.WithContext(ctx), "testuser")

	if removeRec.Code != http.StatusOK {
		t.Errorf("remove by fingerprint: got %d, want 200; body %s", removeRec.Code, removeRec.Body.String())
	}

	// Verify the key was removed.
	keysAfter, err := registry.Store().ListSSHKeys(ctx, testUser.ID)
	if err != nil {
		t.Fatalf("list SSH keys after removal: %v", err)
	}

	if len(keysAfter) != 1 {
		t.Errorf("expected 1 key after removal, got %d", len(keysAfter))
	}
}

// TestAddUserKeyRejectsInvalidKey verifies that malformed SSH keys are rejected.
func TestAddUserKeyRejectsInvalidKey(t *testing.T) {
	store := newTestStore(t)
	registry := user.NewRegistry(user.NewStore(store))
	api := NewUserAPI(registry)

	ctx := context.Background()

	// Create a test user with a valid key.
	initialKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPSPxu+tRlshDnHNEutP6HI+rleoK9CLSV4Gc6jASu2E test@example.com"
	_, err := registry.AddUser(ctx, "testuser", initialKey, user.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Invalid keys that should be rejected.
	invalidKeys := []string{
		"not-a-valid-key",
		"ssh-rsa",
		"",
		"BEGIN PUBLIC KEY", // Wrong format
	}

	for _, badKey := range invalidKeys {
		code, _ := postUserKey(t, api, "testuser", badKey)
		if code != http.StatusBadRequest {
			t.Errorf("invalid key %q: got %d, want 400", badKey, code)
		}
	}
}

// TestAddUserKeyHTTPRejectsInvalidKey verifies the HTTP handler rejects bad keys.
func TestAddUserKeyHTTPRejectsInvalidKey(t *testing.T) {
	store := newTestStore(t)
	registry := user.NewRegistry(user.NewStore(store))
	api := NewUserAPI(registry)
	mux := http.NewServeMux()
	api.Register(mux)

	ctx := context.Background()

	// Create a test user with a valid key.
	initialKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPSPxu+tRlshDnHNEutP6HI+rleoK9CLSV4Gc6jASu2E test@example.com"
	_, err := registry.AddUser(ctx, "testuser", initialKey, user.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Try adding an invalid key via HTTP.
	req := httptest.NewRequest("POST", "/v1/users/testuser/keys", strings.NewReader(`{"key":"not-a-valid-key"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid key via HTTP: got %d, want 400; body %s", rec.Code, rec.Body.String())
	}
}
