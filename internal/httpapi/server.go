// Package httpapi wires the raffle up to HTTP: a small JSON API plus the
// static page that drives it.
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kevinnguyen/diaper-raffle/internal/store"
)

// Server holds everything the handlers need.
type Server struct {
	store  *store.Store
	log    *slog.Logger
	assets http.Handler

	// index is the single page every non-API route serves, so /e/some-slug
	// works on a hard refresh.
	index []byte

	// Simulated odds are deterministic per roster version, so they can be
	// cached and will not jitter when the panel is reopened.
	oddsMu    sync.Mutex
	oddsCache map[string][]OddsRow
}

// New builds the server. assets serves the static files; index is the HTML
// that backs every page route.
func New(st *store.Store, log *slog.Logger, assets http.Handler, index []byte) *Server {
	return &Server{
		store:     st,
		log:       log,
		assets:    assets,
		index:     index,
		oddsCache: map[string][]OddsRow{},
	}
}

// Handler returns the fully routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)

	mux.HandleFunc("GET /api/events", s.handleListEvents)
	mux.HandleFunc("POST /api/events", s.handleCreateEvent)
	mux.HandleFunc("GET /api/events/{slug}", s.handleGetEvent)
	mux.HandleFunc("PATCH /api/events/{slug}", s.handleUpdateEvent)
	mux.HandleFunc("DELETE /api/events/{slug}", s.handleDeleteEvent)

	mux.HandleFunc("PUT /api/events/{slug}/roster", s.handlePutRoster)
	mux.HandleFunc("GET /api/events/{slug}/odds", s.handleGetOdds)

	mux.HandleFunc("POST /api/events/{slug}/guests", s.handleAddDiapers)
	mux.HandleFunc("PATCH /api/events/{slug}/guests/{id}", s.handleUpdateGuest)
	mux.HandleFunc("DELETE /api/events/{slug}/guests/{id}", s.handleDeleteGuest)

	mux.HandleFunc("GET /api/events/{slug}/draws", s.handleListDraws)
	mux.HandleFunc("POST /api/events/{slug}/draws", s.handleRunDraw)

	mux.HandleFunc("GET /api/draws/{id}", s.handleGetDraw)
	mux.HandleFunc("POST /api/draws/{id}/winners/{prize}/reveal", s.handleReveal)

	mux.HandleFunc("/", s.handlePage)

	return s.recoverPanic(s.logRequests(mux))
}

// handlePage serves static assets, and falls back to the single page for
// anything that is not a file — so /e/{slug} survives a refresh.
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.writeJSON(w, r, http.StatusNotFound,
			errorBody{errorDetail{Code: "not_found", Message: "No such endpoint."}})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Anything with a file extension is an asset request; everything else is a
	// page route and gets the app shell.
	if r.URL.Path != "/" && strings.Contains(lastSegment(r.URL.Path), ".") {
		s.assets.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(s.index)
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB().PingContext(r.Context()); err != nil {
		s.log.ErrorContext(r.Context(), "health check failed", "error", err)
		s.writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// logRequests records one line per request with its status and duration.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		}
		s.log.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(started).Round(time.Microsecond).String(),
		)
	})
}

// recoverPanic keeps one bad request from taking the party down.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				s.log.ErrorContext(r.Context(), "panic serving request",
					"panic", p, "method", r.Method, "path", r.URL.Path)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":{"code":"internal","message":"Something went wrong. Try again."}}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.written {
		return
	}
	r.status = status
	r.written = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.written = true
	return r.ResponseWriter.Write(b)
}
