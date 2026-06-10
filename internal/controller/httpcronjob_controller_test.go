package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
		WithStatusSubresource(&cronopsv1alpha1.HttpCronJob{}).
		Build()
	r := &HttpCronJobReconciler{
		Client:    c,
		Recorder:  record.NewFakeRecorder(32),
		Scheduler: scheduler.New(),
		Executor:  executor.New(c),
	}
	return r, c
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

	r.runJob(key)
	job := getJob(t, c, key)
	if job.Status.LastRun == nil || job.Status.LastRun.Result != cronopsv1alpha1.ResultSuccess {
		t.Fatalf("lastRun = %+v, want Success", job.Status.LastRun)
	}
	if len(job.Status.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(job.Status.History))
	}

	// History is capped at historyLimit, newest first.
	r.runJob(key)
	r.runJob(key)
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

	r.runJob(key)
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
	job := getJob(t, c, key)
	if job.Status.LastRun != nil {
		t.Fatalf("lastRun = %+v, want nil for suspended job", job.Status.LastRun)
	}
}
