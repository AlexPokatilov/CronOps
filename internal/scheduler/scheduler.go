// Package scheduler keeps an in-memory registry of cron entries for
// HttpCronJob resources. It is a pure cache: the registry is rebuilt from the
// Kubernetes API (via the reconciler) after every controller restart.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/types"
)

// BuildCronSpec combines a 5-field cron expression with an optional IANA
// timezone into the spec string understood by robfig/cron, validating both.
func BuildCronSpec(schedule, timezone string) (string, error) {
	spec := schedule
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return "", fmt.Errorf("invalid timezone %q: %w", timezone, err)
		}
		spec = fmt.Sprintf("CRON_TZ=%s %s", timezone, schedule)
	}
	if _, err := cron.ParseStandard(spec); err != nil {
		return "", fmt.Errorf("invalid schedule %q: %w", schedule, err)
	}
	return spec, nil
}

// NextRun returns the next fire time of a cron spec produced by BuildCronSpec.
func NextRun(spec string, now time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(now), nil
}

type entry struct {
	id   cron.EntryID
	spec string
}

// Scheduler wraps a single cron engine with an upsert/remove registry keyed
// by namespaced resource name. It implements manager.Runnable and only runs
// on the elected leader.
type Scheduler struct {
	cron    *cron.Cron
	mu      sync.Mutex
	entries map[types.NamespacedName]entry
}

func New() *Scheduler {
	return &Scheduler{
		cron:    cron.New(),
		entries: map[types.NamespacedName]entry{},
	}
}

// Start runs the cron engine until the context is cancelled.
// It blocks, satisfying sigs.k8s.io/controller-runtime/pkg/manager.Runnable.
func (s *Scheduler) Start(ctx context.Context) error {
	s.cron.Start()
	<-ctx.Done()
	stopped := s.cron.Stop()
	<-stopped.Done()
	return nil
}

// NeedLeaderElection makes the manager start the cron engine only on the
// leader, so runs are executed exactly once across replicas.
func (s *Scheduler) NeedLeaderElection() bool {
	return true
}

// Upsert registers fn under the given cron spec. If the key is already
// registered with the same spec the existing entry is kept, so the callback
// must not capture per-generation state (it should re-read the resource).
func (s *Scheduler) Upsert(key types.NamespacedName, spec string, fn func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		if e.spec == spec {
			return nil
		}
		s.cron.Remove(e.id)
		delete(s.entries, key)
	}
	id, err := s.cron.AddFunc(spec, fn)
	if err != nil {
		return err
	}
	s.entries[key] = entry{id: id, spec: spec}
	return nil
}

// Remove drops the entry for key, if any.
func (s *Scheduler) Remove(key types.NamespacedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		s.cron.Remove(e.id)
		delete(s.entries, key)
	}
}

// Len returns the number of registered entries.
func (s *Scheduler) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
