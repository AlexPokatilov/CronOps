package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
	"github.com/AlexPokatilov/CronOps/internal/executor"
	"github.com/AlexPokatilov/CronOps/internal/scheduler"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cronopsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func newReconciler(t *testing.T, objs ...client.Object) (*HttpCronJobReconciler, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&cronopsv1alpha1.HttpCronJob{}, &cronopsv1alpha1.HttpCronJobRun{}).
		Build()
	r := &HttpCronJobReconciler{
		Client:    c,
		Recorder:  record.NewFakeRecorder(32),
		Scheduler: scheduler.New(),
		Tracker:   NewRunTracker(),
	}
	return r, c
}

// newRunReconciler shares the tracker with the job reconciler, mirroring the
// real wiring in cmd/controller.
func newRunReconciler(t *testing.T, r *HttpCronJobReconciler, c client.Client) *HttpCronJobRunReconciler {
	t.Helper()
	return &HttpCronJobRunReconciler{
		Client:   c,
		Recorder: record.NewFakeRecorder(32),
		Executor: executor.New(c),
		Tracker:  r.Tracker,
	}
}

func testJob(mutate ...func(*cronopsv1alpha1.HttpCronJob)) *cronopsv1alpha1.HttpCronJob {
	job := &cronopsv1alpha1.HttpCronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "job", Namespace: "ns", Generation: 1},
		Spec: cronopsv1alpha1.HttpCronJobSpec{
			Schedule: "0 3 * * *",
			Endpoint: "https://example.com/hook",
			Method:   "POST",
		},
	}
	for _, m := range mutate {
		m(job)
	}
	return job
}

func reconcileOnce(t *testing.T, r *HttpCronJobReconciler, key types.NamespacedName) {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}
}

func getJob(t *testing.T, c client.Client, key types.NamespacedName) *cronopsv1alpha1.HttpCronJob {
	t.Helper()
	var job cronopsv1alpha1.HttpCronJob
	if err := c.Get(context.Background(), key, &job); err != nil {
		t.Fatal(err)
	}
	return &job
}

func TestReconcileActive(t *testing.T) {
	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	r, c := newReconciler(t, testJob())

	reconcileOnce(t, r, key)

	job := getJob(t, c, key)
	if job.Status.Phase != cronopsv1alpha1.PhaseActive {
		t.Fatalf("phase = %q, want Active", job.Status.Phase)
	}
	if job.Status.NextScheduleTime == nil {
		t.Fatal("nextScheduleTime not set")
	}
	if r.Scheduler.Len() != 1 {
		t.Fatalf("scheduler entries = %d, want 1", r.Scheduler.Len())
	}
	// Idempotent: a second reconcile keeps the same single entry.
	reconcileOnce(t, r, key)
	if r.Scheduler.Len() != 1 {
		t.Fatalf("scheduler entries after second reconcile = %d, want 1", r.Scheduler.Len())
	}
}

func TestReconcileSuspended(t *testing.T) {
	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	r, c := newReconciler(t, testJob(func(j *cronopsv1alpha1.HttpCronJob) {
		j.Spec.Suspend = ptr.To(true)
	}))

	reconcileOnce(t, r, key)

	job := getJob(t, c, key)
	if job.Status.Phase != cronopsv1alpha1.PhaseSuspended {
		t.Fatalf("phase = %q, want Suspended", job.Status.Phase)
	}
	if job.Status.NextScheduleTime != nil {
		t.Fatal("nextScheduleTime must be cleared while suspended")
	}
	if r.Scheduler.Len() != 0 {
		t.Fatalf("scheduler entries = %d, want 0", r.Scheduler.Len())
	}
}

