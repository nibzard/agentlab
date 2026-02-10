# Testing Guide

This guide covers everything you need to know about testing in AgentLab, including how to run tests, write new tests, and understand the testing architecture.

## Table of Contents

1. [Test Organization](#test-organization)
2. [Running Tests](#running-tests)
3. [Writing Unit Tests](#writing-unit-tests)
4. [Writing Integration Tests](#writing-integration-tests)
5. [Using Test Utilities](#using-test-utilities)
6. [Test Coverage](#test-coverage)
7. [Race Detector](#race-detector)
8. [CI/CD Pipeline](#cicd-pipeline)
9. [Adding Tests for New Features](#adding-tests-for-new-features)
10. [Best Practices](#best-practices)

---

## Test Organization

AgentLab uses a two-tier testing approach:

### Unit Tests (`*_test.go`)

Unit tests are co-located with the source code they test. Every package has its own `*_test.go` files:

```
internal/
├── config/
│   └── config_test.go       # Unit tests for config package
├── db/
│   ├── sandboxes_test.go    # Unit tests for sandbox operations
│   ├── jobs_test.go         # Unit tests for job operations
│   └── testutil.go          # Package-specific test helpers
└── daemon/
    ├── daemon_test.go       # Unit tests for daemon logic
    └── profile_validation_test.go
```

### Integration Tests (`tests/integration_test.go`)

Integration tests live in the `tests/` directory and test the full system:

```
tests/
└── integration_test.go      # End-to-end integration tests
```

Integration tests are build-tagged with `//go:build integration` and are not run by default.

### Why Separate Them?

- **Unit tests** run fast and test individual components in isolation
- **Integration tests** are slower but verify components work together correctly
- This separation allows developers to run quick unit tests during development
- Integration tests can be run separately before committing or in CI

---

## Running Tests

Before running tests, ensure Go 1.24.0 or higher is installed (per the `go.mod` requirement).

### Run All Unit Tests

```bash
make test
# Or directly:
go test ./...
```

### Run Tests with Coverage

```bash
make test-coverage
```

This generates:
- `coverage.out` - Machine-readable coverage data
- `coverage.html` - Interactive HTML coverage report

### Run Coverage Audit (recommended)

```bash
make coverage-audit
```

This runs unit tests with coverage and prints:
- Overall coverage
- Per-package coverage (ascending)
- Lowest coverage files/functions

It writes artifacts to `dist/coverage/` by default. Override with:
- `COVERAGE_DIR=/tmp/coverage`
- `TOP_N=25` (change the number of low-coverage items shown)

### Generate Coverage HTML Report (dist/)

```bash
make coverage-html
```

This writes `dist/coverage/coverage.html`.

### Run Race Detector

The race detector helps find concurrent data access bugs:

```bash
make test-race
# Or directly:
go test -race ./...
```

### Run Integration Tests

Integration tests require the `integration` build tag:

```bash
make test-integration
# Or directly:
go test -tags=integration ./...
```

Skip integration tests in short mode:

```bash
go test -tags=integration -short ./tests/...
```

### Run All Tests

```bash
make test-all
```

This runs unit tests, race detector, and generates coverage.

### Run Specific Test

Run a specific test function:

```bash
go test -run TestCreateSandbox ./internal/db
```

Run tests in a specific package:

```bash
go test ./internal/config/...
```

Run with verbose output:

```bash
go test -v ./internal/config/...
```

---

## Writing Unit Tests

### Table-Driven Test Pattern

AgentLab uses the table-driven test pattern extensively. This pattern makes tests readable and maintainable:

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
                c.ArtifactListen = "0.0.0.0:8846"
                c.ControllerURL = "http://10.77.0.1:8844"
                c.ArtifactUploadURL = "http://10.77.0.1:8846/upload"
                c.AgentSubnet = ""
            },
            wantErr:     true,
            errContains: "agent_subnet",
        },
        {
            name: "wildcard with all required fields",
            setup: func(c *Config) {
                c.BootstrapListen = "0.0.0.0:8844"
                c.ArtifactListen = "0.0.0.0:8846"
                c.AgentSubnet = "10.77.0.0/16"
                c.ControllerURL = "http://10.77.0.1:8844"
                c.ArtifactUploadURL = "http://10.77.0.1:8846/upload"
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

### Testify Assertions

AgentLab uses the `testify` library for assertions:

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// assert - fails test but continues execution
assert.NoError(t, err)
assert.Equal(t, expected, actual)
assert.Contains(t, string, substring)
assert.True(t, condition)

// require - fails test and stops execution immediately
require.NoError(t, err)
require.Equal(t, expected, actual)
```

Use `require` for setup/teardown and critical operations where continuing doesn't make sense.
Use `assert` for multiple related checks where you want to see all failures.

### Subtests

Use `t.Run()` to create subtests for better organization:

```go
func TestCreateSandbox(t *testing.T) {
    t.Run("success", func(t *testing.T) {
        // Test success case
    })

    t.Run("nil store", func(t *testing.T) {
        // Test nil store error
    })

    t.Run("missing vmid", func(t *testing.T) {
        // Test validation error
    })
}
```

### Cleanup with t.Cleanup()

Use `t.Cleanup()` for cleanup that runs even if the test fails:

```go
func TestSomething(t *testing.T) {
    tmpDir := t.TempDir()  // Auto-cleaned temporary directory
    file, _ := os.Create(tmpDir + "/test.txt")

    t.Cleanup(func() {
        file.Close()
        // This runs even if test fails
    })
}
```

---

## Writing Integration Tests

### Build Tags

Integration tests use the `integration` build tag:

```go
//go:build integration
// +build integration

package tests

import (
    "testing"
    // ...
)
```

This keeps them from running by default. Add `-tags=integration` to run them.

### Test Structure

Integration tests typically follow a lifecycle pattern:

```go
func TestCompleteJobLifecycle(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }

    ctx := context.Background()
    store := openTestDB(t)

    // Step 1: Create a job
    job := models.Job{
        ID:        "integration-test-job-1",
        RepoURL:   "https://github.com/example/repo",
        Status:    models.JobQueued,
        CreatedAt: time.Now().UTC(),
    }
    err := store.CreateJob(ctx, job)
    require.NoError(t, err)

    // Step 2: Transition through states
    err = store.UpdateJobStatus(ctx, job.ID, models.JobRunning)
    require.NoError(t, err)

    // Step 3: Create and attach sandbox
    // ...

    // Step 4: Complete job
    err = store.UpdateJobResult(ctx, job.ID, models.JobCompleted, resultJSON)
    require.NoError(t, err)
}
```

### Test Environment Setup

Integration tests use environment variables for configuration:

```go
func integrationConfig(t *testing.T) config.Config {
    t.Helper()

    profilesDir := os.Getenv("AGENTLAB_PROFILES_DIR")
    if profilesDir == "" {
        profilesDir = "/etc/agentlab/profiles"
    }

    dataDir := os.Getenv("AGENTLAB_DATA_DIR")
    if dataDir == "" {
        dataDir = t.TempDir()
    }

    return config.Config{
        ProfilesDir: profilesDir,
        DataDir:     dataDir,
        // ...
    }
}
```

### Requirements

Integration tests may require:
- A running Proxmox instance (or mock)
- Valid Proxmox credentials
- Network access
- Specific profile configurations

Always check for `testing.Short()` to allow skipping in quick test runs.

---

## Using Test Utilities

### internal/testing Package

The `internal/testing` package provides shared test utilities:

```go
import testutil "github.com/agentlab/agentlab/internal/testing"
```

### Factory Functions

Create test objects with sensible defaults:

```go
// Create a test job
job := testutil.NewTestJob(testutil.JobOpts{
    ID:      "custom-job-id",
    Status:  models.JobRunning,
    Profile: "custom-profile",
})

// Create a test sandbox
sandbox := testutil.NewTestSandbox(testutil.SandboxOpts{
    VMID:    123,
    Name:    "my-sandbox",
    State:   models.SandboxRunning,
})

// Create a test workspace
workspace := testutil.NewTestWorkspace(testutil.WorkspaceOpts{
    ID:     "ws-1",
    SizeGB: 100,
})

// Create a test profile
profile := testutil.NewTestProfile(testutil.ProfileOpts{
    Name:       "my-profile",
    TemplateVM: 9000,
})
```

### Test Constants

Common test constants are defined in `internal/testing/testutil.go`:

```go
const (
    TestRepoURL     = "https://github.com/example/repo"
    TestProfile     = "default"
    TestRef         = "main"
    TestVMID        = 100
    TestVMIDAlt     = 200
    TestWorkspaceID = "ws-test-1"
)
```

### Fixed Time

Use `testutil.FixedTime` for deterministic time-based tests:

```go
import "time"

createdAt := testutil.FixedTime
expiresAt := testutil.FixedTime.Add(24 * time.Hour)
```

### Helper Functions

#### TempFile

Create a temporary file with content:

```go
path := testutil.TempFile(t, "file content")
```

#### MkdirTempInDir

Create a temp directory in a specific parent:

```go
tmpDir := testutil.MkdirTempInDir(t, "/tmp")
```

#### OpenTestDB

Open a test SQLite database:

```go
db := testutil.OpenTestDB(t)
// Auto-closed when test completes
```

#### AssertJSONEqual

Compare JSON semantically (ignoring formatting):

```go
testutil.AssertJSONEqual(t, expectedJSON, actualJSON)
```

#### ParseTime

Parse an RFC3339 timestamp:

```go
ts := testutil.ParseTime(t, "2024-01-01T12:00:00Z")
```

### Package-Specific Test Helpers

Some packages have their own `testutil.go` files:

```go
// internal/db/testutil.go
func openTestStore(t *testing.T) *Store {
    t.Helper()
    path := testutil.MkdirTempInDir(t, t.TempDir())
    store, err := Open(path + "/test.db")
    require.NoError(t, err)
    t.Cleanup(func() {
        store.Close()
    })
    return store
}
```

---

## Test Coverage

### Generating Coverage Reports

```bash
make coverage-html
```

`make test-coverage` still writes `coverage.out` and `coverage.html` to the repo root for quick local use.

### Coverage Audit Workflow

```bash
make coverage-audit
```

This emits:
- Overall coverage
- Per-package coverage (ascending)
- Lowest coverage files/functions

It also writes:
- `dist/coverage/coverage.out`
- `dist/coverage/coverage.func.txt`

### Reading Coverage Reports

Open `dist/coverage/coverage.html` (or `coverage.html` if you used `make test-coverage`) in a browser:
- Green lines: Covered by tests
- Red lines: Not covered
- Yellow: Partially covered (e.g., some branches not tested)

### Baseline Snapshot (2026-02-10)

Overall coverage: **46.3%**

Per-package baseline (coverage-audit, sorted ascending):

| Package | Coverage |
| --- | --- |
| `cmd/agentlab` | 37.3% |
| `internal/config` | 45.1% |
| `internal/proxmox` | 45.2% |
| `internal/daemon` | 49.0% |
| `cmd/agentlabd` | 50.0% |
| `internal/secrets` | 58.9% |
| `internal/db` | 68.7% |
| `internal/buildinfo` | 100.0% |

Note: `internal/testing` currently reports 0.0% (helper-only package) and is excluded from targets.

### Coverage Targets (initial)

Targets are conservative and are expected to ratchet up each coverage sprint.

| Package | Baseline | Target | Rationale |
| --- | --- | --- | --- |
| `cmd/agentlab` | 37.3% | 50% | CLI request building + remote auth edge cases |
| `internal/config` | 45.1% | 60% | Validation and merge precedence |
| `internal/daemon` | 49.0% | 60% | Handler wiring + auth middleware |
| `internal/proxmox` | 45.2% | 55% | API backend error paths |
| `cmd/agentlabd` | 50.0% | 60% | Daemon startup/config wiring |
| `internal/secrets` | 58.9% | 70% | Bundle parsing + redaction safety |
| `internal/db` | 68.7% | 75% | Core data invariants |

Focus coverage on:
- Complex business logic
- Error handling paths
- State transitions
- Validation code

### Next Coverage Slices (prioritized)

1. `cmd/agentlab`: CLI request building, remote auth errors, config precedence.
2. `internal/config`: validation rules and merge precedence.
3. `internal/daemon`: auth middleware, handler edge cases, wiring.
4. `internal/proxmox`: API backend error handling.

### View Package Coverage

See coverage by package:

```bash
go test -cover ./...
```

Output:
```
ok      github.com/agentlab/agentlab/cmd/agentlab         coverage: 37.3% of statements
ok      github.com/agentlab/agentlab/internal/config      coverage: 45.1% of statements
ok      github.com/agentlab/agentlab/internal/daemon      coverage: 49.0% of statements
```

---

## Race Detector

### Running with Race Detection

```bash
make test-race
# Or:
go test -race ./...
```

### Understanding Race Conditions

The race detector finds:
- Data races (concurrent reads/writes without synchronization)
- Unsafe goroutine access
- Missing mutex locks

### Common Race Patterns

**Unprotected shared state:**
```go
// BAD - Race condition
var counter int
go func() { counter++ }()
go func() { counter++ }()

// GOOD - Use mutex or atomic
var counter int64
go func() { atomic.AddInt64(&counter, 1) }()
go func() { atomic.AddInt64(&counter, 1) }()
```

**Missing synchronization:**
```go
// BAD - Race on close
go func() {
    ch <- value
    close(ch)
}()
<-ch

// GOOD - Use sync.WaitGroup
var wg sync.WaitGroup
wg.Add(1)
go func() {
    defer wg.Done()
    ch <- value
}()
value = <-ch
wg.Wait()
```

### CI Race Detection

The CI pipeline runs the race detector on all PRs. Fix race conditions before merging.

---

## CI/CD Pipeline

### GitHub Actions Workflow

Located at `.github/workflows/ci.yml`:

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - Checkout
      - Set up Go
      - Lint (make lint)
      - Test with Coverage
      - Upload Coverage to Codecov
      - Comment Coverage on PR
      - Test Race Detector
      - Integration Tests (continue-on-error: true)
      - Build
```

### What Runs in CI

1. **Lint**: `make lint` - Code formatting and vet
2. **Test**: Unit tests with coverage
3. **Coverage**: Uploads to Codecov, comments on PR
4. **Race Detector**: `make test-race`
5. **Integration Tests**: With `-tags=integration -short`
6. **Build**: Verifies code compiles

### Coverage Comments

Coverage is automatically commented on PRs:
- Shows overall coverage percentage
- Highlights files with changed coverage
- Tracks coverage delta from base branch

### Integration Tests in CI

Integration tests run with `continue-on-error: true` because they may require:
- Specific Proxmox setup
- Network access
- Credentials not available in CI

---

## Adding Tests for New Features

### TDD Approach

Follow Test-Driven Development:

1. **Write a failing test** for the new feature
2. **Run the test** to confirm it fails
3. **Write minimal code** to make the test pass
4. **Run the test** to confirm it passes
5. **Refactor** while keeping tests green
6. **Repeat** for each new behavior

### New Feature Checklist

When adding a new feature, ensure:

- [ ] Unit tests for all public functions/methods
- [ ] Unit tests for error cases
- [ ] Unit tests for edge cases (nil, empty, invalid inputs)
- [ ] Integration test if feature spans multiple components
- [ ] Test coverage >= 80% for new code
- [ ] No race conditions (run with `-race`)
- [ ] All tests pass before committing

### Example: Adding a New Function

```go
// First, write the test
func TestNewFeature(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {
            name:  "valid input",
            input: "test",
            want:  "TEST",
        },
        {
            name:    "empty input",
            input:   "",
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := NewFeature(tt.input)
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}

// Then write the implementation
func NewFeature(input string) (string, error) {
    if input == "" {
        return "", errors.New("input is required")
    }
    return strings.ToUpper(input), nil
}
```

### Testing Error Paths

Always test error handling:

```go
t.Run("nil store", func(t *testing.T) {
    err := (*Store)(nil).CreateSandbox(ctx, sandbox)
    assert.EqualError(t, err, "db store is nil")
})

t.Run("missing required field", func(t *testing.T) {
    sandbox := models.Sandbox{}  // Missing VMID
    err := store.CreateSandbox(ctx, sandbox)
    assert.EqualError(t, err, "sandbox vmid is required")
})

t.Run("database error", func(t *testing.T) {
    // Simulate database error
    err := store.CreateSandbox(ctx, sandbox)
    require.Error(t, err)
})
```

---

## Best Practices

### DO:

- **Use table-driven tests** for multiple related test cases
- **Use `t.Helper()`** in helper functions for correct line reporting
- **Use `require`** for setup/teardown, `assert` for checks
- **Clean up resources** with `t.Cleanup()` or `t.TempDir()`
- **Use descriptive test names** that explain what is being tested
- **Test error paths** as thoroughly as happy paths
- **Use `testing.Short()`** for slow integration tests
- **Add `t.Helper()`** to test helper functions

```go
func openTestDB(t *testing.T) *Store {
    t.Helper()  // Marks this as a test helper
    // ...
}
```

### DON'T:

- **Don't ignore errors** in tests (`_ = err`)
- **Don't use `time.Sleep()`** for synchronization (use channels or sync primitives)
- **Don't skip tests** without a good reason and `t.Skip()` explanation
- **Don't test implementation details** - test behavior and interfaces
- **Don't create unnecessary abstractions** for testing
- **Don't use global state** in tests (reset or isolate it)

### Test Naming

Use descriptive names that follow the pattern:

```go
Test<Function><Scenario><ExpectedResult>

// Examples:
TestCreateSandboxSuccess
TestCreateSandboxMissingVMID
TestCreateSandboxNilStore
TestUpdateJobStatusTransitionQueuedToRunning
```

### Subtest Naming

Use human-readable subtest names:

```go
t.Run("wildcard bootstrap requires subnet", func(t *testing.T) {
    // ...
})

t.Run("valid IPv4 subnet", func(t *testing.T) {
    // ...
})
```

### Organization

- Keep test files next to the code they test
- Group related tests with subtests
- Use table-driven tests for multiple cases
- Extract common setup/teardown to helper functions
- Keep tests simple and focused

### Performance

- Unit tests should run in seconds, not minutes
- Use `-short` flag for skipping slow tests
- Parallelize independent tests with `t.Parallel()`

```go
func TestFastOperation(t *testing.T) {
    t.Parallel()  // Can run in parallel with other tests
    // ...
}
```

### Test Isolation

Each test should be independent:
- Don't rely on test execution order
- Clean up after each test
- Use fresh fixtures for each test
- Avoid sharing state between tests

---

## Resources

- [Go Testing Package](https://pkg.go.dev/testing)
- [Testify Assertions](https://pkg.go.dev/github.com/stretchr/testify/assert)
- [Go Race Detector](https://go.dev/doc/articles/race_detector)
- [Table-Driven Tests in Go](https://dave.cheney.net/2019/05/07/prefer-table-driven-tests)

---

## Quick Reference

```bash
# Run unit tests
make test

# Run with coverage
make test-coverage

# Run coverage audit
make coverage-audit

# Generate coverage HTML (dist/)
make coverage-html

# Run race detector
make test-race

# Run integration tests
make test-integration

# Run all tests
make test-all

# Run specific test
go test -run TestName ./path/to/package

# Run with verbose output
go test -v ./...

# Run tests in parallel
go test -parallel 4 ./...

# Skip slow tests
go test -short ./...

# View coverage
open dist/coverage/coverage.html
```
