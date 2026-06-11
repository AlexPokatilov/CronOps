package controller

import (
	"context"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
	"github.com/AlexPokatilov/CronOps/internal/executor"
)

// HttpCronJobRunReconciler executes pending HttpCronJobRun resources and
// garbage-collects finished ones (count cap + optional TTL). Runs are
// executed in goroutines so a slow endpoint never blocks the reconcile queue;
// the shared tracker enforces the job's concurrency policy.
type HttpCronJobRunReconciler struct {
	client.Client
	Recorder record.EventRecorder
	Executor *executor.Executor
	Tracker  *RunTracker
}

// Reconcile drives a run through its lifecycle: pending → Running →
// Succeeded/Failed → (TTL) deleted.
func (r *HttpCronJobRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var run cronopsv1alpha1.HttpCronJobRun
	if err := r.Get(ctx, req.NamespacedName, &run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	jobKey := types.NamespacedName{Namespace: run.Namespace, Name: run.Spec.JobName}

	switch run.Status.Phase {
	case cronopsv1alpha1.RunPhaseSucceeded, cronopsv1alpha1.RunPhaseFailed:
		return r.cleanupFinished(ctx, &run, jobKey)
	case cronopsv1alpha1.RunPhaseRunning:
		if r.Tracker.has(jobKey, run.Name) {
			return ctrl.Result{}, nil // executing right now in this process
		}
		// Running in status but unknown in memory: the previous leader died
		// mid-run. The HTTP call may or may not have happened — fail it.
		return ctrl.Result{}, r.finishRun(ctx, req.NamespacedName, func(st *cronopsv1alpha1.HttpCronJobRunStatus) {
			st.Phase = cronopsv1alpha1.RunPhaseFailed
			st.Message = "interrupted: controller restarted while the run was in progress"
		})
	}

	return r.startRun(ctx, &run, jobKey)
}

// startRun claims a pending run and launches its execution goroutine,
// honouring the parent job's concurrency policy.
func (r *HttpCronJobRunReconciler) startRun(ctx context.Context, run *cronopsv1alpha1.HttpCronJobRun, jobKey types.NamespacedName) (ctrl.Result, error) {
	runKey := client.ObjectKeyFromObject(run)

	var job cronopsv1alpha1.HttpCronJob
	if err := r.Get(ctx, jobKey, &job); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.finishRun(ctx, runKey, func(st *cronopsv1alpha1.HttpCronJobRunStatus) {
				st.Phase = cronopsv1alpha1.RunPhaseFailed
				st.Message = "HttpCronJob " + jobKey.String() + " not found"
			})
		}
		return ctrl.Result{}, err
	}

	switch job.Spec.ConcurrencyPolicy {
	case cronopsv1alpha1.ConcurrencyAllow:
	case cronopsv1alpha1.ConcurrencyReplace:
		r.Tracker.cancelAll(jobKey)
	default: // Forbid
		if r.Tracker.busy(jobKey) {
			r.Recorder.Event(&job, "Warning", "SkippedConcurrent",
				"previous run still in progress, skipping (concurrencyPolicy: Forbid)")
			return ctrl.Result{}, r.finishRun(ctx, runKey, func(st *cronopsv1alpha1.HttpCronJobRunStatus) {
				st.Phase = cronopsv1alpha1.RunPhaseFailed
				st.Message = "skipped: previous run still in progress (concurrencyPolicy: Forbid)"
			})
		}
	}

	started := metav1.Now()
	if err := r.patchRunStatus(ctx, runKey, func(st *cronopsv1alpha1.HttpCronJobRunStatus) {
		st.Phase = cronopsv1alpha1.RunPhaseRunning
		st.StartedAt = &started
	}); err != nil {
		return ctrl.Result{}, err
	}

	// The goroutine outlives this reconcile, so it gets its own context;
	// cancel comes from the tracker (Replace) or process shutdown.
	runCtx, cancel := context.WithCancel(context.Background())
	r.Tracker.add(jobKey, run.Name, cancel)
	go r.execute(runCtx, cancel, jobKey, runKey, &job, run.Spec.Trigger, started)
	return ctrl.Result{}, nil
}

