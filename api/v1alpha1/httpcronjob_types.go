package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Phase values reported in HttpCronJob status.
const (
	PhaseActive    = "Active"
	PhaseSuspended = "Suspended"
	PhaseInvalid   = "Invalid"
)

// Run result values.
const (
	ResultSuccess = "Success"
	ResultFailed  = "Failed"
)

// Auth types supported by HttpCronJob.
const (
	AuthTypeNone   = "none"
	AuthTypeBasic  = "basic"
	AuthTypeBearer = "bearer"
	AuthTypeAPIKey = "apiKey"
)

// Concurrency policies.
const (
	ConcurrencyAllow   = "Allow"
	ConcurrencyForbid  = "Forbid"
	ConcurrencyReplace = "Replace"
)

// Run triggers recorded on HttpCronJobRun resources.
const (
	TriggerSchedule = "Schedule"
	TriggerManual   = "Manual"
)

// DefaultProject is the implicit project for jobs with no spec.project.
const DefaultProject = "default"

// LabelCronJob on a HttpCronJobRun points at the owning HttpCronJob name.
const LabelCronJob = "cronops.io/cronjob"

// ConditionScheduled reports whether the job is registered in the scheduler.
const ConditionScheduled = "Scheduled"

// HeaderSpec is a single HTTP header sent with each request.
type HeaderSpec struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SecretKeyRef points to a key inside a Secret in the same namespace as the HttpCronJob.
type SecretKeyRef struct {
	Name string `json:"name"`
	// Key inside the Secret. Defaults: "token" for bearer/apiKey.
	// For basic auth the Secret must contain "username" and "password" keys and Key is ignored.
	// +optional
	Key string `json:"key,omitempty"`
}

// AuthSpec describes how the request to the endpoint is authenticated.
// Credentials are never stored in the spec — only referenced from Secrets.
type AuthSpec struct {
	// +kubebuilder:validation:Enum=none;basic;bearer;apiKey
	Type string `json:"type"`
	// +optional
	SecretRef *SecretKeyRef `json:"secretRef,omitempty"`
	// HeaderName carries the api key for type=apiKey. Defaults to "X-API-Key".
	// +optional
	HeaderName string `json:"headerName,omitempty"`
}

// RetrySpec controls retries of failed runs.
type RetrySpec struct {
	// MaxAttempts is the total number of attempts per run, including the
	// first one. 1 means no retries.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	// +optional
	MaxAttempts *int32 `json:"maxAttempts,omitempty"`

	// BackoffSeconds is the delay before the first retry; each subsequent
	// retry doubles it (exponential backoff).
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	// +optional
	BackoffSeconds *int32 `json:"backoffSeconds,omitempty"`
}

// SuccessCriteriaSpec validates the response body in addition to the HTTP
// status code. All set criteria must pass for the run to count as Success.
type SuccessCriteriaSpec struct {
	// BodyRegex is an RE2 regular expression the response body must match.
	// +optional
	BodyRegex string `json:"bodyRegex,omitempty"`

	// JSONPath is a JSONPath expression (e.g. "{.status}" or ".status")
	// evaluated against the JSON response body. Without Value the expression
	// only needs to resolve to a non-empty result.
	// +optional
	JSONPath string `json:"jsonPath,omitempty"`

	// Value the JSONPath result must equal (string comparison).
	// +optional
	Value string `json:"value,omitempty"`
}

