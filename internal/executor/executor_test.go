package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
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

func TestRunCapturesResponseBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"queued","id":42}`))
	}))
	defer ts.Close()

	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())
	res := e.Run(context.Background(), baseJob(ts.URL))
	if res.Body != `{"status":"queued","id":42}` {
		t.Fatalf("Body = %q, want response body captured", res.Body)
	}
}

func TestRunTruncatesResponseBody(t *testing.T) {
	big := make([]byte, 10<<10) // 10 KiB
	for i := range big {
		big[i] = 'x'
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(big)
	}))
	defer ts.Close()

	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())
	res := e.Run(context.Background(), baseJob(ts.URL))
	if len(res.Body) != maxCaptureBytes {
		t.Fatalf("len(Body) = %d, want truncated to %d", len(res.Body), maxCaptureBytes)
	}
}

func TestRunCaptureDisabled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("sensitive"))
	}))
	defer ts.Close()

	job := baseJob(ts.URL)
	off := false
	job.Spec.CaptureResponseBody = &off

	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())
	res := e.Run(context.Background(), job)
	if res.Body != "" {
		t.Fatalf("Body = %q, want empty when capture is disabled", res.Body)
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

func TestRunRetriesUntilSuccess(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	job := baseJob(ts.URL)
	job.Spec.Retry = &cronopsv1alpha1.RetrySpec{
		MaxAttempts:    ptr.To(int32(3)),
		BackoffSeconds: ptr.To(int32(1)),
	}
	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())
	res := e.Run(context.Background(), job)
	if !res.Success {
		t.Fatalf("Run() = %+v, want success after retries", res)
	}
	if res.Attempts != 3 || calls != 3 {
		t.Fatalf("attempts = %d (calls %d), want 3", res.Attempts, calls)
	}
}

func TestRunRetriesExhausted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	job := baseJob(ts.URL)
	job.Spec.Retry = &cronopsv1alpha1.RetrySpec{
		MaxAttempts:    ptr.To(int32(2)),
		BackoffSeconds: ptr.To(int32(1)),
	}
	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())
	res := e.Run(context.Background(), job)
	if res.Success || res.Attempts != 2 {
		t.Fatalf("Run() = %+v, want failure after 2 attempts", res)
	}
}

func TestRunBodyRegexCriteria(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`job finished: OK`))
	}))
	defer ts.Close()

	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())

	job := baseJob(ts.URL)
	job.Spec.SuccessCriteria = &cronopsv1alpha1.SuccessCriteriaSpec{BodyRegex: `finished: OK`}
	if res := e.Run(context.Background(), job); !res.Success {
		t.Fatalf("Run() = %+v, want success on matching regex", res)
	}

	job.Spec.SuccessCriteria = &cronopsv1alpha1.SuccessCriteriaSpec{BodyRegex: `finished: FAILED`}
	if res := e.Run(context.Background(), job); res.Success {
		t.Fatalf("Run() = %+v, want failure on non-matching regex", res)
	}
}

func TestRunJSONPathCriteria(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"done","items":3}`))
	}))
	defer ts.Close()

	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())

	job := baseJob(ts.URL)
	job.Spec.SuccessCriteria = &cronopsv1alpha1.SuccessCriteriaSpec{JSONPath: ".status", Value: "done"}
	if res := e.Run(context.Background(), job); !res.Success {
		t.Fatalf("Run() = %+v, want success on matching jsonPath value", res)
	}

	job.Spec.SuccessCriteria = &cronopsv1alpha1.SuccessCriteriaSpec{JSONPath: "{.status}", Value: "pending"}
	if res := e.Run(context.Background(), job); res.Success {
		t.Fatalf("Run() = %+v, want failure on wrong jsonPath value", res)
	}

	job.Spec.SuccessCriteria = &cronopsv1alpha1.SuccessCriteriaSpec{JSONPath: ".missing"}
	if res := e.Run(context.Background(), job); res.Success {
		t.Fatalf("Run() = %+v, want failure on unresolved jsonPath", res)
	}
}

func TestRunJSONPathEmptyValue(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"","error":""}`))
	}))
	defer ts.Close()

	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())

	// A field that legitimately resolves to "" must pass an existence check.
	job := baseJob(ts.URL)
	job.Spec.SuccessCriteria = &cronopsv1alpha1.SuccessCriteriaSpec{JSONPath: ".error"}
	if res := e.Run(context.Background(), job); !res.Success {
		t.Fatalf("Run() = %+v, want success when path resolves to empty string", res)
	}
}

func TestRunCriteriaBodyTooLarge(t *testing.T) {
	big := make([]byte, (1<<20)+100) // just over maxDrainBytes
	for i := range big {
		big[i] = 'x'
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(big)
	}))
	defer ts.Close()

	job := baseJob(ts.URL)
	job.Spec.SuccessCriteria = &cronopsv1alpha1.SuccessCriteriaSpec{BodyRegex: "x+"}
	e := New(fake.NewClientBuilder().WithScheme(newScheme(t)).Build())
	res := e.Run(context.Background(), job)
	if res.Success {
		t.Fatalf("Run() = %+v, want explicit failure on truncated body", res)
	}
	if !strings.Contains(res.Message, "exceeds") {
		t.Fatalf("message = %q, want a clear truncation error", res.Message)
	}
}

func TestValidateCriteria(t *testing.T) {
	if err := ValidateCriteria(nil); err != nil {
		t.Errorf("nil criteria: %v", err)
	}
	if err := ValidateCriteria(&cronopsv1alpha1.SuccessCriteriaSpec{}); err == nil {
		t.Error("empty criteria: want error")
	}
	if err := ValidateCriteria(&cronopsv1alpha1.SuccessCriteriaSpec{BodyRegex: "("}); err == nil {
		t.Error("broken regex: want error")
	}
	if err := ValidateCriteria(&cronopsv1alpha1.SuccessCriteriaSpec{JSONPath: ".ok"}); err != nil {
		t.Errorf("valid jsonPath: %v", err)
	}
	if err := ValidateCriteria(&cronopsv1alpha1.SuccessCriteriaSpec{Value: "x"}); err == nil {
		t.Error("value without jsonPath: want error")
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
