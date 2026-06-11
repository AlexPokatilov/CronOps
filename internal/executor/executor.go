// Package executor performs the actual HTTP call for a HttpCronJob run.
package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
)

// maxDrainBytes caps how much of the response body is read before closing,
// protecting the controller from endpoints that stream unbounded data.
const maxDrainBytes = 1 << 20 // 1 MiB

// maxCaptureBytes caps the response body snippet stored in run history; it is
// kept small because history lives inside the resource status in etcd.
const maxCaptureBytes = 2 << 10 // 2 KiB

// Result is the outcome of a single HTTP run.
type Result struct {
	Success    bool
	StatusCode int
	Message    string
	// Body is a snippet of the response body, truncated to maxCaptureBytes
	// (empty when capture is disabled in the spec).
	Body string
}

// Executor builds and sends the HTTP request described by a HttpCronJob spec,
// resolving credentials from Secrets in the job's namespace.
type Executor struct {
	Client client.Client
	HTTP   *http.Client
}

func New(c client.Client) *Executor {
	return &Executor{
		Client: c,
		// Per-request deadlines come from spec.timeoutSeconds via context.
		HTTP: &http.Client{},
	}
}

// Run executes one HTTP call for the job and reports the outcome.
func (e *Executor) Run(ctx context.Context, job *cronopsv1alpha1.HttpCronJob) Result {
	timeout := time.Duration(job.TimeoutOrDefault()) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if job.Spec.Body != "" {
		body = strings.NewReader(job.Spec.Body)
	}
	req, err := http.NewRequestWithContext(ctx, job.Spec.Method, job.Spec.Endpoint, body)
	if err != nil {
		return Result{Message: fmt.Sprintf("building request: %v", err)}
	}
	for _, h := range job.Spec.Headers {
		req.Header.Set(h.Name, h.Value)
	}
	if err := e.applyAuth(ctx, job, req); err != nil {
		return Result{Message: fmt.Sprintf("resolving auth: %v", err)}
	}

	resp, err := e.HTTP.Do(req)
	if err != nil {
		return Result{Message: fmt.Sprintf("request failed: %v", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	var snippet string
	if job.CaptureResponseBodyOrDefault() {
		captured, _ := io.ReadAll(io.LimitReader(resp.Body, maxCaptureBytes))
		snippet = string(captured)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))

	res := Result{
		StatusCode: resp.StatusCode,
		Success:    CodeMatches(resp.StatusCode, job.Spec.SuccessHTTPCodes),
		Body:       snippet,
	}
	if !res.Success {
		res.Message = fmt.Sprintf("unexpected status %d", resp.StatusCode)
	}
	return res
}

func (e *Executor) applyAuth(ctx context.Context, job *cronopsv1alpha1.HttpCronJob, req *http.Request) error {
	auth := job.Spec.Auth
	if auth == nil || auth.Type == cronopsv1alpha1.AuthTypeNone || auth.Type == "" {
		return nil
	}
	if auth.SecretRef == nil {
		return fmt.Errorf("auth type %q requires secretRef", auth.Type)
	}
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: job.Namespace, Name: auth.SecretRef.Name}
	if err := e.Client.Get(ctx, key, &secret); err != nil {
		return fmt.Errorf("reading secret %s: %w", key, err)
	}

	switch auth.Type {
	case cronopsv1alpha1.AuthTypeBasic:
		user, okU := secret.Data["username"]
		pass, okP := secret.Data["password"]
		if !okU || !okP {
			return fmt.Errorf("secret %s must contain %q and %q keys", key, "username", "password")
		}
		req.SetBasicAuth(string(user), string(pass))
	case cronopsv1alpha1.AuthTypeBearer:
		token, err := secretValue(&secret, auth.SecretRef.Key, "token")
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	case cronopsv1alpha1.AuthTypeAPIKey:
		token, err := secretValue(&secret, auth.SecretRef.Key, "token")
		if err != nil {
			return err
		}
		header := auth.HeaderName
		if header == "" {
			header = "X-API-Key"
		}
		req.Header.Set(header, token)
	default:
		return fmt.Errorf("unsupported auth type %q", auth.Type)
	}
	return nil
}

func secretValue(secret *corev1.Secret, key, defaultKey string) (string, error) {
	if key == "" {
		key = defaultKey
	}
	v, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no key %q", secret.Namespace, secret.Name, key)
	}
	return string(v), nil
}

// CodeMatches reports whether an HTTP status code satisfies the success
// patterns: exact codes ("200") or classes ("2xx"). Defaults to ["2xx"].
func CodeMatches(code int, patterns []string) bool {
	if len(patterns) == 0 {
		patterns = []string{"2xx"}
	}
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if strings.HasSuffix(p, "xx") && len(p) == 3 {
			if class, err := strconv.Atoi(p[:1]); err == nil && code/100 == class {
				return true
			}
			continue
		}
		if exact, err := strconv.Atoi(p); err == nil && code == exact {
			return true
		}
	}
	return false
}

// ValidateAuth checks the auth section without touching the cluster, so the
// reconciler can mark obviously broken specs as Invalid.
func ValidateAuth(auth *cronopsv1alpha1.AuthSpec) error {
	if auth == nil || auth.Type == cronopsv1alpha1.AuthTypeNone || auth.Type == "" {
		return nil
	}
	switch auth.Type {
	case cronopsv1alpha1.AuthTypeBasic, cronopsv1alpha1.AuthTypeBearer, cronopsv1alpha1.AuthTypeAPIKey:
		if auth.SecretRef == nil || auth.SecretRef.Name == "" {
			return fmt.Errorf("auth type %q requires secretRef.name", auth.Type)
		}
		return nil
	default:
		return fmt.Errorf("unsupported auth type %q", auth.Type)
	}
}
