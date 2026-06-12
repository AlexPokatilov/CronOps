package apiserver

import (
	"fmt"
	"net/http"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
)

// runView is the wire format for a single HttpCronJobRun: the status fields
// are embedded as-is so new fields flow through without a hand-written
// mapping, plus identity metadata from the object itself.
type runView struct {
	Name      string      `json:"name"`
	Namespace string      `json:"namespace"`
	JobName   string      `json:"jobName"`
	Trigger   string      `json:"trigger"`
	CreatedAt metav1.Time `json:"createdAt"`

	cronopsv1alpha1.HttpCronJobRunStatus
}

func toRunView(r *cronopsv1alpha1.HttpCronJobRun) runView {
	v := runView{
		Name:                 r.Name,
		Namespace:            r.Namespace,
		JobName:              r.Spec.JobName,
		Trigger:              r.Spec.Trigger,
		CreatedAt:            r.CreationTimestamp,
		HttpCronJobRunStatus: r.Status,
	}
	if v.Phase == "" {
		v.Phase = "Pending"
	}
	return v
}

// handleRunNow creates a Manual HttpCronJobRun; the controller picks it up
// and executes the HTTP call (run-now works even on suspended jobs).
func (s *Server) handleRunNow(w http.ResponseWriter, r *http.Request) {
	job, ok := s.fetch(w, r)
	if !ok {
		return
	}
	run := cronopsv1alpha1.NewRunForJob(job, cronopsv1alpha1.TriggerManual)
	if err := s.Client.Create(r.Context(), run); err != nil {
		writeKubeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toRunView(run))
}

// handleListRuns returns the job's HttpCronJobRun objects, newest first.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	key := keyFromURL(r)
	var runs cronopsv1alpha1.HttpCronJobRunList
	if err := s.Client.List(r.Context(), &runs,
		client.InNamespace(key.Namespace),
		client.MatchingLabels{cronopsv1alpha1.LabelCronJob: key.Name}); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing runs: %v", err))
		return
	}
	views := make([]runView, 0, len(runs.Items))
	for i := range runs.Items {
		views = append(views, toRunView(&runs.Items[i]))
	}
	sort.Slice(views, func(i, j int) bool {
		return views[i].CreatedAt.After(views[j].CreatedAt.Time)
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}
