// Package executor performs the actual HTTP call for a HttpCronJob run.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/jsonpath"
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
	// Attempts is how many HTTP attempts were made (retries included).
	Attempts int32
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

// maxBackoff caps the exponential retry delay so a high backoffSeconds with
// many attempts cannot park a run in Running for the better part of a day.
const maxBackoff = time.Hour

// Run executes the HTTP call for the job, retrying failed attempts with
// exponential backoff per spec.retry, and reports the final outcome.
func (e *Executor) Run(ctx context.Context, job *cronopsv1alpha1.HttpCronJob) Result {
	maxAttempts := job.MaxAttemptsOrDefault()
	backoff := time.Duration(job.BackoffSecondsOrDefault()) * time.Second

	var res Result
	for attempt := int32(1); ; attempt++ {
		res = e.attempt(ctx, job)
		res.Attempts = attempt
		if res.Success || attempt >= maxAttempts {
			return res
		}
		select {
		case <-ctx.Done():
			res.Message += "; retries aborted: " + ctx.Err().Error()
			return res
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// attempt performs a single HTTP call with the per-attempt timeout applied.
func (e *Executor) attempt(ctx context.Context, job *cronopsv1alpha1.HttpCronJob) Result {
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

	// The body is needed in full (bounded) for success criteria; only a
	// short snippet of it is ever stored in run history. Reading one byte
	// past the limit detects truncation instead of silently evaluating
	// criteria against a cut-off body.
	criteria := job.Spec.SuccessCriteria
	readLimit := int64(maxCaptureBytes)
	if criteria != nil {
		readLimit = maxDrainBytes
	}
	captured, _ := io.ReadAll(io.LimitReader(resp.Body, readLimit+1))
	truncated := int64(len(captured)) > readLimit
	if truncated {
		captured = captured[:readLimit]
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))

	var snippet string
	if job.CaptureResponseBodyOrDefault() {
		snippet = string(captured[:min(len(captured), maxCaptureBytes)])
	}

	res := Result{
		StatusCode: resp.StatusCode,
		Success:    CodeMatches(resp.StatusCode, job.Spec.SuccessHTTPCodes),
		Body:       snippet,
	}
	if !res.Success {
		res.Message = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		return res
	}
	if criteria != nil && truncated {
		// An honest error beats a confusing one from matching half a body.
		res.Success = false
		res.Message = fmt.Sprintf("response body exceeds %d bytes; success criteria cannot be evaluated reliably", maxDrainBytes)
		return res
	}
	if err := CheckBody(captured, criteria); err != nil {
		res.Success = false
		res.Message = err.Error()
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

// CheckBody evaluates the success criteria against the response body and
// returns a descriptive error when any criterion fails.
func CheckBody(body []byte, criteria *cronopsv1alpha1.SuccessCriteriaSpec) error {
	if criteria == nil {
		return nil
	}
	if criteria.BodyRegex != "" {
		re, err := regexp.Compile(criteria.BodyRegex)
		if err != nil {
			return fmt.Errorf("invalid bodyRegex: %w", err)
		}
		if !re.Match(body) {
			return fmt.Errorf("response body does not match bodyRegex %q", criteria.BodyRegex)
		}
	}
	if criteria.JSONPath != "" {
		got, err := evalJSONPath(body, criteria.JSONPath)
		if err != nil {
			return err
		}
		if criteria.Value != "" && got != criteria.Value {
			return fmt.Errorf("jsonPath %s = %q, want %q", criteria.JSONPath, got, criteria.Value)
		}
	}
	return nil
}

// evalJSONPath runs a kubectl-style JSONPath expression over a JSON body and
// returns the result rendered as a string.
func evalJSONPath(body []byte, expr string) (string, error) {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("response body is not valid JSON: %v", err)
	}
	jp := jsonpath.New("successCriteria")
	if err := jp.Parse(normalizeJSONPath(expr)); err != nil {
		return "", fmt.Errorf("invalid jsonPath %q: %v", expr, err)
	}
	var out strings.Builder
	if err := jp.Execute(&out, doc); err != nil {
		return "", fmt.Errorf("jsonPath %s did not match response body: %v", expr, err)
	}
	// An empty rendering is fine: the path resolved (existence is proven by
	// Execute succeeding) and the value may legitimately be "".
	return out.String(), nil
}

// normalizeJSONPath lets users write ".status" or "$.status" instead of the
// full "{.status}" template syntax.
func normalizeJSONPath(expr string) string {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "{") {
		return expr
	}
	expr = strings.TrimPrefix(expr, "$")
	if !strings.HasPrefix(expr, ".") && !strings.HasPrefix(expr, "[") {
		expr = "." + expr
	}
	return "{" + expr + "}"
}

// ValidateCriteria checks spec.successCriteria without running a request, so
// the reconciler can mark obviously broken specs as Invalid.
func ValidateCriteria(criteria *cronopsv1alpha1.SuccessCriteriaSpec) error {
	if criteria == nil {
		return nil
	}
	if criteria.BodyRegex == "" && criteria.JSONPath == "" {
		return fmt.Errorf("successCriteria requires bodyRegex or jsonPath")
	}
	if criteria.BodyRegex != "" {
		if _, err := regexp.Compile(criteria.BodyRegex); err != nil {
			return fmt.Errorf("invalid successCriteria.bodyRegex: %v", err)
		}
	}
	if criteria.JSONPath != "" {
		if err := jsonpath.New("successCriteria").Parse(normalizeJSONPath(criteria.JSONPath)); err != nil {
			return fmt.Errorf("invalid successCriteria.jsonPath: %v", err)
		}
	}
	if criteria.Value != "" && criteria.JSONPath == "" {
		return fmt.Errorf("successCriteria.value requires jsonPath")
	}
	return nil
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
