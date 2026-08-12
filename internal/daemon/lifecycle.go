package daemon

import (
	"context"
	"sync"
	"time"
)

// BackgroundRunner registers detached work against the daemon lifecycle so that
// it is cancelled and awaited at shutdown. Components that spawn goroutines, or
// that run synchronous work which must outlive a single HTTP request (job
// execution, sandbox provisioning, workspace lease renewal), route that work
// through a BackgroundRunner instead of using context.Background() directly
// (review H2).
type BackgroundRunner interface {
	// Go runs fn in a tracked goroutine under the daemon lifecycle context. It
	// returns false when the daemon is shutting down, in which case fn is not
	// invoked and the caller should refuse or defer the work.
	Go(name string, fn func(ctx context.Context)) bool
	// LifecycleContext returns the daemon lifecycle context. It is cancelled at
	// shutdown but otherwise outlives any single HTTP request, so synchronous
	// provisioning inside a handler is not coupled to the request's deadline.
	LifecycleContext() context.Context
}

// detachedRunner is the no-op fallback used when a component has no daemon
// lifecycle available (for example, in unit tests that construct a manager
// directly). Work runs detached against context.Background(), matching the
// pre-H2 behavior so existing tests are unaffected.
type detachedRunner struct{}

func (detachedRunner) Go(_ string, fn func(ctx context.Context)) bool {
	go fn(context.Background())
	return true
}

func (detachedRunner) LifecycleContext() context.Context { return context.Background() }

// DetachedRunner returns a BackgroundRunner that runs work detached from any
// daemon lifecycle. It is the default for components constructed without one.
func DetachedRunner() BackgroundRunner { return detachedRunner{} }

// taskTracker accounts for in-flight background work so shutdown can wait for
// it (or time out) before closing the store. It is race-free with respect to
// the classic WaitGroup Add-versus-Wait hazard: registration (Add) and the
// shutdown "draining" gate share one mutex, and shutdown sets draining before
// waiting, so no goroutine can register concurrently with Wait (review H2).
type taskTracker struct {
	mu         sync.Mutex
	wg         sync.WaitGroup
	draining   bool
	registered int // advisory in-flight count, updated under mu
}

// register reports whether work may begin. When ok is true the caller MUST call
// the returned done function exactly once when the work finishes (including on
// panic, via defer). When false the daemon is draining and the work must not
// run at all.
func (t *taskTracker) register(_ string) (done func(), ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.draining {
		return nil, false
	}
	t.wg.Add(1)
	t.registered++
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			t.registered--
			t.wg.Done()
			t.mu.Unlock()
		})
	}, true
}

// closeRegistration prevents any further work from registering. It must be
// called before wait. After it returns, register reports (nil, false).
func (t *taskTracker) closeRegistration() {
	t.mu.Lock()
	t.draining = true
	t.mu.Unlock()
}

// wait blocks until all previously registered work completes or the timeout
// elapses, returning whether everything drained in time. Must be called after
// closeRegistration.
func (t *taskTracker) wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// count reports the number of currently registered tasks (for diagnostics and
// tests). It is advisory and does not block.
func (t *taskTracker) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.registered
}

// isDraining reports whether closeRegistration has been called.
func (t *taskTracker) isDraining() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.draining
}
