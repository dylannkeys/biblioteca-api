package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClock permite avanzar el tiempo en los tests sin esperar.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestStore(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)}
	s, err := Open(":memory:", WithClock(clock.Now))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, clock
}

func mustBook(t *testing.T, s *Store, title string, copies int) Book {
	t.Helper()
	b, err := s.CreateBook(context.Background(), BookInput{Title: title, Author: "Autor", Copies: copies})
	if err != nil {
		t.Fatalf("CreateBook: %v", err)
	}
	return b
}

func mustMember(t *testing.T, s *Store, email string) Member {
	t.Helper()
	m, err := s.CreateMember(context.Background(), MemberInput{Name: "Socio", Email: email})
	if err != nil {
		t.Fatalf("CreateMember: %v", err)
	}
	return m
}

func mustBorrow(t *testing.T, s *Store, bookID, memberID int64) Loan {
	t.Helper()
	l, err := s.Borrow(context.Background(), bookID, memberID)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	return l
}

func errorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestBorrowAndReturnLate(t *testing.T) {
	s, clock := newTestStore(t)
	ctx := context.Background()
	book := mustBook(t, s, "Rayuela", 2)
	member := mustMember(t, s, "ana@example.com")

	loan := mustBorrow(t, s, book.ID, member.ID)
	if loan.Status != StatusActive || !loan.DueAt.Equal(loan.LoanedAt.Add(LoanPeriod)) {
		t.Fatalf("préstamo nuevo inesperado: %+v", loan)
	}
	if b, _ := s.GetBook(ctx, book.ID); b.Available != 1 {
		t.Fatalf("available = %d, quería 1", b.Available)
	}

	// 14 días de préstamo + 2 días y 3 horas de retraso = 3 días de multa (cada fracción cuenta).
	clock.Advance(LoanPeriod + 51*time.Hour)
	loan, _ = s.GetLoan(ctx, loan.ID)
	if loan.Status != StatusOverdue || loan.DaysLate != 3 || loan.Fine != 0.75 {
		t.Fatalf("préstamo vencido inesperado: status=%s días=%d multa=%v", loan.Status, loan.DaysLate, loan.Fine)
	}

	returned, err := s.Return(ctx, loan.ID)
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if returned.Status != StatusReturned || returned.ReturnedAt == nil || returned.Fine != 0.75 {
		t.Fatalf("devolución inesperada: %+v", returned)
	}

	// La multa queda fija al devolver aunque pase el tiempo.
	clock.Advance(10 * 24 * time.Hour)
	if l, _ := s.GetLoan(ctx, loan.ID); l.Fine != 0.75 {
		t.Fatalf("la multa cambió después de devolver: %v", l.Fine)
	}
	if b, _ := s.GetBook(ctx, book.ID); b.Available != 2 {
		t.Fatalf("available = %d, quería 2", b.Available)
	}
}

func TestBorrowRules(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store, clock *fakeClock) (bookID, memberID int64)
		wantCode string
	}{
		{
			name: "sin ejemplares disponibles",
			setup: func(t *testing.T, s *Store, _ *fakeClock) (int64, int64) {
				book := mustBook(t, s, "Único", 1)
				mustBorrow(t, s, book.ID, mustMember(t, s, "otro@example.com").ID)
				return book.ID, mustMember(t, s, "yo@example.com").ID
			},
			wantCode: "no_copies_available",
		},
		{
			name: "límite de préstamos alcanzado",
			setup: func(t *testing.T, s *Store, _ *fakeClock) (int64, int64) {
				member := mustMember(t, s, "lector@example.com")
				for i := 0; i < MaxActiveLoans; i++ {
					mustBorrow(t, s, mustBook(t, s, "Libro", 1).ID, member.ID)
				}
				return mustBook(t, s, "Uno más", 1).ID, member.ID
			},
			wantCode: "loan_limit_reached",
		},
		{
			name: "ya tiene ese libro",
			setup: func(t *testing.T, s *Store, _ *fakeClock) (int64, int64) {
				book := mustBook(t, s, "Repetido", 3)
				member := mustMember(t, s, "lector@example.com")
				mustBorrow(t, s, book.ID, member.ID)
				return book.ID, member.ID
			},
			wantCode: "already_borrowed",
		},
		{
			name: "tiene préstamos vencidos",
			setup: func(t *testing.T, s *Store, clock *fakeClock) (int64, int64) {
				member := mustMember(t, s, "moroso@example.com")
				mustBorrow(t, s, mustBook(t, s, "Viejo", 1).ID, member.ID)
				clock.Advance(LoanPeriod + time.Hour)
				return mustBook(t, s, "Nuevo", 1).ID, member.ID
			},
			wantCode: "member_has_overdue",
		},
		{
			name: "socio inexistente",
			setup: func(t *testing.T, s *Store, _ *fakeClock) (int64, int64) {
				return mustBook(t, s, "Libro", 1).ID, 999
			},
			wantCode: "not_found",
		},
		{
			name: "libro inexistente",
			setup: func(t *testing.T, s *Store, _ *fakeClock) (int64, int64) {
				return 999, mustMember(t, s, "lector@example.com").ID
			},
			wantCode: "not_found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, clock := newTestStore(t)
			bookID, memberID := tt.setup(t, s, clock)
			_, err := s.Borrow(context.Background(), bookID, memberID)
			if got := errorCode(err); got != tt.wantCode {
				t.Fatalf("código = %q (error: %v), quería %q", got, err, tt.wantCode)
			}
		})
	}
}