func TestReconcileInvalidSchedule(t *testing.T) {
	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	r, c := newReconciler(t, testJob(func(j *cronopsv1alpha1.HttpCronJob) {
		j.Spec.Schedule = "not a cron"
	}))

	reconcileOnce(t, r, key)

	job := getJob(t, c, key)
	if job.Status.Phase != cronopsv1alpha1.PhaseInvalid {
		t.Fatalf("phase = %q, want Invalid", job.Status.Phase)
	}
	if r.Scheduler.Len() != 0 {
		t.Fatalf("scheduler entries = %d, want 0", r.Scheduler.Len())
	}
}

func TestReconcileDeleted(t *testing.T) {
	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	r, c := newReconciler(t, testJob())

	reconcileOnce(t, r, key)
	if r.Scheduler.Len() != 1 {
		t.Fatal("expected an entry before deletion")
	}
	if err := c.Delete(context.Background(), testJob()); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, key)
	if r.Scheduler.Len() != 0 {
		t.Fatalf("scheduler entries after delete = %d, want 0", r.Scheduler.Len())
	}
}

// runJobOnce fires the cron callback and drives the created run through the
// run reconciler to completion, like the real two-controller pipeline.
func runJobOnce(t *testing.T, r *HttpCronJobReconciler, rr *HttpCronJobRunReconciler, c client.Client, key types.NamespacedName) {
	t.Helper()
	r.runJob(key)
	ctx := context.Background()
	var runs cronopsv1alpha1.HttpCronJobRunList
	if err := c.List(ctx, &runs, client.InNamespace(key.Namespace)); err != nil {
		t.Fatal(err)
	}
	for i := range runs.Items {
		if runs.Items[i].Status.Phase != "" {
			continue
		}
		req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&runs.Items[i])}
		if _, err := rr.Reconcile(ctx, req); err != nil {
			t.Fatalf("run reconcile: %v", err)
		}
	}
	waitRunsFinished(t, c, key.Namespace)
}

// waitRunsFinished blocks until every run in the namespace reached a terminal
// phase (execution happens in goroutines).
func waitRunsFinished(t *testing.T, c client.Client, ns string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var runs cronopsv1alpha1.HttpCronJobRunList
		if err := c.List(context.Background(), &runs, client.InNamespace(ns)); err != nil {
			t.Fatal(err)
		}
		done := true
		for i := range runs.Items {
			if !runs.Items[i].Finished() {
				done = false
			}
		}
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("runs did not finish in time")
}

func listRuns(t *testing.T, c client.Client, ns string) []cronopsv1alpha1.HttpCronJobRun {
	t.Helper()
	var runs cronopsv1alpha1.HttpCronJobRunList
	if err := c.List(context.Background(), &runs, client.InNamespace(ns)); err != nil {
		t.Fatal(err)
	}
	return runs.Items
}

func TestRunJobRecordsResult(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	r, c := newReconciler(t, testJob(func(j *cronopsv1alpha1.HttpCronJob) {
		j.Spec.Endpoint = ts.URL
		j.Spec.HistoryLimit = ptr.To(int32(2))
	}))
	rr := newRunReconciler(t, r, c)

	runJobOnce(t, r, rr, c, key)
	job := getJob(t, c, key)
	if job.Status.LastRun == nil || job.Status.LastRun.Result != cronopsv1alpha1.ResultSuccess {
		t.Fatalf("lastRun = %+v, want Success", job.Status.LastRun)
	}
	if job.Status.LastRun.Trigger != cronopsv1alpha1.TriggerSchedule {
		t.Fatalf("trigger = %q, want Schedule", job.Status.LastRun.Trigger)
	}
	if len(job.Status.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(job.Status.History))
	}
	runs := listRuns(t, c, "ns")
	if len(runs) != 1 || runs[0].Status.Phase != cronopsv1alpha1.RunPhaseSucceeded {
		t.Fatalf("runs = %+v, want one Succeeded run object", runs)
	}

	// History is capped at historyLimit, newest first.
	runJobOnce(t, r, rr, c, key)
	runJobOnce(t, r, rr, c, key)
	job = getJob(t, c, key)
	if len(job.Status.History) != 2 {
		t.Fatalf("history length = %d, want 2 (capped)", len(job.Status.History))
	}
}

