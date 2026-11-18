// Package api expone la biblioteca como una API REST con JSON.
package api

import (
	"log/slog"
	"net/http"

	"github.com/dylannkeys/biblioteca-api/internal/store"
)

// Server agrupa las dependencias de los handlers.
type Server struct {
	store *store.Store
	log   *slog.Logger
}

// New devuelve el handler HTTP con todas las rutas y middlewares.
func New(st *store.Store, logger *slog.Logger) http.Handler {
	s := &Server{store: st, log: logger}

	// Desde Go 1.22 el enrutador estándar entiende métodos y parámetros ({id}): no hace falta un framework.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /stats", s.stats)

	mux.HandleFunc("GET /books", s.listBooks)
	mux.HandleFunc("POST /books", s.createBook)
	mux.HandleFunc("GET /books/{id}", s.getBook)
	mux.HandleFunc("PUT /books/{id}", s.updateBook)
	mux.HandleFunc("DELETE /books/{id}", s.deleteBook)

	mux.HandleFunc("GET /members", s.listMembers)
	mux.HandleFunc("POST /members", s.createMember)
	mux.HandleFunc("GET /members/{id}", s.getMember)
	mux.HandleFunc("GET /members/{id}/loans", s.memberLoans)

	mux.HandleFunc("GET /loans", s.listLoans)
	mux.HandleFunc("POST /loans", s.createLoan)
	mux.HandleFunc("GET /loans/{id}", s.getLoan)
	mux.HandleFunc("POST /loans/{id}/return", s.returnLoan)

	return s.recoverPanics(s.logRequests(jsonRoutingErrors(mux)))
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":      "Biblioteca API",
		"version":   "1.0.0",
		"endpoints": []string{"/health", "/stats", "/books", "/members", "/loans"},
		"docs":      "https://github.com/dylannkeys/biblioteca-api#endpoints",
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "La base de datos no responde.", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Stats(r.Context())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
