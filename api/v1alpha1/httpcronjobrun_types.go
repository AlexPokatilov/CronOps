package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Phase values reported in HttpCronJobRun status. An empty phase means the
// run is pending and has not been picked up by the controller yet.
const (
	RunPhaseRunning   = "Running"
	RunPhaseSucceeded = "Succeeded"
	RunPhaseFailed    = "Failed"
	// RunPhaseCancelled marks a run aborted on purpose (concurrencyPolicy:
	// Replace or controller shutdown); not a failure of the endpoint.
	RunPhaseCancelled = "Cancelled"
	// RunPhaseSkipped marks a run that never executed because another run
	// was in progress (concurrencyPolicy: Forbid).
	RunPhaseSkipped = "Skipped"
)

// HttpCronJobRunSpec describes a single requested run of a HttpCronJob.
// The controller creates these for scheduled runs; the API server creates
// them with trigger=Manual for run-now requests.
type HttpCronJobRunSpec struct {
	// JobName is the HttpCronJob (same namespace) this run belongs to.
	// +kubebuilder:validation:MinLength=1
	JobName string `json:"jobName"`

	// Trigger records what requested the run.
	// +kubebuilder:validation:Enum=Schedule;Manual
	// +kubebuilder:default=Manual
	// +optional
	Trigger string `json:"trigger,omitempty"`
}

// HttpCronJobRunStatus is the observed outcome of the run.
type HttpCronJobRunStatus struct {
	// +kubebuilder:validation:Enum=Running;Succeeded;Failed;Cancelled;Skipped
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
	// +optional
	HTTPStatusCode int32 `json:"httpStatusCode,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// DurationMs is the wall-clock run time in milliseconds.
	// +optional
	DurationMs int64 `json:"durationMs,omitempty"`
	// ResponseBody is a truncated snippet of the response body
	// (see HttpCronJob spec.captureResponseBody).
	// +optional
	ResponseBody string `json:"responseBody,omitempty"`
	// Attempts is how many HTTP attempts were made (retries included).
	// +optional
	Attempts int32 `json:"attempts,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hcjr
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=`.spec.jobName`
// +kubebuilder:printcolumn:name="Trigger",type=string,JSONPath=`.spec.trigger`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Code",type=integer,JSONPath=`.status.httpStatusCode`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HttpCronJobRun is one execution of a HttpCronJob: a full, queryable run
// history that outlives the bounded status.history ring buffer.
type HttpCronJobRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HttpCronJobRunSpec   `json:"spec,omitempty"`
	Status HttpCronJobRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HttpCronJobRunList contains a list of HttpCronJobRun.
type HttpCronJobRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HttpCronJobRun `json:"items"`
}

// Finished reports whether the run reached a terminal phase.
func (r *HttpCronJobRun) Finished() bool {
	switch r.Status.Phase {
	case RunPhaseSucceeded, RunPhaseFailed, RunPhaseCancelled, RunPhaseSkipped:
		return true
	}
	return false
}

// NewRunForJob builds a HttpCronJobRun owned by the job. The controller
// creates them for scheduled runs, the API server for manual run-now.
func NewRunForJob(job *HttpCronJob, trigger string) *HttpCronJobRun {
	return &HttpCronJobRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: job.Name + "-",
			Namespace:    job.Namespace,
			Labels:       map[string]string{LabelCronJob: job.Name},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(job, GroupVersion.WithKind("HttpCronJob")),
			},
		},
		Spec: HttpCronJobRunSpec{
			JobName: job.Name,
			Trigger: trigger,
		},
	}
}