// execute performs the HTTP call (with retries) and writes the outcome to the
// run, the parent job's status and the event stream, then prunes old runs.
func (r *HttpCronJobRunReconciler) execute(ctx context.Context, cancel context.CancelFunc, jobKey, runKey types.NamespacedName, job *cronopsv1alpha1.HttpCronJob, trigger string, started metav1.Time) {
	defer cancel()
	defer r.Tracker.remove(jobKey, runKey.Name)
	log := logf.Log.WithName("runner").WithValues("httpcronjobrun", runKey.String())

	res := r.Executor.Run(ctx, job)
	finished := metav1.Now()

	phase := cronopsv1alpha1.RunPhaseFailed
	message := res.Message
	if res.Success {
		phase = cronopsv1alpha1.RunPhaseSucceeded
	} else if ctx.Err() == context.Canceled {
		message = "canceled: replaced by a newer run (concurrencyPolicy: Replace)"
	}

	// Status writes use a fresh context: ctx is already canceled on Replace.
	bg, bgCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer bgCancel()

	if res.Success {
		r.Recorder.Eventf(job, "Normal", "RunSucceeded", "HTTP %d from %s", res.StatusCode, job.Spec.Endpoint)
	} else {
		r.Recorder.Eventf(job, "Warning", "RunFailed", "%s", message)
	}

	result := cronopsv1alpha1.RunResult{
		StartedAt:      started,
		FinishedAt:     &finished,
		Result:         cronopsv1alpha1.ResultFailed,
		HTTPStatusCode: int32(res.StatusCode),
		Message:        message,
		DurationMs:     finished.Sub(started.Time).Milliseconds(),
		ResponseBody:   res.Body,
		Attempts:       res.Attempts,
		Trigger:        trigger,
	}
	if res.Success {
		result.Result = cronopsv1alpha1.ResultSuccess
	}
	if err := recordJobRunResult(bg, r.Client, jobKey, result); err != nil {
		log.Error(err, "recording run result on job")
	}

	// The terminal phase goes last, so anyone who observed the run finish
	// also sees the result already published on the parent job.
	if err := r.patchRunStatus(bg, runKey, func(st *cronopsv1alpha1.HttpCronJobRunStatus) {
		st.Phase = phase
		st.StartedAt = &started
		st.FinishedAt = &finished
		st.HTTPStatusCode = int32(res.StatusCode)
		st.Message = message
		st.DurationMs = finished.Sub(started.Time).Milliseconds()
		st.ResponseBody = res.Body
		st.Attempts = res.Attempts
	}); err != nil {
		log.Error(err, "recording run status")
	}

	if err := r.pruneRuns(bg, jobKey, job.RunHistoryLimitOrDefault()); err != nil {
		log.Error(err, "pruning old runs")
	}
}

// finishRun marks a run terminal without executing it.
func (r *HttpCronJobRunReconciler) finishRun(ctx context.Context, key types.NamespacedName, mutate func(*cronopsv1alpha1.HttpCronJobRunStatus)) error {
	now := metav1.Now()
	return r.patchRunStatus(ctx, key, func(st *cronopsv1alpha1.HttpCronJobRunStatus) {
		mutate(st)
		if st.FinishedAt == nil {
			st.FinishedAt = &now
		}
	})
}

// patchRunStatus applies mutate to the freshest copy of the run's status,
// retrying on optimistic-concurrency conflicts.
func (r *HttpCronJobRunReconciler) patchRunStatus(ctx context.Context, key types.NamespacedName, mutate func(*cronopsv1alpha1.HttpCronJobRunStatus)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest cronopsv1alpha1.HttpCronJobRun
		if err := r.Get(ctx, key, &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		mutate(&latest.Status)
		return r.Status().Update(ctx, &latest)
	})
}

// cleanupFinished deletes the run once the parent job's TTL elapses, or
// schedules a requeue for the remaining time. Without a TTL the count-based
// pruning after each run is the only garbage collection.
func (r *HttpCronJobRunReconciler) cleanupFinished(ctx context.Context, run *cronopsv1alpha1.HttpCronJobRun, jobKey types.NamespacedName) (ctrl.Result, error) {
	var job cronopsv1alpha1.HttpCronJob
	if err := r.Get(ctx, jobKey, &job); err != nil {
		// Job gone: owner-reference GC removes the runs.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ttl := job.Spec.RunTTLSecondsAfterFinished
	if ttl == nil || run.Status.FinishedAt == nil {
		return ctrl.Result{}, nil
	}
	expiry := run.Status.FinishedAt.Add(time.Duration(*ttl) * time.Second)
	if remaining := time.Until(expiry); remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}
	return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, run))
}

// pruneRuns keeps at most limit finished runs per job, deleting oldest first.
func (r *HttpCronJobRunReconciler) pruneRuns(ctx context.Context, jobKey types.NamespacedName, limit int32) error {
	var runs cronopsv1alpha1.HttpCronJobRunList
	if err := r.List(ctx, &runs,
		client.InNamespace(jobKey.Namespace),
		client.MatchingLabels{cronopsv1alpha1.LabelCronJob: jobKey.Name}); err != nil {
		return err
	}
	finished := make([]*cronopsv1alpha1.HttpCronJobRun, 0, len(runs.Items))
	for i := range runs.Items {
		if runs.Items[i].Finished() {
			finished = append(finished, &runs.Items[i])
		}
	}
	if len(finished) <= int(limit) {
		return nil
	}
	sort.Slice(finished, func(i, j int) bool { // newest first
		return finished[i].CreationTimestamp.After(finished[j].CreationTimestamp.Time)
	})
	for _, old := range finished[limit:] {
		if err := client.IgnoreNotFound(r.Delete(ctx, old)); err != nil {
			return err
		}
	}
	return nil
}

// SetupWithManager wires the reconciler into the manager.
func (r *HttpCronJobRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cronopsv1alpha1.HttpCronJobRun{}).
		Named("httpcronjobrun").
		Complete(r)
}
