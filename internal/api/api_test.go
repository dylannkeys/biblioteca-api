package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dylannkeys/biblioteca-api/internal/store"
)

func newTestAPI(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

type response struct {
	status int
	header http.Header
	body   map[string]any
}

// call hace una petición al handler. body puede ser nil, un string (JSON en bruto) o cualquier valor serializable.
func call(t *testing.T, h http.Handler, method, path string, body any) response {
	t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	res := response{status: rec.Code, header: rec.Header(), body: map[string]any{}}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &res.body); err != nil {
			t.Fatalf("%s %s: la respuesta no es JSON: %q", method, path, rec.Body.String())
		}
	}
	return res
}

func errorField(r response, key string) string {
	e, _ := r.body["error"].(map[string]any)
	v, _ := e[key].(string)
	return v
}

func id(r response) int64 {
	v, _ := r.body["id"].(float64)
	return int64(v)
}

func TestHealthAndIndex(t *testing.T) {
	h := newTestAPI(t)
	if r := call(t, h, "GET", "/health", nil); r.status != 200 || r.body["status"] != "ok" {
		t.Fatalf("/health: %d %v", r.status, r.body)
	}
	if r := call(t, h, "GET", "/", nil); r.status != 200 || r.body["name"] != "Biblioteca API" {
		t.Fatalf("/: %d %v", r.status, r.body)
	}
}

func TestRoutingErrorsAreJSON(t *testing.T) {
	h := newTestAPI(t)
	if r := call(t, h, "GET", "/no-existe", nil); r.status != 404 || errorField(r, "code") != "not_found" {
		t.Fatalf("ruta inexistente: %d %v", r.status, r.body)
	}
	if r := call(t, h, "DELETE", "/health", nil); r.status != 405 || errorField(r, "code") != "method_not_allowed" {
		t.Fatalf("método no permitido: %d %v", r.status, r.body)
	}
	if r := call(t, h, "GET", "/books/abc", nil); r.status != 400 || errorField(r, "code") != "invalid_id" {
		t.Fatalf("id no numérico: %d %v", r.status, r.body)
	}
}

func TestCreateBookValidation(t *testing.T) {
	h := newTestAPI(t)

	r := call(t, h, "POST", "/books", map[string]any{"title": " ", "year": 3000, "isbn": "978-0-306-40615-8", "copies": -1})
	if r.status != 400 || errorField(r, "code") != "validation_failed" {
		t.Fatalf("validación: %d %v", r.status, r.body)
	}
	fields, _ := r.body["error"].(map[string]any)["fields"].(map[string]any)
	for _, f := range []string{"title", "author", "year", "isbn", "copies"} {
		if _, ok := fields[f]; !ok {
			t.Errorf("falta el error del campo %q en %v", f, fields)
		}
	}

	badJSON := map[string]string{
		"vacío":            "",
		"mal formado":      `{"title": `,
		"campo extra":      `{"title": "A", "author": "B", "precio": 10}`,
		"tipo incorrecto":  `{"title": "A", "author": "B", "copies": "dos"}`,
		"dos objetos JSON": `{"title": "A", "author": "B"} {}`,
	}
	for name, body := range badJSON {
		if r := call(t, h, "POST", "/books", body); r.status != 400 || errorField(r, "code") != "invalid_json" {
			t.Errorf("%s: %d %v", name, r.status, r.body)
		}
	}
}

