package apiserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cronopsv1alpha1 "github.com/AlexPokatilov/CronOps/api/v1alpha1"
)

// projectView is the wire format for a CronProject, enriched with the number
// of jobs currently assigned to it.
type projectView struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	JobCount    int         `json:"jobCount"`
	CreatedAt   metav1.Time `json:"createdAt"`
}

// handleListProjects returns all CronProjects plus the implicit "default"
// project, each with its job count.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	var projects cronopsv1alpha1.CronProjectList
	if err := s.Client.List(r.Context(), &projects); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing projects: %v", err))
		return
	}
	var jobs cronopsv1alpha1.HttpCronJobList
	if err := s.Client.List(r.Context(), &jobs); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing cronjobs: %v", err))
		return
	}
	counts := map[string]int{}
	for i := range jobs.Items {
		counts[jobs.Items[i].ProjectOrDefault()]++
	}

	views := []projectView{}
	seenDefault := false
	for i := range projects.Items {
		p := &projects.Items[i]
		if p.Name == cronopsv1alpha1.DefaultProject {
			seenDefault = true
		}
		views = append(views, projectView{
			Name:        p.Name,
			Description: p.Spec.Description,
			JobCount:    counts[p.Name],
			CreatedAt:   p.CreationTimestamp,
		})
	}
	if !seenDefault {
		views = append(views, projectView{
			Name:        cronopsv1alpha1.DefaultProject,
			Description: "Implicit project for jobs without spec.project",
			JobCount:    counts[cronopsv1alpha1.DefaultProject],
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

type createProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	project := &cronopsv1alpha1.CronProject{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name},
		Spec:       cronopsv1alpha1.CronProjectSpec{Description: req.Description},
	}
	if err := s.Client.Create(r.Context(), project); err != nil {
		writeKubeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectView{
		Name:        project.Name,
		Description: project.Spec.Description,
		CreatedAt:   project.CreationTimestamp,
	})
}

// handleDeleteProject removes the CronProject object. Jobs referencing it are
// left untouched (their spec.project simply points at a missing project).
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == cronopsv1alpha1.DefaultProject {
		writeError(w, http.StatusBadRequest, "the default project cannot be deleted")
		return
	}
	project := &cronopsv1alpha1.CronProject{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := s.Client.Delete(r.Context(), project); err != nil {
		writeKubeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