func TestReturnTwice(t *testing.T) {
	s, _ := newTestStore(t)
	loan := mustBorrow(t, s, mustBook(t, s, "Libro", 1).ID, mustMember(t, s, "a@example.com").ID)
	if _, err := s.Return(context.Background(), loan.ID); err != nil {
		t.Fatalf("primera devolución: %v", err)
	}
	if _, err := s.Return(context.Background(), loan.ID); errorCode(err) != "already_returned" {
		t.Fatalf("segunda devolución: %v", err)
	}
}

func TestUpdateBookCannotDropBelowLoans(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	book := mustBook(t, s, "Popular", 2)
	mustBorrow(t, s, book.ID, mustMember(t, s, "a@example.com").ID)
	mustBorrow(t, s, book.ID, mustMember(t, s, "b@example.com").ID)

	_, err := s.UpdateBook(ctx, book.ID, BookInput{Title: "Popular", Author: "Autor", Copies: 1})
	if errorCode(err) != "copies_below_loans" {
		t.Fatalf("UpdateBook: %v", err)
	}
	updated, err := s.UpdateBook(ctx, book.ID, BookInput{Title: "Popular", Author: "Autor", Copies: 5})
	if err != nil || updated.Available != 3 {
		t.Fatalf("UpdateBook a 5 ejemplares: %+v, %v", updated, err)
	}
}

func TestDeleteBook(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	unused := mustBook(t, s, "Sin préstamos", 1)
	if err := s.DeleteBook(ctx, unused.ID); err != nil {
		t.Fatalf("DeleteBook: %v", err)
	}
	if _, err := s.GetBook(ctx, unused.ID); errorCode(err) != "not_found" {
		t.Fatalf("el libro sigue existiendo: %v", err)
	}

	used := mustBook(t, s, "Con historial", 1)
	loan := mustBorrow(t, s, used.ID, mustMember(t, s, "a@example.com").ID)
	if _, err := s.Return(ctx, loan.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBook(ctx, used.ID); errorCode(err) != "book_has_loans" {
		t.Fatalf("DeleteBook con historial: %v", err)
	}
}

func TestUniqueConstraints(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	in := BookInput{Title: "A", Author: "B", ISBN: "9780306406157", Copies: 1}
	if _, err := s.CreateBook(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBook(ctx, in); errorCode(err) != "duplicate_isbn" {
		t.Fatalf("ISBN repetido: %v", err)
	}
	// Varios libros sin ISBN no chocan entre sí.
	mustBook(t, s, "Sin ISBN 1", 1)
	mustBook(t, s, "Sin ISBN 2", 1)

	mustMember(t, s, "ana@example.com")
	if _, err := s.CreateMember(ctx, MemberInput{Name: "Ana", Email: "ANA@example.com"}); errorCode(err) != "duplicate_email" {
		t.Fatalf("correo repetido: %v", err)
	}
}

func TestListBooksFilters(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	mustBook(t, s, "Cien años de soledad", 1)
	mustBook(t, s, "El 100% de algo", 1)
	busy := mustBook(t, s, "Ocupado", 1)
	mustBorrow(t, s, busy.ID, mustMember(t, s, "a@example.com").ID)

	books, total, err := s.ListBooks(ctx, BookFilter{Query: "100%", Limit: 10})
	if err != nil || total != 1 || books[0].Title != "El 100% de algo" {
		t.Fatalf("búsqueda con %% literal: total=%d, %v", total, err)
	}

	_, total, _ = s.ListBooks(ctx, BookFilter{OnlyAvailable: true, Limit: 10})
	if total != 2 {
		t.Fatalf("disponibles = %d, quería 2", total)
	}

	page, total, _ := s.ListBooks(ctx, BookFilter{Limit: 2, Offset: 2})
	if total != 3 || len(page) != 1 {
		t.Fatalf("paginación: total=%d página=%d", total, len(page))
	}
}

func TestSeedAndStats(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	seeded, err := s.SeedIfEmpty(ctx)
	if err != nil || !seeded {
		t.Fatalf("SeedIfEmpty: %v, %v", seeded, err)
	}
	if seeded, _ := s.SeedIfEmpty(ctx); seeded {
		t.Fatal("SeedIfEmpty cargó datos dos veces")
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Books != 10 || st.Members != 4 || st.OnLoan != 4 || st.Overdue != 1 {
		t.Fatalf("estadísticas inesperadas: %+v", st)
	}
	if len(st.TopBooks) == 0 || st.TopBooks[0].Title != "Cien años de soledad" {
		t.Fatalf("libros más prestados: %+v", st.TopBooks)
	}
}
