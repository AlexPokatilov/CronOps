// Package apiserver is the REST facade of cronops-server: a thin, stateless
// layer that translates HTTP calls from the web UI into Kubernetes API
// operations on HttpCronJob resources.
package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AlexPokatilov/CronOps/internal/auth"
)

type ctxKey string

const userKey ctxKey = "user"

// Server holds dependencies of the REST API.
type Server struct {
	Client    client.Client
	Auth      *auth.Service
	StaticDir string
	// DevCORSOrigin, when set (vite dev server origin), enables permissive
	// CORS for local development.
	DevCORSOrigin string
}

// Handler builds the chi router with all routes mounted.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	if s.DevCORSOrigin != "" {
		r.Use(s.corsMiddleware)
	}

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	r.Route("/api/v1", func(api chi.Router) {
		api.Post("/auth/login", s.handleLogin)
		api.Post("/auth/logout", s.handleLogout)

		api.Group(func(priv chi.Router) {
			priv.Use(s.authMiddleware)
			priv.Get("/me", s.handleMe)
			priv.Get("/stats", s.handleStats)
			priv.Get("/cronjobs", s.handleList)
			priv.Post("/cronjobs", s.handleCreate)
			priv.Get("/cronjobs/{namespace}/{name}", s.handleGet)
			priv.Put("/cronjobs/{namespace}/{name}", s.handleUpdate)
			priv.Patch("/cronjobs/{namespace}/{name}/suspend", s.handleSuspend)
			priv.Delete("/cronjobs/{namespace}/{name}", s.handleDelete)
		})
	})

	if s.StaticDir != "" {
		r.NotFound(s.handleStatic)
	}
	return r
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.DevCORSOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.CookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		user, err := s.Auth.Verify(r.Context(), cookie.Value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "session expired or invalid")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	})
}

// handleStatic serves the built SPA with a fallback to index.html so
// client-side routes survive a refresh.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	path := filepath.Join(s.StaticDir, filepath.Clean("/"+r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.StaticDir, "index.html"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
