package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
	"github.com/AlexPokatilov/CronOps/internal/auth"
)

func newTestServer(t *testing.T, objs ...client.Object) (*Server, http.Handler) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cronopsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	authSvc, err := auth.New(auth.Config{
		Client:    c,
		Namespace: "cronops",
		DevUsers:  map[string]string{"admin": "admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Client: c, Auth: authSvc}
	return s, s.Handler()
}

func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"admin"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	t.Fatal("no session cookie issued")
	return nil
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	_, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"wrong"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAPIRequiresAuth(t *testing.T) {
	_, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cronjobs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCreateListGetDelete(t *testing.T) {
	_, h := newTestServer(t)
	cookie := login(t, h)

	body := `{"name":"ping","namespace":"default","spec":{"schedule":"*/5 * * * *","endpoint":"https://example.com/ping","method":"GET"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cronjobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/cronjobs", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var list struct {
		Items []jobView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "ping" {
		t.Fatalf("list = %+v, want one item named ping", list.Items)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/cronjobs/default/ping", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/cronjobs/default/ping", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
}

func TestCreateFromYAML(t *testing.T) {
	_, h := newTestServer(t)
	cookie := login(t, h)

	manifest := `apiVersion: cronops.io/v1alpha1
kind: HttpCronJob
metadata:
  name: yaml-job
  namespace: default
spec:
  schedule: "0 3 * * *"
  endpoint: https://example.com/run
  method: POST
`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cronjobs", strings.NewReader(manifest))
	req.Header.Set("Content-Type", "application/yaml")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("yaml create status = %d, body %s", rec.Code, rec.Body.String())
	}

	// Wrong kind is rejected.
	bad := strings.Replace(manifest, "kind: HttpCronJob", "kind: ConfigMap", 1)
	bad = strings.Replace(bad, "name: yaml-job", "name: bad-kind", 1)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/cronjobs", strings.NewReader(bad))
	req.Header.Set("Content-Type", "application/yaml")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind status = %d, want 400", rec.Code)
	}
}

func TestSuspendToggle(t *testing.T) {
	job := &cronopsv1alpha1.HttpCronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "job", Namespace: "default"},
		Spec: cronopsv1alpha1.HttpCronJobSpec{
			Schedule: "0 3 * * *", Endpoint: "https://example.com", Method: "GET",
		},
	}
	_, h := newTestServer(t, job)
	cookie := login(t, h)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/cronjobs/default/job/suspend",
		strings.NewReader(`{"suspend":true}`))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend status = %d, body %s", rec.Code, rec.Body.String())
	}
	var view jobView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Spec.Suspend == nil || !*view.Spec.Suspend {
		t.Fatalf("spec.suspend = %v, want true", view.Spec.Suspend)
	}
}

func TestStats(t *testing.T) {
	now := metav1.Now()
	active := &cronopsv1alpha1.HttpCronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "default"},
		Spec:       cronopsv1alpha1.HttpCronJobSpec{Schedule: "* * * * *", Endpoint: "https://x", Method: "GET"},
		Status: cronopsv1alpha1.HttpCronJobStatus{
			Phase: cronopsv1alpha1.PhaseActive,
			LastRun: &cronopsv1alpha1.RunResult{
				StartedAt: now, Result: cronopsv1alpha1.ResultSuccess, HTTPStatusCode: 200,
			},
			History: []cronopsv1alpha1.RunResult{
				{StartedAt: now, Result: cronopsv1alpha1.ResultSuccess, HTTPStatusCode: 200},
			},
		},
	}
	suspended := &cronopsv1alpha1.HttpCronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "default"},
		Spec:       cronopsv1alpha1.HttpCronJobSpec{Schedule: "* * * * *", Endpoint: "https://x", Method: "GET"},
		Status: cronopsv1alpha1.HttpCronJobStatus{
			Phase: cronopsv1alpha1.PhaseSuspended,
			LastRun: &cronopsv1alpha1.RunResult{
				StartedAt: now, Result: cronopsv1alpha1.ResultFailed, HTTPStatusCode: 500,
			},
		},
	}
	_, h := newTestServer(t, active, suspended)
	cookie := login(t, h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status = %d", rec.Code)
	}
	var stats statsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Total != 2 || stats.Active != 1 || stats.Suspended != 1 {
		t.Fatalf("stats = %+v, want total=2 active=1 suspended=1", stats)
	}
	if stats.LastRuns.Success != 1 || stats.LastRuns.Failed != 1 {
		t.Fatalf("lastRuns = %+v, want 1/1", stats.LastRuns)
	}
	if len(stats.Recent) != 1 {
		t.Fatalf("recentRuns = %d, want 1", len(stats.Recent))
	}
}
