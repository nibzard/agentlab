// Package testing provides shared test utilities for agentlab.
package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/agentlab/agentlab/internal/models"
)

// MockProxmoxBackend is a mock implementation of a Proxmox backend for testing.
type MockProxmoxBackend struct {
	mu                sync.Mutex
	VMs               map[int]*MockVM
	NextVMID          int
	CreateDelay       time.Duration
	CreateError       error
	ShouldFailCreate  bool
	ShouldFailStart   bool
	ShouldFailDestroy bool
}

// MockVM represents a mock virtual machine.
type MockVM struct {
	VMID        int
	Name        string
	State       string
	IP          string
	Profile     string
	CreatedAt   time.Time
	StartedAt   *time.Time
	DestroyedAt *time.Time
}

// NewMockProxmoxBackend creates a new mock Proxmox backend.
func NewMockProxmoxBackend() *MockProxmoxBackend {
	return &MockProxmoxBackend{
		VMs:      make(map[int]*MockVM),
		NextVMID: 100,
	}
}

// CreateVM simulates creating a VM.
func (m *MockProxmoxBackend) CreateVM(ctx context.Context, name string, profile string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.CreateDelay > 0 {
		select {
		case <-time.After(m.CreateDelay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}

	if m.ShouldFailCreate || m.CreateError != nil {
		return 0, m.CreateError
	}

	vmid := m.NextVMID
	m.NextVMID++

	m.VMs[vmid] = &MockVM{
		VMID:      vmid,
		Name:      name,
		State:     string(models.SandboxProvisioning),
		Profile:   profile,
		CreatedAt: time.Now(),
	}

	return vmid, nil
}

// StartVM simulates starting a VM.
func (m *MockProxmoxBackend) StartVM(ctx context.Context, vmid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	vm, exists := m.VMs[vmid]
	if !exists {
		return fmt.Errorf("vm %d not found", vmid)
	}

	if m.ShouldFailStart {
		return fmt.Errorf("failed to start vm %d", vmid)
	}

	now := time.Now()
	vm.StartedAt = &now
	vm.State = string(models.SandboxReady)
	vm.IP = fmt.Sprintf("10.77.0.%d", vmid%100)

	return nil
}

// StopVM simulates stopping a VM.
func (m *MockProxmoxBackend) StopVM(ctx context.Context, vmid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	vm, exists := m.VMs[vmid]
	if !exists {
		return fmt.Errorf("vm %d not found", vmid)
	}

	vm.State = string(models.SandboxStopped)
	return nil
}

// DestroyVM simulates destroying a VM.
func (m *MockProxmoxBackend) DestroyVM(ctx context.Context, vmid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ShouldFailDestroy {
		return fmt.Errorf("failed to destroy vm %d", vmid)
	}

	vm, exists := m.VMs[vmid]
	if !exists {
		return fmt.Errorf("vm %d not found", vmid)
	}

	now := time.Now()
	vm.DestroyedAt = &now
	vm.State = string(models.SandboxDestroyed)

	return nil
}

// GetVM returns a VM by ID.
func (m *MockProxmoxBackend) GetVM(vmid int) *MockVM {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.VMs[vmid]
}

// MockSecretsStore is a mock implementation of a secrets store for testing.
type MockSecretsStore struct {
	mu        sync.Mutex
	secrets   map[string]string
	getError  error
	putError  error
	shouldLag bool
}

// NewMockSecretsStore creates a new mock secrets store.
func NewMockSecretsStore() *MockSecretsStore {
	return &MockSecretsStore{
		secrets: make(map[string]string),
	}
}

// Get retrieves a secret value by key.
func (m *MockSecretsStore) Get(ctx context.Context, key string) (string, error) {
	if m.shouldLag {
		time.Sleep(10 * time.Millisecond)
	}

	if m.getError != nil {
		return "", m.getError
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	val, ok := m.secrets[key]
	if !ok {
		return "", fmt.Errorf("secret not found: %s", key)
	}
	return val, nil
}

// Put stores a secret value by key.
func (m *MockSecretsStore) Put(ctx context.Context, key, value string) error {
	if m.putError != nil {
		return m.putError
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.secrets[key] = value
	return nil
}

// Delete removes a secret by key.
func (m *MockSecretsStore) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.secrets, key)
	return nil
}

// SetGetError sets an error to be returned by Get.
func (m *MockSecretsStore) SetGetError(err error) {
	m.getError = err
}

// SetPutError sets an error to be returned by Put.
func (m *MockSecretsStore) SetPutError(err error) {
	m.putError = err
}

// SetShouldLag enables artificial lag in operations.
func (m *MockSecretsStore) SetShouldLag(lag bool) {
	m.shouldLag = lag
}

// MockHTTPHandler is a mock HTTP handler for testing API clients.
type MockHTTPHandler struct {
	mu            sync.Mutex
	responses     map[string][]*MockResponse
	requests      []*MockRequest
	defaultStatus int
	delay         time.Duration
}

// MockResponse represents a mock HTTP response.
type MockResponse struct {
	Status int
	Body   any
	Header map[string]string
}

// MockRequest represents a captured HTTP request.
type MockRequest struct {
	Method  string
	Path    string
	Header  http.Header
	Body    []byte
	At      time.Time
}

// NewMockHTTPHandler creates a new mock HTTP handler.
func NewMockHTTPHandler() *MockHTTPHandler {
	return &MockHTTPHandler{
		responses:     make(map[string][]*MockResponse),
		defaultStatus: http.StatusOK,
	}
}

// ServeHTTP implements http.Handler.
func (m *MockHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Capture request
	body, _ := io.ReadAll(r.Body)
	req := &MockRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Header: r.Header.Clone(),
		Body:   body,
		At:     time.Now(),
	}
	m.requests = append(m.requests, req)

	// Apply delay if set
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	// Get response for this path
	key := r.Method + ":" + r.URL.Path
	responses, ok := m.responses[key]
	if !ok || len(responses) == 0 {
		w.WriteHeader(m.defaultStatus)
		return
	}

	// Get next response in round-robin fashion
	resp := responses[0]
	if len(responses) > 1 {
		m.responses[key] = responses[1:]
	}

	// Set headers
	for k, v := range resp.Header {
		w.Header().Set(k, v)
	}

	// Write status
	if resp.Status != 0 {
		w.WriteHeader(resp.Status)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	// Write body
	if resp.Body != nil {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		_ = enc.Encode(resp.Body)
	}
}

// AddResponse adds a mock response for a given method and path.
func (m *MockHTTPHandler) AddResponse(method, path string, status int, body any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := method + ":" + path
	m.responses[key] = append(m.responses[key], &MockResponse{
		Status: status,
		Body:   body,
	})
}

// AddResponseWithHeaders adds a mock response with custom headers.
func (m *MockHTTPHandler) AddResponseWithHeaders(method, path string, status int, body any, headers map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := method + ":" + path
	m.responses[key] = append(m.responses[key], &MockResponse{
		Status: status,
		Body:   body,
		Header: headers,
	})
}

// SetDelay sets an artificial delay for all responses.
func (m *MockHTTPHandler) SetDelay(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = d
}

// GetRequests returns all captured requests.
func (m *MockHTTPHandler) GetRequests() []*MockRequest {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.requests
}

// ClearRequests clears all captured requests.
func (m *MockHTTPHandler) ClearRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = nil
}

// NewTestServer creates a test HTTP server with the mock handler.
func (m *MockHTTPHandler) NewTestServer(t interface {
	Cleanup(func())
}) *httptest.Server {
	srv := httptest.NewServer(m)
	if t, ok := t.(interface{ Cleanup(func()) }); ok {
		t.Cleanup(srv.Close)
	}
	return srv
}

// Reset clears all responses and requests.
func (m *MockHTTPHandler) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.responses = make(map[string][]*MockResponse)
	m.requests = nil
}
