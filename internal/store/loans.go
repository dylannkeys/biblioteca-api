package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Estados de un préstamo.
const (
	StatusActive   = "active"   // prestado y dentro de plazo
	StatusOverdue  = "overdue"  // prestado y fuera de plazo
	StatusReturned = "returned" // devuelto
)

// Loan es el préstamo de un libro a un socio.
type Loan struct {
	ID         int64      `json:"id"`
	BookID     int64      `json:"book_id"`
	BookTitle  string     `json:"book_title"`
	MemberID   int64      `json:"member_id"`
	MemberName string     `json:"member_name"`
	LoanedAt   time.Time  `json:"loaned_at"`
	DueAt      time.Time  `json:"due_at"`
	ReturnedAt *time.Time `json:"returned_at"`
	Status     string     `json:"status"`
	DaysLate   int        `json:"days_late"`
	Fine       float64    `json:"fine"`
}

// LoanFilter filtra y pagina los préstamos. Status "active" incluye también los vencidos.
type LoanFilter struct {
	Status   string
	MemberID int64
	BookID   int64
	Limit    int
	Offset   int
}

const loanSelect = `SELECT l.id, l.book_id, b.title, l.member_id, m.name, l.loaned_at, l.due_at, l.returned_at
	FROM loans l
	JOIN books b ON b.id = l.book_id
	JOIN members m ON m.id = l.member_id`

// scanLoan lee un préstamo y calcula su estado, días de retraso y multa con el reloj actual.
func (s *Store) scanLoan(row scanner) (Loan, error) {
	var l Loan
	var loaned, due string
	var returned sql.NullString
	if err := row.Scan(&l.ID, &l.BookID, &l.BookTitle, &l.MemberID, &l.MemberName, &loaned, &due, &returned); err != nil {
		return Loan{}, err
	}
	l.LoanedAt = parseTime(loaned)
	l.DueAt = parseTime(due)

	end := s.clock()
	switch {
	case returned.Valid:
		t := parseTime(returned.String)
		l.ReturnedAt = &t
		end = t
		l.Status = StatusReturned
	case end.After(l.DueAt):
		l.Status = StatusOverdue
	default:
		l.Status = StatusActive
	}

	// Cualquier fracción de día cuenta como un día de retraso.
	if end.After(l.DueAt) {
		l.DaysLate = int(math.Ceil(end.Sub(l.DueAt).Hours() / 24))
	}
	l.Fine = float64(l.DaysLate) * FinePerDay
	return l, nil
}

func (s *Store) getLoan(ctx context.Context, q querier, id int64) (Loan, error) {
	l, err := s.scanLoan(q.QueryRowContext(ctx, loanSelect+` WHERE l.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Loan{}, notFound("Préstamo no encontrado.")
	}
	return l, err
}

// GetLoan devuelve un préstamo por su id.
func (s *Store) GetLoan(ctx context.Context, id int64) (Loan, error) {
	return s.getLoan(ctx, s.db, id)
}

// Borrow presta un libro a un socio si se cumplen todas las reglas de negocio.
// Todo ocurre en una transacción: las comprobaciones y la inserción son atómicas.
func (s *Store) Borrow(ctx context.Context, bookID, memberID int64) (Loan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Loan{}, err
	}
	defer tx.Rollback()

	var memberExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = ?)`, memberID).Scan(&memberExists); err != nil {
		return Loan{}, err
	}
	if !memberExists {
		return Loan{}, notFound("Socio no encontrado.")
	}

	book, err := getBook(ctx, tx, bookID)
	if err != nil {
		return Loan{}, err
	}

	now := s.clock()
	var open, overdue, sameBook int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(due_at < ?), 0), COALESCE(SUM(book_id = ?), 0)
		FROM loans WHERE member_id = ? AND returned_at IS NULL`,
		formatTime(now), bookID, memberID).Scan(&open, &overdue, &sameBook)
	if err != nil {
		return Loan{}, err
	}

	switch {
	case overdue > 0:
		return Loan{}, ruleViolation("member_has_overdue",
			"El socio tiene préstamos vencidos: debe devolverlos antes de pedir otro libro.")
	case open >= MaxActiveLoans:
		return Loan{}, ruleViolation("loan_limit_reached",
			fmt.Sprintf("El socio ya tiene %d préstamos activos (máximo %d).", open, MaxActiveLoans))
	case sameBook > 0:
		return Loan{}, ruleViolation("already_borrowed", "El socio ya tiene prestado este libro.")
	case book.Available <= 0:
		return Loan{}, ruleViolation("no_copies_available", "No quedan ejemplares disponibles de este libro.")
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO loans (book_id, member_id, loaned_at, due_at) VALUES (?, ?, ?, ?)`,
		bookID, memberID, formatTime(now), formatTime(now.Add(LoanPeriod)))
	if err != nil {
		return Loan{}, fmt.Errorf("crear préstamo: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Loan{}, err
	}

	loan, err := s.getLoan(ctx, tx, id)
	if err != nil {
		return Loan{}, err
	}
	return loan, tx.Commit()
}

// Return registra la devolución de un préstamo. La respuesta incluye la multa si hubo retraso.
func (s *Store) Return(ctx context.Context, loanID int64) (Loan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Loan{}, err
	}
	defer tx.Rollback()

	loan, err := s.getLoan(ctx, tx, loanID)
	if err != nil {
		return Loan{}, err
	}
	if loan.ReturnedAt != nil {
		return Loan{}, ruleViolation("already_returned", "Este préstamo ya fue devuelto.")
	}

	if _, err := tx.ExecContext(ctx, `UPDATE loans SET returned_at = ? WHERE id = ?`, formatTime(s.clock()), loanID); err != nil {
		return Loan{}, fmt.Errorf("devolver préstamo: %w", err)
	}

	loan, err = s.getLoan(ctx, tx, loanID)
	if err != nil {
		return Loan{}, err
	}
	return loan, tx.Commit()
}

// ListLoans devuelve una página de préstamos, del más reciente al más antiguo.
func (s *Store) ListLoans(ctx context.Context, f LoanFilter) ([]Loan, int, error) {
	where := []string{"1 = 1"}
	var args []any
	switch f.Status {
	case StatusActive:
		where = append(where, "l.returned_at IS NULL")
	case StatusOverdue:
		where = append(where, "l.returned_at IS NULL AND l.due_at < ?")
		args = append(args, formatTime(s.clock()))
	case StatusReturned:
		where = append(where, "l.returned_at IS NOT NULL")
	}
	if f.MemberID > 0 {
		where = append(where, "l.member_id = ?")
		args = append(args, f.MemberID)
	}
	if f.BookID > 0 {
		where = append(where, "l.book_id = ?")
		args = append(args, f.BookID)
	}
	condition := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loans l WHERE `+condition, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("contar préstamos: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		loanSelect+` WHERE `+condition+` ORDER BY l.loaned_at DESC, l.id DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("listar préstamos: %w", err)
	}
	defer rows.Close()

	loans := []Loan{}
	for rows.Next() {
		l, err := s.scanLoan(rows)
		if err != nil {
			return nil, 0, err
		}
		loans = append(loans, l)
	}
	return loans, total, rows.Err()
}
