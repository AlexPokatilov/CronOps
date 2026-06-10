package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
)

func TestCodeMatches(t *testing.T) {
	tests := []struct {
		code     int
		patterns []string
		want     bool
	}{
		{200, nil, true},
		{204, nil, true},
		{301, nil, false},
		{500, nil, false},
		{404, []string{"404"}, true},
		{404, []string{"2xx", "404"}, true},
		{503, []string{"2xx", "404"}, false},
		{201, []string{"2xx"}, true},
		{301, []string{"3xx"}, true},
		{200, []string{"5xx"}, false},
	}
	for _, tt := range tests {
		if got := CodeMatches(tt.code, tt.patterns); got != tt.want {
			t.Errorf("CodeMatches(%d, %v) = %v, want %v", tt.code, tt.patterns, got, tt.want)
		}
	}
}

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

func baseJob(endpoint string) *cronopsv1alpha1.HttpCronJob {
	return &cronopsv1alpha1.HttpCronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "ns"},
		Spec: cronopsv1alpha1.HttpCronJobSpec{
			Schedule: "* * * * *",
			Endpoint: endpoint,
			Method:   http.MethodPost,
			Body:     `{"hello":"world"}`,
			Headers: []cronopsv1alpha1.HeaderSpec{
				{Name: "Content-Type", Value: "application/json"},
			},
		},
	}
}

func TestRunSuccess(t *testing.T) {
	var gotMethod, gotHeader, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())
	res := e.Run(context.Background(), baseJob(ts.URL))

	if !res.Success {
		t.Fatalf("Run() = %+v, want success", res)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", res.StatusCode)
	}
	if gotMethod != http.MethodPost || gotHeader != "application/json" || gotBody != `{"hello":"world"}` {
		t.Fatalf("request not built from spec: method=%q header=%q body=%q", gotMethod, gotHeader, gotBody)
	}
}

func TestRunFailureStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())
	res := e.Run(context.Background(), baseJob(ts.URL))
	if res.Success {
		t.Fatalf("Run() = %+v, want failure", res)
	}
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("StatusCode = %d, want 503", res.StatusCode)
	}
}

func TestRunBearerAuth(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "api-token", Namespace: "ns"},
		Data:       map[string][]byte{"token": []byte("s3cr3t")},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(secret).Build()

	job := baseJob(ts.URL)
	job.Spec.Auth = &cronopsv1alpha1.AuthSpec{
		Type:      cronopsv1alpha1.AuthTypeBearer,
		SecretRef: &cronopsv1alpha1.SecretKeyRef{Name: "api-token"},
	}
	res := New(c).Run(context.Background(), job)
	if !res.Success {
		t.Fatalf("Run() = %+v, want success", res)
	}
	if gotAuth != "Bearer s3cr3t" {
		t.Fatalf("Authorization = %q, want Bearer s3cr3t", gotAuth)
	}
}

func TestRunAPIKeyAuth(t *testing.T) {
	var gotKey string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Custom-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "api-token", Namespace: "ns"},
		Data:       map[string][]byte{"apikey": []byte("k3y")},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(secret).Build()

	job := baseJob(ts.URL)
	job.Spec.Auth = &cronopsv1alpha1.AuthSpec{
		Type:       cronopsv1alpha1.AuthTypeAPIKey,
		SecretRef:  &cronopsv1alpha1.SecretKeyRef{Name: "api-token", Key: "apikey"},
		HeaderName: "X-Custom-Key",
	}
	res := New(c).Run(context.Background(), job)
	if !res.Success {
		t.Fatalf("Run() = %+v, want success", res)
	}
	if gotKey != "k3y" {
		t.Fatalf("X-Custom-Key = %q, want k3y", gotKey)
	}
}

func TestRunMissingSecret(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	job := baseJob("http://127.0.0.1:1") // never reached
	job.Spec.Auth = &cronopsv1alpha1.AuthSpec{
		Type:      cronopsv1alpha1.AuthTypeBearer,
		SecretRef: &cronopsv1alpha1.SecretKeyRef{Name: "missing"},
	}
	res := New(c).Run(context.Background(), job)
	if res.Success {
		t.Fatal("Run() succeeded, want auth resolution failure")
	}
}

func TestValidateAuth(t *testing.T) {
	if err := ValidateAuth(nil); err != nil {
		t.Errorf("nil auth: %v", err)
	}
	if err := ValidateAuth(&cronopsv1alpha1.AuthSpec{Type: "none"}); err != nil {
		t.Errorf("none auth: %v", err)
	}
	if err := ValidateAuth(&cronopsv1alpha1.AuthSpec{Type: "bearer"}); err == nil {
		t.Error("bearer without secretRef: want error")
	}
	if err := ValidateAuth(&cronopsv1alpha1.AuthSpec{Type: "weird"}); err == nil {
		t.Error("unknown type: want error")
	}
}
