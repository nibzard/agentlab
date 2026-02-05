# Contributing to AgentLab

Thank you for your interest in contributing to AgentLab! This document provides comprehensive guidance for developers who want to contribute to the project.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Development Environment Setup](#development-environment-setup)
- [Building the Project](#building-the-project)
- [Running Tests](#running-tests)
- [Writing Tests](#writing-tests)
- [Code Style Guidelines](#code-style-guidelines)
- [Development Without Proxmox](#development-without-proxmox)
- [Adding New CLI Commands](#adding-new-cli-commands)
- [Adding New Daemon API Endpoints](#adding-new-daemon-api-endpoints)
- [Pull Request Process](#pull-request-process)
- [Getting Help](#getting-help)

## Prerequisites

Before you begin, ensure you have the following installed:

- **Go 1.24.0 or higher**: AgentLab requires Go 1.24.0
  ```bash
  go version  # Should show go1.24.0 or higher
  ```

- **Git**: For version control
  ```bash
  git --version
  ```

- **Make**: For building with the Makefile (most systems have this pre-installed)

### Optional but Recommended

- **Proxmox VE**: For integration testing (see [Development Without Proxmox](#development-without-proxmox) for alternatives)
- **gofmt**: Comes with Go, used for code formatting
- **go vet**: Comes with Go, used for static analysis

## Development Environment Setup

### 1. Fork and Clone the Repository

```bash
# Fork the repository on GitHub first, then clone your fork
git clone https://github.com/YOUR_USERNAME/agentlab.git
cd agentlab

# Add the upstream remote
git remote add upstream https://github.com/agentlab/agentlab.git
```

### 2. Install Dependencies

AgentLab uses Go modules, so dependencies are managed automatically:

```bash
# Download all dependencies
go mod download

# Verify dependencies
go mod verify
```

### 3. Verify Your Setup

```bash
# Build the project to ensure everything works
make build

# Run the linter
make lint

# Run tests
make test
```

## Building the Project

AgentLab provides a Makefile with several build targets:

### Standard Build

Build binaries for your current platform:

```bash
make build
```

This creates:
- `bin/agentlab` - The CLI tool
- `bin/agentlabd` - The daemon service
- `dist/agentlab_linux_amd64` - Linux CLI binary (cross-compiled)
- `dist/agentlabd_linux_amd64` - Linux daemon binary (cross-compiled)

### Build Individual Components

```bash
# Build only the CLI
make bin/agentlab

# Build only the daemon
make bin/agentlabd

# Build Linux binaries
make dist/agentlab_linux_amd64
make dist/agentlabd_linux_amd64
```

### Clean Build Artifacts

```bash
make clean
```

### Cross-Compilation

To build for different platforms, use the `GOOS` and `GOARCH` environment variables:

```bash
# Build for Linux ARM64
GOOS=linux GOARCH=arm64 go build -o bin/agentlab-linux-arm64 ./cmd/agentlab

# Build for macOS AMD64
GOOS=darwin GOARCH=amd64 go build -o bin/agentlab-darwin-amd64 ./cmd/agentlab

# Build for Windows
GOOS=windows GOARCH=amd64 go build -o bin/agentlab.exe ./cmd/agentlab
```

## Running Tests

AgentLab uses a comprehensive testing suite. Here's how to run tests:

### Unit Tests

Run all unit tests:

```bash
make test
# or directly:
go test ./...
```

### Test Coverage

Generate a coverage report:

```bash
make test-coverage
```

This creates:
- `coverage.out` - Raw coverage data
- `coverage.html` - HTML coverage report (open in a browser)

### Race Condition Tests

Run tests with race detection:

```bash
make test-race
# or directly:
go test -race ./...
```

### Integration Tests

Integration tests require the `integration` build tag:

```bash
make test-integration
# or directly:
go test -tags=integration ./...
```

**Note:** Integration tests typically require a running Proxmox instance or appropriate test infrastructure.

### Run All Tests

Run the complete test suite:

```bash
make test-all
```

This runs unit tests, race detector tests, and generates coverage reports.

### Run Specific Tests

Run tests for a specific package:

```bash
# Test a specific package
go test ./internal/config

# Test with verbose output
go test -v ./internal/daemon

# Run a specific test
go test -v ./internal/config -run TestValidateWildcard
```

## Writing Tests

AgentLab follows Go testing best practices with table-driven tests and the `testify` assertion library.

### Unit Tests

Unit tests should be fast, deterministic, and test a single piece of functionality.

#### Table-Driven Tests

Use table-driven tests for multiple test cases:

```go
func TestValidateWildcard(t *testing.T) {
    tests := []struct {
        name        string
        setup       func(*Config)
        wantErr     bool
        errContains string
    }{
        {
            name: "wildcard bootstrap requires subnet",
            setup: func(c *Config) {
                c.BootstrapListen = "0.0.0.0:8844"
                c.AgentSubnet = ""
            },
            wantErr:     true,
            errContains: "agent_subnet",
        },
        {
            name: "wildcard with all required fields",
            setup: func(c *Config) {
                c.BootstrapListen = "0.0.0.0:8844"
                c.AgentSubnet = "10.77.0.0/16"
            },
            wantErr: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            cfg := DefaultConfig()
            if tt.setup != nil {
                tt.setup(&cfg)
            }
            err := cfg.Validate()
            if tt.wantErr {
                require.Error(t, err)
                if tt.errContains != "" {
                    assert.Contains(t, err.Error(), tt.errContains)
                }
            } else {
                assert.NoError(t, err)
            }
        })
    }
}
```

#### Using Testify

AgentLab uses `github.com/stretchr/testify` for assertions:

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestSomething(t *testing.T) {
    // require.NoError fails the test immediately if condition is not met
    require.NoError(t, err, "setup should not fail")

    // assert.Equal records the failure but continues execution
    assert.Equal(t, expected, actual, "values should match")

    // assert.Contains checks substring membership
    assert.Contains(t, actualString, substring, "should contain substring")
}
```

**When to use `require` vs `assert`:**
- Use `require` when the test cannot continue if the check fails (e.g., setup failures)
- Use `assert` when you want to check multiple conditions in a test

### Test Helpers

Use the test helpers in `internal/testing/`:

```go
import "github.com/agentlab/agentlab/internal/testing"

func TestWithHelpers(t *testing.T) {
    // Create test models with defaults
    job := testing.NewTestJob(testing.JobOpts{
        Status: models.JobRunning,
        Task:   "test-task",
    })

    // Create a test database
    db := testing.OpenTestDB(t)

    // Create temp files
    configPath := testing.TempFile(t, "key: value")

    // Use test assertions
    testing.AssertJSONEqual(t, expected, actual)
}
```

### Integration Tests

Integration tests should be tagged with `integration`:

```go
//go:build integration
// +build integration

package daemon

import (
    "testing"
)

func TestProxmoxIntegration(t *testing.T) {
    // This test requires a Proxmox instance
    // It will only run with: go test -tags=integration
}
```

### Test File Organization

- Test files should be named `*_test.go`
- Place test files in the same package as the code they test
- Use `t.Helper()` for helper functions to get correct line numbers in failures
- Use `t.Cleanup()` for cleanup instead of `t.Defer()`

### Test Best Practices

1. **Test Names**: Use descriptive names that describe what is being tested
2. **Table-Driven Tests**: Use for multiple similar test cases
3. **Determinism**: Tests should be deterministic and produce the same results every time
4. **Isolation**: Tests should not depend on each other
5. **Speed**: Unit tests should be fast (< 100ms per test)
6. **Coverage**: Aim for high coverage but focus on testing behavior, not lines

## Code Style Guidelines

AgentLab follows standard Go conventions:

### Formatting

Use `gofmt` to format code:

```bash
gofmt -w .
```

Or use the Makefile target:

```bash
make lint
```

This will:
- Check that all code is properly formatted
- Run `go vet ./...` for static analysis

### Naming Conventions

- **Packages**: Use lowercase, single words when possible
- **Constants**: Use `PascalCase` for exported constants
- **Variables**: Use `camelCase`
- **Interfaces**: Use `PascalCase` ending in "er" (e.g., `Runner`, `Store`)
- **Test Functions**: Use `Test<FunctionName>` or `Test<FunctionName>_<Scenario>`

### Comments

- Exported functions should have comments
- Comments should be complete sentences
- Use godoc format for package and exported function comments
- Don't use comments that state the obvious

Example:

```go
// Validate checks if the configuration is valid and returns an error if not.
// It verifies that all required fields are set and that values are within
// acceptable ranges.
func (c *Config) Validate() error {
    // ...
}
```

### Error Handling

- Always handle errors explicitly
- Don't ignore errors with `_`
- Use `errors.Is()` and `errors.As()` for error checking
- Wrap errors with context using `fmt.Errorf("context: %w", err)`

Example:

```go
if err != nil {
    return fmt.Errorf("failed to load config: %w", err)
}

if errors.Is(err, os.ErrNotExist) {
    // Handle file not found
}
```

### File Organization

- Each file should start with a brief comment describing its purpose
- Group related functions together
- Keep files focused and reasonably sized (< 500 lines when possible)
- Use subdirectories for organizing large packages

## Development Without Proxmox

You can develop and test AgentLab without a Proxmox instance by using the mock implementations in `internal/testing/`.

### Using MockProxmoxBackend

The mock backend simulates Proxmox operations:

```go
import "github.com/agentlab/agentlab/internal/testing"

func TestWithoutProxmox(t *testing.T) {
    mockBackend := testing.NewMockProxmoxBackend()

    // Simulate creating a VM
    vmid, err := mockBackend.CreateVM(ctx, "test-vm", "default")
    require.NoError(t, err)
    assert.Equal(t, 100, vmid)

    // Simulate starting the VM
    err = mockBackend.StartVM(ctx, vmid)
    require.NoError(t, err)

    // Check VM state
    vm := mockBackend.GetVM(vmid)
    assert.NotNil(t, vm)
    assert.Equal(t, "test-vm", vm.Name)
}
```

### Mock Features

The mock backend supports:
- Creating VMs with configurable delays and errors
- Starting/stopping VMs
- Destroying VMs
- Setting failure conditions for testing error paths

```go
mockBackend := testing.NewMockProxmoxBackend()

// Simulate failure conditions
mockBackend.ShouldFailCreate = true
mockBackend.ShouldFailStart = true
mockBackend.ShouldFailDestroy = true

// Add delay to operations
mockBackend.CreateDelay = 5 * time.Second

// Set specific error
mockBackend.CreateError = fmt.Errorf("out of resources")
```

### Using MockSecretsStore

For testing secrets handling without actual encryption:

```go
mockStore := testing.NewMockSecretsStore()

// Store a secret
err := mockStore.Put(ctx, "test-key", "test-value")
require.NoError(t, err)

// Retrieve a secret
value, err := mockStore.Get(ctx, "test-key")
require.NoError(t, err)
assert.Equal(t, "test-value", value)

// Simulate errors
mockStore.SetGetError(fmt.Errorf("connection failed"))
mockStore.SetPutError(fmt.Errorf("quota exceeded"))
```

### Using MockHTTPHandler

For testing HTTP clients without real servers:

```go
mockHandler := testing.NewMockHTTPHandler()

// Add mock responses
mockHandler.AddResponse("GET", "/api/vm/100", 200, map[string]interface{}{
    "vmid": 100,
    "name": "test-vm",
    "status": "running",
})

// Create test server
srv := mockHandler.NewTestServer(t)
defer srv.Close()

// Use srv.URL for your client
client := NewClient(srv.URL)

// Verify requests
requests := mockHandler.GetRequests()
assert.Len(t, requests, 1)
assert.Equal(t, "GET", requests[0].Method)
assert.Equal(t, "/api/vm/100", requests[0].Path)
```

## Adding New CLI Commands

The CLI is structured with a command dispatch pattern. Here's how to add a new command:

### 1. Define Command Usage

Add your command to the usage text in `cmd/agentlab/main.go`:

```go
const usageText = `agentlab is the CLI for agentlabd.

Usage:
  agentlab mycommand --required-flag <value> [--optional-flag]

[... existing commands ...]
`
```

### 2. Add Dispatch Handler

Update the `dispatch` function in `cmd/agentlab/main.go`:

```go
func dispatch(ctx context.Context, args []string, base commonFlags) error {
    switch args[0] {
    case "job":
        return runJobCommand(ctx, args[1:], base)
    case "mycommand":  // Add your new command here
        return runMyCommand(ctx, args[1:], base)
    // ... other commands ...
    default:
        printUsage()
        return fmt.Errorf("unknown command %q", args[0])
    }
}
```

### 3. Implement Command Handler

Create a new file `cmd/agentlab/mycommand.go`:

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "io"
)

func runMyCommand(ctx context.Context, args []string, base commonFlags) error {
    fs := flag.NewFlagSet("mycommand", flag.ContinueOnError)
    fs.SetOutput(io.Discard)

    var requiredFlag string
    var optionalFlag string

    fs.StringVar(&requiredFlag, "required-flag", "", "Required flag description")
    fs.StringVar(&optionalFlag, "optional-flag", "", "Optional flag description")

    if err := fs.Parse(args); err != nil {
        if err == flag.ErrHelp {
            printMyCommandUsage()
            return errHelp
        }
        return newUsageError(err, true)
    }

    // Validate required flags
    if requiredFlag == "" {
        return newUsageError(fmt.Errorf("--required-flag is required"), true)
    }

    // Call the daemon API
    client := newAPIClient(base.socketPath, base.timeout)

    result, err := client.DoMyCommand(ctx, requiredFlag, optionalFlag)
    if err != nil {
        return err
    }

    // Output result
    if base.jsonOutput {
        writeJSON(os.Stdout, result)
    } else {
        fmt.Printf("Success: %s\n", result.Message)
    }

    return nil
}

func printMyCommandUsage() {
    fmt.Fprintln(os.Stdout, "Usage: agentlab mycommand --required-flag <value> [--optional-flag]")
}
```

### 4. Add API Client Method

Update `cmd/agentlab/api.go` to add the API call:

```go
func (c *client) DoMyCommand(ctx context.Context, required, optional string) (*MyCommandResponse, error) {
    reqBody := V1MyCommandRequest{
        Required: required,
        Optional: optional,
    }

    req, err := c.newRequest(http.MethodPost, "/v1/mycommand", reqBody)
    if err != nil {
        return nil, err
    }

    var resp V1MyCommandResponse
    if err := c.do(ctx, req, &resp); err != nil {
        return nil, err
    }

    return &resp, nil
}
```

### 5. Test Your Command

Create `cmd/agentlab/mycommand_test.go`:

```go
package main

import (
    "context"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestRunMyCommand(t *testing.T) {
    // Test implementation
    t.Run("with valid flags", func(t *testing.T) {
        ctx := context.Background()
        args := []string{"--required-flag", "value"}
        base := commonFlags{
            socketPath: "/tmp/test.sock",
            jsonOutput: false,
        }

        err := runMyCommand(ctx, args, base)
        // Assert expected behavior
    })
}
```

## Adding New Daemon API Endpoints

API endpoints are handled by the `ControlAPI` in `internal/daemon/`.

### 1. Define Request/Response Types

Add types to `internal/daemon/api_types.go`:

```go
// V1MyEndpointRequest defines the request for the my endpoint.
type V1MyEndpointRequest struct {
    Field1 string `json:"field1"`
    Field2 int    `json:"field2"`
}

// V1MyEndpointResponse defines the response for the my endpoint.
type V1MyEndpointResponse struct {
    Result   string `json:"result"`
    Metadata string `json:"metadata,omitempty"`
}
```

### 2. Register the Route

Update `Register()` in `internal/daemon/api.go`:

```go
func (api *ControlAPI) Register(mux *http.ServeMux) {
    if mux == nil {
        return
    }
    // ... existing routes ...
    mux.HandleFunc("/v1/myendpoint", api.handleMyEndpoint)
    mux.HandleFunc("/v1/myendpoint/", api.handleMyEndpointByID)
}
```

### 3. Implement the Handler

Add handler functions to `internal/daemon/api.go`:

```go
func (api *ControlAPI) handleMyEndpoint(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case http.MethodPost:
        api.handleMyEndpointCreate(w, r)
    case http.MethodGet:
        api.handleMyEndpointList(w, r)
    default:
        writeMethodNotAllowed(w, []string{http.MethodPost, http.MethodGet})
    }
}

func (api *ControlAPI) handleMyEndpointCreate(w http.ResponseWriter, r *http.Request) {
    var req V1MyEndpointRequest
    if err := decodeJSON(w, r, &req); err != nil {
        writeError(w, http.StatusBadRequest, err.Error())
        return
    }

    // Validate request
    if req.Field1 == "" {
        writeError(w, http.StatusBadRequest, "field1 is required")
        return
    }

    // Process request
    result, err := api.myEndpointService.Create(r.Context(), req)
    if err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }

    writeJSON(w, http.StatusCreated, result)
}
```

### 4. Add Helper Functions

Use existing helper functions for common operations:

- `decodeJSON(w, r, &req)` - Decode and validate JSON request body
- `writeJSON(w, status, data)` - Write JSON response
- `writeError(w, status, message)` - Write error response
- `writeMethodNotAllowed(w, methods)` - Write 405 Method Not Allowed

### 5. Test Your Endpoint

Create `internal/daemon/myendpoint_api_test.go`:

```go
package daemon

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestHandleMyEndpointCreate(t *testing.T) {
    // Setup test API
    api := setupTestAPI(t)

    t.Run("creates resource successfully", func(t *testing.T) {
        reqBody := V1MyEndpointRequest{
            Field1: "test",
            Field2: 42,
        }
        body, _ := json.Marshal(reqBody)

        req := httptest.NewRequest(http.MethodPost, "/v1/myendpoint", strings.NewReader(string(body)))
        w := httptest.NewRecorder()

        api.handleMyEndpointCreate(w, req)

        assert.Equal(t, http.StatusCreated, w.Code)

        var resp V1MyEndpointResponse
        err := json.Unmarshal(w.Body.Bytes(), &resp)
        require.NoError(t, err)
        assert.NotEmpty(t, resp.Result)
    })
}
```

## Pull Request Process

### Branch Naming

Use descriptive branch names:

```
feature/add-new-cli-command
fix/sandbox-timeout-handling
docs/update-api-documentation
refactor/improve-error-messages
```

### Commit Messages

Follow conventional commit format:

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes (formatting, etc.)
- `refactor`: Code refactoring
- `test`: Adding or updating tests
- `chore`: Maintenance tasks

Examples:

```
feat(daemon): add metrics endpoint for monitoring

This adds a new /v1/metrics endpoint that exposes Prometheus-compatible
metrics for daemon monitoring.

Closes #123
```

```
fix(cli): handle missing profile gracefully

Previously, the CLI would panic when a non-existent profile was
specified. Now it returns a clear error message.
```

### Pull Request Checklist

Before submitting a PR:

1. **Code Quality**
   - [ ] Code follows style guidelines (run `make lint`)
   - [ ] Code is well-documented with comments
   - [ ] No unnecessary changes included

2. **Testing**
   - [ ] Tests added for new functionality
   - [ ] All tests pass (`make test-all`)
   - [ ] Test coverage is adequate

3. **Documentation**
   - [ ] API documentation updated (if applicable)
   - [ ] User guide updated (if user-facing change)
   - [ ] Comments are clear and accurate

4. **Commit History**
   - [ ] Commits are logically organized
   - [ ] Commit messages follow conventions
   - [ ] No fixup or squashed commits

### Submitting a Pull Request

1. Push your branch to your fork:
   ```bash
   git push origin feature/your-feature-name
   ```

2. Create a pull request on GitHub with:
   - Clear title describing the change
   - Detailed description of what you did and why
   - Links to related issues
   - Screenshots for UI changes (if applicable)

3. Request review from maintainers

4. Address review feedback:
   - Make requested changes
   - Push updates to your branch
   - Respond to review comments

## Getting Help

If you need help contributing:

1. **Documentation**: Check existing docs in `docs/`
2. **Issues**: Look for related GitHub issues
3. **Tests**: Review existing tests for patterns
4. **Code**: Read similar code for examples

### Communication Channels

- GitHub Issues: For bugs and feature requests
- GitHub Discussions: For questions and general discussion
- Pull Requests: For code review and collaboration

### Code Review Tips

When reviewing or responding to review:

- Be constructive and respectful
- Explain the "why" behind suggestions
- Ask questions when something is unclear
- Approve when you're satisfied, don't wait for perfection

Thank you for contributing to AgentLab!
