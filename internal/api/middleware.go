package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// statusRecorder guarda el código de estado para poder registrarlo en el log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// logRequests registra cada petición con su método, ruta, estado y duración.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("petición",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

// recoverPanics evita que un fallo inesperado tumbe el servidor y responde 500.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "error", v, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal", "Error interno del servidor.", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// jsonRoutingErrors convierte las respuestas 404 y 405 en texto plano del enrutador estándar
// en errores JSON, para que toda la API responda con el mismo formato.
func jsonRoutingErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&routingErrorWriter{ResponseWriter: w}, r)
	})
}

type routingErrorWriter struct {
	http.ResponseWriter
	replaced bool
}

func (w *routingErrorWriter) WriteHeader(status int) {
	plainText := strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain")
	if plainText && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) {
		w.replaced = true
		body := errorBody{Error: apiError{Code: "not_found", Message: "Ruta no encontrada."}}
		if status == http.StatusMethodNotAllowed {
			body.Error = apiError{Code: "method_not_allowed", Message: "Método no permitido en esta ruta."}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.ResponseWriter.WriteHeader(status)
		_ = json.NewEncoder(w.ResponseWriter).Encode(body)
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *routingErrorWriter) Write(b []byte) (int, error) {
	if w.replaced {
		return len(b), nil // descarta el texto plano original
	}
	return w.ResponseWriter.Write(b)
}