// HttpCronJobSpec defines the desired state of HttpCronJob.
type HttpCronJobSpec struct {
	// Schedule in standard 5-field cron syntax, e.g. "0 3 * * *".
	// +kubebuilder:validation:MinLength=1
	Schedule string `json:"schedule"`

	// Endpoint is the URL called on each run.
	// +kubebuilder:validation:Pattern=`^https?://.+`
	Endpoint string `json:"endpoint"`

	// +kubebuilder:validation:Enum=GET;POST;PUT;PATCH;DELETE;HEAD
	Method string `json:"method"`

	// Timezone for the schedule, IANA name (e.g. "Europe/Kyiv"). Defaults to UTC.
	// +optional
	Timezone string `json:"timezone,omitempty"`

	// Suspend pauses scheduling without deleting the resource.
	// +optional
	Suspend *bool `json:"suspend,omitempty"`

	// +optional
	Headers []HeaderSpec `json:"headers,omitempty"`

	// +optional
	Auth *AuthSpec `json:"auth,omitempty"`

	// Body sent with the request (for methods that accept one).
	// +optional
	Body string `json:"body,omitempty"`

	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	// +optional
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`

	// SuccessHttpCodes lists response codes treated as success.
	// Entries are exact codes ("200") or classes ("2xx"). Defaults to ["2xx"].
	// +optional
	SuccessHTTPCodes []string `json:"successHttpCodes,omitempty"`

	// SuccessCriteria additionally validates the response body
	// (regex and/or JSONPath) before a run counts as Success.
	// +optional
	SuccessCriteria *SuccessCriteriaSpec `json:"successCriteria,omitempty"`

	// Retry re-runs failed attempts with exponential backoff.
	// +optional
	Retry *RetrySpec `json:"retry,omitempty"`

	// Project assigns the job to a CronProject for grouping and filtering.
	// Empty means the "default" project.
	// +optional
	Project string `json:"project,omitempty"`

	// ConcurrencyPolicy controls what happens when a run fires while the
	// previous one is still in progress: Allow runs them in parallel,
	// Forbid skips the new run, Replace cancels the old run first.
	// +kubebuilder:validation:Enum=Allow;Forbid;Replace
	// +kubebuilder:default=Forbid
	// +optional
	ConcurrencyPolicy string `json:"concurrencyPolicy,omitempty"`

	// HistoryLimit caps the number of past runs kept in status.history.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	// +optional
	HistoryLimit *int32 `json:"historyLimit,omitempty"`

	// RunHistoryLimit caps the number of finished HttpCronJobRun objects
	// kept per job; the oldest are deleted first.
	// +kubebuilder:default=20
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=200
	// +optional
	RunHistoryLimit *int32 `json:"runHistoryLimit,omitempty"`

	// RunTTLSecondsAfterFinished, when set, deletes finished
	// HttpCronJobRun objects this many seconds after they complete.
	// +kubebuilder:validation:Minimum=60
	// +optional
	RunTTLSecondsAfterFinished *int32 `json:"runTTLSecondsAfterFinished,omitempty"`

	// CaptureResponseBody stores a truncated snippet (first 2 KiB) of each
	// response body in run history so it can be inspected in the UI.
	// Disable for endpoints that return sensitive data.
	// +kubebuilder:default=true
	// +optional
	CaptureResponseBody *bool `json:"captureResponseBody,omitempty"`
}

// RunResult records the outcome of a single run.
type RunResult struct {
	StartedAt metav1.Time `json:"startedAt"`
	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
	// +kubebuilder:validation:Enum=Success;Failed
	Result string `json:"result"`
	// +optional
	HTTPStatusCode int32 `json:"httpStatusCode,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// DurationMs is the wall-clock run time in milliseconds.
	// +optional
	DurationMs int64 `json:"durationMs,omitempty"`
	// ResponseBody is a truncated snippet of the response body
	// (see spec.captureResponseBody).
	// +optional
	ResponseBody string `json:"responseBody,omitempty"`
	// Attempts is how many HTTP attempts the run took (retries included).
	// +optional
	Attempts int32 `json:"attempts,omitempty"`
	// Trigger records what started the run: Schedule or Manual.
	// +optional
	Trigger string `json:"trigger,omitempty"`
}

// HttpCronJobStatus defines the observed state of HttpCronJob.
type HttpCronJobStatus struct {
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	LastScheduleTime *metav1.Time `json:"lastScheduleTime,omitempty"`
	// +optional
	NextScheduleTime *metav1.Time `json:"nextScheduleTime,omitempty"`
	// +optional
	LastRun *RunResult `json:"lastRun,omitempty"`
	// History holds the most recent runs, newest first, capped by spec.historyLimit.
	// +optional
	History []RunResult `json:"history,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hcj
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Method",type=string,JSONPath=`.spec.method`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Last Result",type=string,JSONPath=`.status.lastRun.result`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HttpCronJob is a declaratively scheduled HTTP call.
type HttpCronJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HttpCronJobSpec   `json:"spec,omitempty"`
	Status HttpCronJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HttpCronJobList contains a list of HttpCronJob.
type HttpCronJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HttpCronJob `json:"items"`
}

// IsSuspended reports whether the job is paused via spec.suspend.
func (j *HttpCronJob) IsSuspended() bool {
	return j.Spec.Suspend != nil && *j.Spec.Suspend
}

// TimeoutOrDefault returns spec.timeoutSeconds with the API default applied.
func (j *HttpCronJob) TimeoutOrDefault() int32 {
	if j.Spec.TimeoutSeconds != nil {
		return *j.Spec.TimeoutSeconds
	}
	return 30
}

// HistoryLimitOrDefault returns spec.historyLimit with the API default applied.
func (j *HttpCronJob) HistoryLimitOrDefault() int32 {
	if j.Spec.HistoryLimit != nil {
		return *j.Spec.HistoryLimit
	}
	return 10
}

// CaptureResponseBodyOrDefault returns spec.captureResponseBody with the API
// default applied.
func (j *HttpCronJob) CaptureResponseBodyOrDefault() bool {
	if j.Spec.CaptureResponseBody != nil {
		return *j.Spec.CaptureResponseBody
	}
	return true
}

// RunHistoryLimitOrDefault returns spec.runHistoryLimit with the API default
// applied.
func (j *HttpCronJob) RunHistoryLimitOrDefault() int32 {
	if j.Spec.RunHistoryLimit != nil {
		return *j.Spec.RunHistoryLimit
	}
	return 20
}

// MaxAttemptsOrDefault returns spec.retry.maxAttempts with the API default
// applied.
func (j *HttpCronJob) MaxAttemptsOrDefault() int32 {
	if j.Spec.Retry != nil && j.Spec.Retry.MaxAttempts != nil {
		return *j.Spec.Retry.MaxAttempts
	}
	return 1
}

// BackoffSecondsOrDefault returns spec.retry.backoffSeconds with the API
// default applied.
func (j *HttpCronJob) BackoffSecondsOrDefault() int32 {
	if j.Spec.Retry != nil && j.Spec.Retry.BackoffSeconds != nil {
		return *j.Spec.Retry.BackoffSeconds
	}
	return 10
}

// ProjectOrDefault returns spec.project, falling back to the default project.
func (j *HttpCronJob) ProjectOrDefault() string {
	if j.Spec.Project != "" {
		return j.Spec.Project
	}
	return DefaultProject
}
