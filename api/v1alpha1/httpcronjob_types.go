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
	ConcurrencyAllow  = "Allow"
	ConcurrencyForbid = "Forbid"
)

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

	// ConcurrencyPolicy controls what happens when a run fires while the
	// previous one is still in progress.
	// +kubebuilder:validation:Enum=Allow;Forbid
	// +kubebuilder:default=Forbid
	// +optional
	ConcurrencyPolicy string `json:"concurrencyPolicy,omitempty"`

	// HistoryLimit caps the number of past runs kept in status.history.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	// +optional
	HistoryLimit *int32 `json:"historyLimit,omitempty"`
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

func init() {
	SchemeBuilder.Register(&HttpCronJob{}, &HttpCronJobList{})
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