func TestRunJobFailureRecorded(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	r, c := newReconciler(t, testJob(func(j *cronopsv1alpha1.HttpCronJob) {
		j.Spec.Endpoint = ts.URL
	}))
	rr := newRunReconciler(t, r, c)

	runJobOnce(t, r, rr, c, key)
	job := getJob(t, c, key)
	if job.Status.LastRun == nil || job.Status.LastRun.Result != cronopsv1alpha1.ResultFailed {
		t.Fatalf("lastRun = %+v, want Failed", job.Status.LastRun)
	}
	if job.Status.LastRun.HTTPStatusCode != http.StatusBadGateway {
		t.Fatalf("httpStatusCode = %d, want 502", job.Status.LastRun.HTTPStatusCode)
	}
}

func TestRunJobSkipsSuspended(t *testing.T) {
	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	r, c := newReconciler(t, testJob(func(j *cronopsv1alpha1.HttpCronJob) {
		j.Spec.Suspend = ptr.To(true)
		j.Spec.Endpoint = "http://127.0.0.1:1"
	}))

	r.runJob(key)
	if runs := listRuns(t, c, "ns"); len(runs) != 0 {
		t.Fatalf("runs = %d, want none for suspended job", len(runs))
	}
}

func TestManualRunOnSuspendedJob(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	r, c := newReconciler(t, testJob(func(j *cronopsv1alpha1.HttpCronJob) {
		j.Spec.Suspend = ptr.To(true)
		j.Spec.Endpoint = ts.URL
	}))
	rr := newRunReconciler(t, r, c)

	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	job := getJob(t, c, key)
	run := cronopsv1alpha1.NewRunForJob(job, cronopsv1alpha1.TriggerManual)
	if err := c.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}
	if _, err := rr.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	waitRunsFinished(t, c, "ns")

	job = getJob(t, c, key)
	if job.Status.LastRun == nil || job.Status.LastRun.Trigger != cronopsv1alpha1.TriggerManual {
		t.Fatalf("lastRun = %+v, want a Manual run recorded", job.Status.LastRun)
	}
}

func TestRunPruning(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	r, c := newReconciler(t, testJob(func(j *cronopsv1alpha1.HttpCronJob) {
		j.Spec.Endpoint = ts.URL
		j.Spec.RunHistoryLimit = ptr.To(int32(2))
	}))
	rr := newRunReconciler(t, r, c)

	for range 4 {
		runJobOnce(t, r, rr, c, key)
	}
	// Pruning runs in the execute goroutine after the terminal status write,
	// so give it a moment to converge.
	deadline := time.Now().Add(5 * time.Second)
	for {
		runs := listRuns(t, c, "ns")
		if len(runs) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runs = %d, want pruned to runHistoryLimit=2", len(runs))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunTTLCleanup(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	key := types.NamespacedName{Namespace: "ns", Name: "job"}
	r, c := newReconciler(t, testJob(func(j *cronopsv1alpha1.HttpCronJob) {
		j.Spec.Endpoint = ts.URL
		j.Spec.RunTTLSecondsAfterFinished = ptr.To(int32(3600))
	}))
	rr := newRunReconciler(t, r, c)

	runJobOnce(t, r, rr, c, key)
	runs := listRuns(t, c, "ns")
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&runs[0])}

	// Fresh run: reconcile keeps it and requeues for the remaining TTL.
	res, err := rr.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("RequeueAfter = %v, want positive TTL requeue", res.RequeueAfter)
	}

	// Expired run: backdate finishedAt past the TTL and reconcile again.
	run := runs[0]
	old := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	run.Status.FinishedAt = &old
	if err := c.Status().Update(context.Background(), &run); err != nil {
		t.Fatal(err)
	}
	if _, err := rr.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if runs := listRuns(t, c, "ns"); len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 after TTL delete", len(runs))
	}
}
