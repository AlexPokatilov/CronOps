// Package controller reconciles HttpCronJob resources into cron entries and
// writes run results back to their status, keeping etcd the single source of
// truth for both desired and observed state.
package controller

import (
	"context"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
	"github.com/AlexPokatilov/CronOps/internal/executor"
	"github.com/AlexPokatilov/CronOps/internal/scheduler"
)

// HttpCronJobReconciler reconciles HttpCronJob objects.
type HttpCronJobReconciler struct {
	client.Client
	Recorder  record.EventRecorder
	Scheduler *scheduler.Scheduler
	Executor  *executor.Executor

	// running tracks in-flight runs per resource for ConcurrencyPolicy=Forbid.
	running sync.Map
}

// +kubebuilder:rbac:groups=cronops.io,resources=httpcronjobs,verbs=get;list;watch
// +kubebuilder:rbac:groups=cronops.io,resources=httpcronjobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile brings the in-memory cron registry in line with the resource and
// publishes the resulting phase/next-run into status. It is idempotent.
func (r *HttpCronJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var job cronopsv1alpha1.HttpCronJob
	if err := r.Get(ctx, req.NamespacedName, &job); err != nil {
		if apierrors.IsNotFound(err) {
			r.Scheduler.Remove(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	cronSpec, err := scheduler.BuildCronSpec(job.Spec.Schedule, job.Spec.Timezone)
	if err == nil {
		err = executor.ValidateAuth(job.Spec.Auth)
	}
	if err != nil {
		r.Scheduler.Remove(req.NamespacedName)
		r.Recorder.Event(&job, "Warning", "InvalidSpec", err.Error())
		return ctrl.Result{}, r.patchStatus(ctx, &job, func(st *cronopsv1alpha1.HttpCronJobStatus) {
			st.Phase = cronopsv1alpha1.PhaseInvalid
			st.ObservedGeneration = job.Generation
			st.NextScheduleTime = nil
			setCondition(st, metav1.ConditionFalse, "InvalidSpec", err.Error(), job.Generation)
		})
	}

	if job.IsSuspended() {
		r.Scheduler.Remove(req.NamespacedName)
		return ctrl.Result{}, r.patchStatus(ctx, &job, func(st *cronopsv1alpha1.HttpCronJobStatus) {
			st.Phase = cronopsv1alpha1.PhaseSuspended
			st.ObservedGeneration = job.Generation
			st.NextScheduleTime = nil
			setCondition(st, metav1.ConditionFalse, "Suspended", "scheduling is suspended via spec.suspend", job.Generation)
		})
	}

	// The callback captures only the key and re-reads the resource at fire
	// time, so spec edits that keep the same schedule need no re-registration.
	key := req.NamespacedName
	if err := r.Scheduler.Upsert(key, cronSpec, func() { r.runJob(key) }); err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("scheduled", "spec", cronSpec)

	next, err := scheduler.NextRun(cronSpec, time.Now())
	if err != nil {
		return ctrl.Result{}, err
	}
	nextMeta := metav1.NewTime(next)
	return ctrl.Result{}, r.patchStatus(ctx, &job, func(st *cronopsv1alpha1.HttpCronJobStatus) {
		st.Phase = cronopsv1alpha1.PhaseActive
		st.ObservedGeneration = job.Generation
		st.NextScheduleTime = &nextMeta
		setCondition(st, metav1.ConditionTrue, "ValidSchedule", "registered in scheduler", job.Generation)
	})
}

// runJob is the cron callback: it re-reads the resource, honours suspend and
// concurrency policy, executes the HTTP call and records the result.
func (r *HttpCronJobReconciler) runJob(key types.NamespacedName) {
	ctx := context.Background()
	log := logf.Log.WithName("runner").WithValues("httpcronjob", key.String())

	var job cronopsv1alpha1.HttpCronJob
	if err := r.Get(ctx, key, &job); err != nil {
		if apierrors.IsNotFound(err) {
			r.Scheduler.Remove(key)
			return
		}
		log.Error(err, "fetching job before run")
		return
	}
	if job.IsSuspended() || job.Status.Phase == cronopsv1alpha1.PhaseInvalid {
		return
	}

	if job.Spec.ConcurrencyPolicy != cronopsv1alpha1.ConcurrencyAllow {
		if _, inFlight := r.running.LoadOrStore(key.String(), struct{}{}); inFlight {
			r.Recorder.Event(&job, "Warning", "SkippedConcurrent",
				"previous run still in progress, skipping (concurrencyPolicy: Forbid)")
			return
		}
		defer r.running.Delete(key.String())
	}

	started := metav1.Now()
	res := r.Executor.Run(ctx, &job)
	finished := metav1.Now()

	run := cronopsv1alpha1.RunResult{
		StartedAt:      started,
		FinishedAt:     &finished,
		Result:         cronopsv1alpha1.ResultFailed,
		HTTPStatusCode: int32(res.StatusCode),
		Message:        res.Message,
		DurationMs:     finished.Time.Sub(started.Time).Milliseconds(),
		ResponseBody:   res.Body,
	}
	if res.Success {
		run.Result = cronopsv1alpha1.ResultSuccess
		r.Recorder.Eventf(&job, "Normal", "RunSucceeded", "HTTP %d from %s", res.StatusCode, job.Spec.Endpoint)
	} else {
		r.Recorder.Eventf(&job, "Warning", "RunFailed", "%s", res.Message)
	}

	cronSpec, specErr := scheduler.BuildCronSpec(job.Spec.Schedule, job.Spec.Timezone)
	if err := r.patchStatus(ctx, &job, func(st *cronopsv1alpha1.HttpCronJobStatus) {
		st.LastScheduleTime = &started
		st.LastRun = &run
		limit := int(job.HistoryLimitOrDefault())
		st.History = append([]cronopsv1alpha1.RunResult{run}, st.History...)
		if len(st.History) > limit {
			st.History = st.History[:limit]
		}
		if specErr == nil {
			if next, err := scheduler.NextRun(cronSpec, time.Now()); err == nil {
				nextMeta := metav1.NewTime(next)
				st.NextScheduleTime = &nextMeta
			}
		}
	}); err != nil {
		log.Error(err, "recording run result")
	}
}

// patchStatus applies mutate to the freshest copy of the object's status,
// retrying on optimistic-concurrency conflicts. No-op updates are skipped to
// avoid reconcile feedback loops.
func (r *HttpCronJobReconciler) patchStatus(ctx context.Context, job *cronopsv1alpha1.HttpCronJob, mutate func(*cronopsv1alpha1.HttpCronJobStatus)) error {
	key := client.ObjectKeyFromObject(job)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest cronopsv1alpha1.HttpCronJob
		if err := r.Get(ctx, key, &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		before := *latest.Status.DeepCopy()
		mutate(&latest.Status)
		if statusEqual(&before, &latest.Status) {
			return nil
		}
		return r.Status().Update(ctx, &latest)
	})
}

func statusEqual(a, b *cronopsv1alpha1.HttpCronJobStatus) bool {
	if a.Phase != b.Phase || a.ObservedGeneration != b.ObservedGeneration {
		return false
	}
	if !timePtrEqual(a.NextScheduleTime, b.NextScheduleTime) || !timePtrEqual(a.LastScheduleTime, b.LastScheduleTime) {
		return false
	}
	if !runEqual(a.LastRun, b.LastRun) {
		return false
	}
	if len(a.History) != len(b.History) {
		return false
	}
	ca, cb := meta.FindStatusCondition(a.Conditions, cronopsv1alpha1.ConditionScheduled), meta.FindStatusCondition(b.Conditions, cronopsv1alpha1.ConditionScheduled)
	if (ca == nil) != (cb == nil) {
		return false
	}
	if ca != nil && (ca.Status != cb.Status || ca.Reason != cb.Reason || ca.Message != cb.Message || ca.ObservedGeneration != cb.ObservedGeneration) {
		return false
	}
	return true
}

func runEqual(a, b *cronopsv1alpha1.RunResult) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.Result == b.Result &&
		a.HTTPStatusCode == b.HTTPStatusCode &&
		a.Message == b.Message &&
		a.DurationMs == b.DurationMs &&
		a.ResponseBody == b.ResponseBody &&
		a.StartedAt.Truncate(time.Second).Equal(b.StartedAt.Truncate(time.Second)) &&
		timePtrEqual(a.FinishedAt, b.FinishedAt)
}

func timePtrEqual(a, b *metav1.Time) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.Truncate(time.Second).Equal(b.Truncate(time.Second))
}

func setCondition(st *cronopsv1alpha1.HttpCronJobStatus, status metav1.ConditionStatus, reason, message string, generation int64) {
	meta.SetStatusCondition(&st.Conditions, metav1.Condition{
		Type:               cronopsv1alpha1.ConditionScheduled,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// SetupWithManager wires the reconciler into the manager.
func (r *HttpCronJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cronopsv1alpha1.HttpCronJob{}).
		Named("httpcronjob").
		Complete(r)
}
