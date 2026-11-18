package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dylannkeys/biblioteca-api/internal/store"
)

type errorBody struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// page es el formato de las respuestas con listas paginadas.
type page[T any] struct {
	Data     []T `json:"data"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	writeJSON(w, status, errorBody{Error: apiError{Code: code, Message: message, Fields: fields}})
}

// storeError traduce los errores de la capa de datos a respuestas HTTP.
func (s *Server) storeError(w http.ResponseWriter, r *http.Request, err error) {
	var e *store.Error
	if errors.As(err, &e) {
		status := map[store.Kind]int{
			store.KindNotFound: http.StatusNotFound,
			store.KindConflict: http.StatusConflict,
			store.KindRule:     http.StatusUnprocessableEntity,
		}[e.Kind]
		writeError(w, status, e.Code, e.Message, nil)
		return
	}
	s.log.Error("error interno", "error", err, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, "internal", "Error interno del servidor.", nil)
}

// decodeJSON lee el cuerpo de la petición con límites y mensajes de error comprensibles.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	var maxErr *http.MaxBytesError
	switch {
	case err == nil:
	case errors.Is(err, io.EOF):
		return errors.New("El cuerpo de la petición está vacío.")
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("El JSON está mal formado.")
	case errors.As(err, &typeErr):
		return fmt.Errorf("El campo %q tiene un tipo incorrecto.", typeErr.Field)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return fmt.Errorf("Campo desconocido: %s.", strings.TrimPrefix(err.Error(), "json: unknown field "))
	case errors.As(err, &maxErr):
		return errors.New("El cuerpo de la petición es demasiado grande.")
	default:
		return errors.New("No se pudo leer el JSON.")
	}

	if dec.More() {
		return errors.New("El cuerpo debe contener un solo objeto JSON.")
	}
	return nil
}

// readJSON decodifica el cuerpo y responde 400 si falla. Devuelve false si ya respondió.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := decodeJSON(w, r, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return false
	}
	return true
}

// pathID lee el parámetro {id} de la ruta. Devuelve false si ya respondió con un error.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_id", "El id debe ser un número entero positivo.", nil)
		return 0, false
	}
	return id, true
}

type pagination struct {
	number, size int
}

func (p pagination) offset() int { return (p.number - 1) * p.size }

// parseQuery lee la paginación (page, page_size) y valida los filtros numéricos indicados.
func parseQuery(r *http.Request, errs fieldErrors) pagination {
	p := pagination{number: 1, size: 20}
	q := r.URL.Query()
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		errs.check(err == nil && n >= 1, "page", "Debe ser un número mayor o igual que 1.")
		p.number = max(n, 1)
	}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		errs.check(err == nil && n >= 1 && n <= 100, "page_size", "Debe ser un número entre 1 y 100.")
		p.size = min(max(n, 1), 100)
	}
	return p
}

func queryID(r *http.Request, name string, errs fieldErrors) int64 {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0
	}
	id, err := strconv.ParseInt(v, 10, 64)
	errs.check(err == nil && id > 0, name, "Debe ser un número entero positivo.")
	return id
}

func respondInvalid(w http.ResponseWriter, errs fieldErrors) bool {
	if len(errs) == 0 {
		return false
	}
	writeError(w, http.StatusBadRequest, "validation_failed", "Hay datos no válidos.", errs)
	return true
}
