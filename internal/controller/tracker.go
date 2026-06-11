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
type RunTracker struct {
	mu       sync.Mutex
	inflight map[types.NamespacedName]map[string]context.CancelFunc
}

func NewRunTracker() *RunTracker {
	return &RunTracker{inflight: map[types.NamespacedName]map[string]context.CancelFunc{}}
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
