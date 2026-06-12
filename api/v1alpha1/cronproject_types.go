package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CronProjectSpec describes a project grouping HttpCronJobs.
// Jobs join a project via their spec.project field; v0.2 uses projects for
// grouping and filtering only, per-project restrictions arrive in v0.3.
type CronProjectSpec struct {
	// +optional
	Description string `json:"description,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=cproj
// +kubebuilder:printcolumn:name="Description",type=string,JSONPath=`.spec.description`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CronProject groups HttpCronJobs, similar to ArgoCD AppProjects.
type CronProject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec CronProjectSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// CronProjectList contains a list of CronProject.
type CronProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CronProject `json:"items"`
}