func TestBookCRUD(t *testing.T) {
	h := newTestAPI(t)

	created := call(t, h, "POST", "/books", map[string]any{
		"title": "Huasipungo", "author": "Jorge Icaza", "year": 1934, "isbn": "0-306-40615-2",
	})
	if created.status != 201 || created.body["copies"] != float64(1) || created.body["isbn"] != "0306406152" {
		t.Fatalf("crear: %d %v", created.status, created.body)
	}
	bookID := id(created)
	if loc := created.header.Get("Location"); loc != "/books/1" {
		t.Fatalf("Location = %q", loc)
	}

	updated := call(t, h, "PUT", "/books/1", map[string]any{"title": "Huasipungo", "author": "Jorge Icaza", "copies": 4})
	if updated.status != 200 || updated.body["available"] != float64(4) {
		t.Fatalf("actualizar: %d %v", updated.status, updated.body)
	}

	list := call(t, h, "GET", "/books?q=icaza&page_size=5", nil)
	if list.status != 200 || list.body["total"] != float64(1) || list.body["page_size"] != float64(5) {
		t.Fatalf("listar: %d %v", list.status, list.body)
	}

	if r := call(t, h, "GET", "/books?page=0&available=quizas", nil); r.status != 400 {
		t.Fatalf("paginación no válida: %d %v", r.status, r.body)
	}

	if r := call(t, h, "DELETE", "/books/1", nil); r.status != 204 {
		t.Fatalf("borrar: %d", r.status)
	}
	if r := call(t, h, "GET", "/books/1", nil); r.status != 404 {
		t.Fatalf("libro borrado: %d (id %d)", r.status, bookID)
	}
}

func TestLoanFlow(t *testing.T) {
	h := newTestAPI(t)

	book := id(call(t, h, "POST", "/books", map[string]any{"title": "Ficciones", "author": "Borges", "copies": 1}))
	ana := id(call(t, h, "POST", "/members", map[string]any{"name": "Ana", "email": " Ana@Example.com "}))
	luis := id(call(t, h, "POST", "/members", map[string]any{"name": "Luis", "email": "luis@example.com"}))

	if r := call(t, h, "POST", "/members", map[string]any{"name": "Otra Ana", "email": "ana@example.com"}); r.status != 409 {
		t.Fatalf("correo repetido: %d %v", r.status, r.body)
	}

	loan := call(t, h, "POST", "/loans", map[string]any{"book_id": book, "member_id": ana})
	if loan.status != 201 || loan.body["status"] != "active" || loan.body["member_name"] != "Ana" {
		t.Fatalf("prestar: %d %v", loan.status, loan.body)
	}

	// Solo había un ejemplar: Luis no puede llevárselo.
	denied := call(t, h, "POST", "/loans", map[string]any{"book_id": book, "member_id": luis})
	if denied.status != 422 || errorField(denied, "code") != "no_copies_available" {
		t.Fatalf("préstamo sin ejemplares: %d %v", denied.status, denied.body)
	}

	if r := call(t, h, "GET", "/members/1/loans?status=active", nil); r.body["total"] != float64(1) {
		t.Fatalf("préstamos de Ana: %v", r.body)
	}

	returned := call(t, h, "POST", "/loans/1/return", nil)
	if returned.status != 200 || returned.body["status"] != "returned" || returned.body["fine"] != float64(0) {
		t.Fatalf("devolver: %d %v", returned.status, returned.body)
	}
	if r := call(t, h, "POST", "/loans/1/return", nil); r.status != 422 {
		t.Fatalf("devolver dos veces: %d", r.status)
	}

	if r := call(t, h, "POST", "/loans", map[string]any{"book_id": book, "member_id": luis}); r.status != 201 {
		t.Fatalf("préstamo tras la devolución: %d %v", r.status, r.body)
	}

	if r := call(t, h, "GET", "/loans?status=perdido", nil); r.status != 400 {
		t.Fatalf("estado no válido: %d", r.status)
	}
	if r := call(t, h, "POST", "/loans", map[string]any{}); r.status != 400 {
		t.Fatalf("préstamo sin datos: %d", r.status)
	}

	stats := call(t, h, "GET", "/stats", nil)
	if stats.body["on_loan"] != float64(1) || stats.body["members"] != float64(2) {
		t.Fatalf("estadísticas: %v", stats.body)
	}
}

func TestValidISBN(t *testing.T) {
	cases := map[string]bool{
		"9780306406157": true,  // ISBN-13 válido
		"9780306406158": false, // dígito de control incorrecto
		"0306406152":    true,  // ISBN-10 válido
		"080442957X":    true,  // ISBN-10 con X final
		"X804429570":    false, // la X solo puede ir al final
		"12345":         false,
	}
	for isbn, want := range cases {
		if got := validISBN(isbn); got != want {
			t.Errorf("validISBN(%q) = %v, quería %v", isbn, got, want)
		}
	}
}
