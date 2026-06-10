package apiserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
)

const maxBodyBytes = 1 << 20 // 1 MiB

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.Auth.Authenticate(r.Context(), req.Username, req.Password); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	cookie, err := s.Auth.IssueCookie(r.Context(), req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, s.Auth.ClearCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userKey).(string)
	writeJSON(w, http.StatusOK, map[string]string{"username": user})
}

// jobView is the wire format for a single HttpCronJob.
type jobView struct {
	Name      string                            `json:"name"`
	Namespace string                            `json:"namespace"`
	Spec      cronopsv1alpha1.HttpCronJobSpec   `json:"spec"`
	Status    cronopsv1alpha1.HttpCronJobStatus `json:"status"`
	CreatedAt metav1.Time                       `json:"createdAt"`
}

func toView(j *cronopsv1alpha1.HttpCronJob) jobView {
	return jobView{
		Name:      j.Name,
		Namespace: j.Namespace,
		Spec:      j.Spec,
		Status:    j.Status,
		CreatedAt: j.CreationTimestamp,
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	var list cronopsv1alpha1.HttpCronJobList
	var opts []client.ListOption
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	if err := s.Client.List(r.Context(), &list, opts...); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing cronjobs: %v", err))
		return
	}
	views := make([]jobView, 0, len(list.Items))
	for i := range list.Items {
		views = append(views, toView(&list.Items[i]))
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Namespace != views[j].Namespace {
			return views[i].Namespace < views[j].Namespace
		}
		return views[i].Name < views[j].Name
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

// createRequest is the JSON form of POST /cronjobs (the UI form mode).
type createRequest struct {
	Name      string                          `json:"name"`
	Namespace string                          `json:"namespace"`
	Spec      cronopsv1alpha1.HttpCronJobSpec `json:"spec"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "reading request body")
		return
	}

	var job cronopsv1alpha1.HttpCronJob
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "yaml") {
		if err := yaml.UnmarshalStrict(body, &job); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid YAML manifest: %v", err))
			return
		}
		gvk := cronopsv1alpha1.GroupVersion.WithKind("HttpCronJob")
		if job.APIVersion != gvk.GroupVersion().String() || job.Kind != gvk.Kind {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("manifest must be apiVersion: %s, kind: %s", gvk.GroupVersion(), gvk.Kind))
			return
		}
	} else {
		var req createRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		job.Name = req.Name
		job.Namespace = req.Namespace
		job.Spec = req.Spec
	}
	if job.Namespace == "" {
		job.Namespace = "default"
	}
	if job.Name == "" {
		writeError(w, http.StatusBadRequest, "metadata.name is required")
		return
	}
	// TypeMeta must not leak into the typed client call path issues; clear it.
	job.APIVersion = ""
	job.Kind = ""
	job.Status = cronopsv1alpha1.HttpCronJobStatus{}

	var createOpts []client.CreateOption
	dryRun := r.URL.Query().Get("dryRun") == "true"
	if dryRun {
		createOpts = append(createOpts, client.DryRunAll)
	}
	if err := s.Client.Create(r.Context(), &job, createOpts...); err != nil {
		writeKubeError(w, err)
		return
	}
	status := http.StatusCreated
	if dryRun {
		status = http.StatusOK
	}
	writeJSON(w, status, toView(&job))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	job, ok := s.fetch(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toView(job))
}

type updateRequest struct {
	Spec cronopsv1alpha1.HttpCronJobSpec `json:"spec"`
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	key := keyFromURL(r)
	var updated cronopsv1alpha1.HttpCronJob
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var job cronopsv1alpha1.HttpCronJob
		if err := s.Client.Get(r.Context(), key, &job); err != nil {
			return err
		}
		job.Spec = req.Spec
		if err := s.Client.Update(r.Context(), &job); err != nil {
			return err
		}
		updated = job
		return nil
	})
	if err != nil {
		writeKubeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toView(&updated))
}

type suspendRequest struct {
	Suspend bool `json:"suspend"`
}

func (s *Server) handleSuspend(w http.ResponseWriter, r *http.Request) {
	var req suspendRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	key := keyFromURL(r)
	var updated cronopsv1alpha1.HttpCronJob
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var job cronopsv1alpha1.HttpCronJob
		if err := s.Client.Get(r.Context(), key, &job); err != nil {
			return err
		}
		job.Spec.Suspend = &req.Suspend
		if err := s.Client.Update(r.Context(), &job); err != nil {
			return err
		}
		updated = job
		return nil
	})
	if err != nil {
		writeKubeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toView(&updated))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	job, ok := s.fetch(w, r)
	if !ok {
		return
	}
	if err := s.Client.Delete(r.Context(), job); err != nil {
		writeKubeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// statsResponse aggregates dashboard numbers from the status of all jobs.
type statsResponse struct {
	Total     int         `json:"total"`
	Active    int         `json:"active"`
	Suspended int         `json:"suspended"`
	Invalid   int         `json:"invalid"`
	LastRuns  runTotals   `json:"lastRuns"`
	Recent    []recentRun `json:"recentRuns"`
}

type runTotals struct {
	Success int `json:"success"`
	Failed  int `json:"failed"`
}

type recentRun struct {
	Job                       string `json:"job"`
	Namespace                 string `json:"namespace"`
	cronopsv1alpha1.RunResult `json:",inline"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	var list cronopsv1alpha1.HttpCronJobList
	if err := s.Client.List(r.Context(), &list); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing cronjobs: %v", err))
		return
	}
	resp := statsResponse{Recent: []recentRun{}}
	resp.Total = len(list.Items)
	for i := range list.Items {
		job := &list.Items[i]
		switch job.Status.Phase {
		case cronopsv1alpha1.PhaseSuspended:
			resp.Suspended++
		case cronopsv1alpha1.PhaseInvalid:
			resp.Invalid++
		default:
			resp.Active++
		}
		if lr := job.Status.LastRun; lr != nil {
			if lr.Result == cronopsv1alpha1.ResultSuccess {
				resp.LastRuns.Success++
			} else {
				resp.LastRuns.Failed++
			}
		}
		for _, run := range job.Status.History {
			resp.Recent = append(resp.Recent, recentRun{
				Job:       job.Name,
				Namespace: job.Namespace,
				RunResult: run,
			})
		}
	}
	sort.Slice(resp.Recent, func(i, j int) bool {
		return resp.Recent[i].StartedAt.Time.After(resp.Recent[j].StartedAt.Time)
	})
	if len(resp.Recent) > 20 {
		resp.Recent = resp.Recent[:20]
	}
	writeJSON(w, http.StatusOK, resp)
}

func keyFromURL(r *http.Request) types.NamespacedName {
	return types.NamespacedName{
		Namespace: chi.URLParam(r, "namespace"),
		Name:      chi.URLParam(r, "name"),
	}
}

func (s *Server) fetch(w http.ResponseWriter, r *http.Request) (*cronopsv1alpha1.HttpCronJob, bool) {
	var job cronopsv1alpha1.HttpCronJob
	if err := s.Client.Get(r.Context(), keyFromURL(r), &job); err != nil {
		writeKubeError(w, err)
		return nil, false
	}
	return &job, true
}

func writeKubeError(w http.ResponseWriter, err error) {
	switch {
	case apierrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, err.Error())
	case apierrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, err.Error())
	case apierrors.IsInvalid(err) || apierrors.IsBadRequest(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case apierrors.IsForbidden(err):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
