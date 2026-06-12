package controller

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// RunTracker knows which runs are executing right now, per job. It backs
// concurrency-policy decisions (Forbid skips, Replace cancels) and lets the
// run reconciler tell an in-progress run from one orphaned by a restart.
// State is in-memory only: runs execute on the elected leader.
//
// It is also a manager.Runnable: Start captures the leader-scoped lifecycle
// context that execution goroutines derive from, so losing leadership or
// shutting down cancels every in-flight run instead of leaving detached
// goroutines writing status from a non-leader.
type RunTracker struct {
	mu       sync.Mutex
	inflight map[types.NamespacedName]map[string]context.CancelFunc
	base     context.Context
	shutdown bool
}

func NewRunTracker() *RunTracker {
	return &RunTracker{inflight: map[types.NamespacedName]map[string]context.CancelFunc{}}
}

// Start blocks until the leader context is cancelled, then aborts all
// in-flight runs. It satisfies sigs.k8s.io/controller-runtime/pkg/manager.Runnable.
func (t *RunTracker) Start(ctx context.Context) error {
	t.mu.Lock()
	t.base = ctx
	t.mu.Unlock()

	<-ctx.Done()

	t.mu.Lock()
	t.shutdown = true
	for _, runs := range t.inflight {
		for _, cancel := range runs {
			cancel()
		}
	}
	t.mu.Unlock()
	return nil
}

// NeedLeaderElection ties the tracker's lifecycle context to leadership, the
// same scope in which runs are executed.
func (t *RunTracker) NeedLeaderElection() bool {
	return true
}

// Context returns the lifecycle context new runs should derive from.
func (t *RunTracker) Context() context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.base != nil {
		return t.base
	}
	return context.Background()
}

// ShuttingDown reports whether in-flight cancellation came from losing the
// lifecycle context rather than a Replace.
func (t *RunTracker) ShuttingDown() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.shutdown
}

func (t *RunTracker) add(job types.NamespacedName, run string, cancel context.CancelFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight[job] == nil {
		t.inflight[job] = map[string]context.CancelFunc{}
	}
	t.inflight[job][run] = cancel
}

func (t *RunTracker) remove(job types.NamespacedName, run string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.inflight[job], run)
	if len(t.inflight[job]) == 0 {
		delete(t.inflight, job)
	}
}

// busy reports whether any run of the job is currently executing.
func (t *RunTracker) busy(job types.NamespacedName) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.inflight[job]) > 0
}

// has reports whether this specific run is currently executing.
func (t *RunTracker) has(job types.NamespacedName, run string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.inflight[job][run]
	return ok
}

// cancelAll aborts every executing run of the job (concurrencyPolicy: Replace).
func (t *RunTracker) cancelAll(job types.NamespacedName) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, cancel := range t.inflight[job] {
		cancel()
	}
}
