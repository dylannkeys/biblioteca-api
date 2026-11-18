package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/dylannkeys/biblioteca-api/internal/store"
)

// ---------- Libros ----------

func (s *Server) listBooks(w http.ResponseWriter, r *http.Request) {
	errs := fieldErrors{}
	p := parseQuery(r, errs)
	filter := store.BookFilter{Query: strings.TrimSpace(r.URL.Query().Get("q")), Limit: p.size, Offset: p.offset()}
	if v := r.URL.Query().Get("available"); v != "" {
		available, err := strconv.ParseBool(v)
		errs.check(err == nil, "available", "Debe ser true o false.")
		filter.OnlyAvailable = available
	}
	if respondInvalid(w, errs) {
		return
	}

	books, total, err := s.store.ListBooks(r.Context(), filter)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Book]{Data: books, Page: p.number, PageSize: p.size, Total: total})
}

func (s *Server) createBook(w http.ResponseWriter, r *http.Request) {
	var in store.BookInput
	if !readJSON(w, r, &in) || respondInvalid(w, validateBook(&in, true)) {
		return
	}
	book, err := s.store.CreateBook(r.Context(), in)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/books/%d", book.ID))
	writeJSON(w, http.StatusCreated, book)
}

func (s *Server) getBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	book, err := s.store.GetBook(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, book)
}

func (s *Server) updateBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.BookInput
	if !readJSON(w, r, &in) || respondInvalid(w, validateBook(&in, false)) {
		return
	}
	book, err := s.store.UpdateBook(r.Context(), id, in)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, book)
}

func (s *Server) deleteBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteBook(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Socios ----------

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	errs := fieldErrors{}
	p := parseQuery(r, errs)
	if respondInvalid(w, errs) {
		return
	}
	filter := store.MemberFilter{Query: strings.TrimSpace(r.URL.Query().Get("q")), Limit: p.size, Offset: p.offset()}
	members, total, err := s.store.ListMembers(r.Context(), filter)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Member]{Data: members, Page: p.number, PageSize: p.size, Total: total})
}

func (s *Server) createMember(w http.ResponseWriter, r *http.Request) {
	var in store.MemberInput
	if !readJSON(w, r, &in) || respondInvalid(w, validateMember(&in)) {
		return
	}
	member, err := s.store.CreateMember(r.Context(), in)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/members/%d", member.ID))
	writeJSON(w, http.StatusCreated, member)
}

func (s *Server) getMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	member, err := s.store.GetMember(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

func (s *Server) memberLoans(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.store.GetMember(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.respondLoans(w, r, id)
}

// ---------- Préstamos ----------

func (s *Server) listLoans(w http.ResponseWriter, r *http.Request) {
	s.respondLoans(w, r, 0)
}

func (s *Server) respondLoans(w http.ResponseWriter, r *http.Request, memberID int64) {
	errs := fieldErrors{}
	p := parseQuery(r, errs)
	filter := store.LoanFilter{
		Status:   r.URL.Query().Get("status"),
		MemberID: memberID,
		BookID:   queryID(r, "book_id", errs),
		Limit:    p.size,
		Offset:   p.offset(),
	}
	if memberID == 0 {
		filter.MemberID = queryID(r, "member_id", errs)
	}
	switch filter.Status {
	case "", store.StatusActive, store.StatusOverdue, store.StatusReturned:
	default:
		errs.check(false, "status", "Debe ser active, overdue o returned.")
	}
	if respondInvalid(w, errs) {
		return
	}

	loans, total, err := s.store.ListLoans(r.Context(), filter)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page[store.Loan]{Data: loans, Page: p.number, PageSize: p.size, Total: total})
}

func (s *Server) createLoan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BookID   int64 `json:"book_id"`
		MemberID int64 `json:"member_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	errs := fieldErrors{}
	errs.check(in.BookID > 0, "book_id", "Indica el id del libro.")
	errs.check(in.MemberID > 0, "member_id", "Indica el id del socio.")
	if respondInvalid(w, errs) {
		return
	}

	loan, err := s.store.Borrow(r.Context(), in.BookID, in.MemberID)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/loans/%d", loan.ID))
	writeJSON(w, http.StatusCreated, loan)
}

func (s *Server) getLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	loan, err := s.store.GetLoan(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, loan)
}

func (s *Server) returnLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	loan, err := s.store.Return(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, loan)
}
