package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Book es un título del catálogo. Copies es el total de ejemplares y Available los que no están prestados.
type Book struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	ISBN      string    `json:"isbn,omitempty"`
	Year      int       `json:"year,omitempty"`
	Genre     string    `json:"genre,omitempty"`
	Copies    int       `json:"copies"`
	Available int       `json:"available"`
	CreatedAt time.Time `json:"created_at"`
}

// BookInput son los datos que se pueden crear o modificar de un libro.
type BookInput struct {
	Title  string `json:"title"`
	Author string `json:"author"`
	ISBN   string `json:"isbn"`
	Year   int    `json:"year"`
	Genre  string `json:"genre"`
	Copies int    `json:"copies"`
}

// BookFilter filtra y pagina el catálogo.
type BookFilter struct {
	Query         string // busca en título, autor o ISBN exacto
	OnlyAvailable bool
	Limit         int
	Offset        int
}

const openLoansForBook = `(SELECT COUNT(*) FROM loans l WHERE l.book_id = b.id AND l.returned_at IS NULL)`

const bookColumns = `b.id, b.title, b.author, COALESCE(b.isbn, ''), COALESCE(b.year, 0), b.genre,
	b.copies, b.copies - ` + openLoansForBook + `, b.created_at`

func scanBook(row scanner) (Book, error) {
	var b Book
	var created string
	err := row.Scan(&b.ID, &b.Title, &b.Author, &b.ISBN, &b.Year, &b.Genre, &b.Copies, &b.Available, &created)
	b.CreatedAt = parseTime(created)
	return b, err
}

func getBook(ctx context.Context, q querier, id int64) (Book, error) {
	b, err := scanBook(q.QueryRowContext(ctx, `SELECT `+bookColumns+` FROM books b WHERE b.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Book{}, notFound("Libro no encontrado.")
	}
	return b, err
}

// GetBook devuelve un libro por su id.
func (s *Store) GetBook(ctx context.Context, id int64) (Book, error) {
	return getBook(ctx, s.db, id)
}

// CreateBook añade un libro al catálogo.
func (s *Store) CreateBook(ctx context.Context, in BookInput) (Book, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO books (title, author, isbn, year, genre, copies, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.Title, in.Author, nullString(in.ISBN), nullInt(in.Year), in.Genre, in.Copies, formatTime(s.clock()))
	if isUniqueViolation(err) {
		return Book{}, conflict("duplicate_isbn", "Ya existe un libro con ese ISBN.")
	}
	if err != nil {
		return Book{}, fmt.Errorf("crear libro: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Book{}, err
	}
	return s.GetBook(ctx, id)
}

// UpdateBook reemplaza los datos de un libro. No permite dejar menos ejemplares que los prestados.
func (s *Store) UpdateBook(ctx context.Context, id int64, in BookInput) (Book, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Book{}, err
	}
	defer tx.Rollback()

	current, err := getBook(ctx, tx, id)
	if err != nil {
		return Book{}, err
	}
	if onLoan := current.Copies - current.Available; in.Copies < onLoan {
		return Book{}, ruleViolation("copies_below_loans",
			fmt.Sprintf("Hay %d ejemplar(es) prestado(s): no puedes dejar menos ejemplares que esos.", onLoan))
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE books SET title = ?, author = ?, isbn = ?, year = ?, genre = ?, copies = ? WHERE id = ?`,
		in.Title, in.Author, nullString(in.ISBN), nullInt(in.Year), in.Genre, in.Copies, id)
	if isUniqueViolation(err) {
		return Book{}, conflict("duplicate_isbn", "Ya existe un libro con ese ISBN.")
	}
	if err != nil {
		return Book{}, fmt.Errorf("actualizar libro: %w", err)
	}

	book, err := getBook(ctx, tx, id)
	if err != nil {
		return Book{}, err
	}
	return book, tx.Commit()
}

// DeleteBook borra un libro que nunca se ha prestado (los préstamos son historial y no se pierden).
func (s *Store) DeleteBook(ctx context.Context, id int64) error {
	var loans int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loans WHERE book_id = ?`, id).Scan(&loans); err != nil {
		return err
	}
	if loans > 0 {
		return conflict("book_has_loans", "El libro tiene préstamos registrados y no se puede borrar.")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("borrar libro: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("Libro no encontrado.")
	}
	return nil
}

// ListBooks devuelve una página del catálogo y el total de resultados.
func (s *Store) ListBooks(ctx context.Context, f BookFilter) ([]Book, int, error) {
	where := []string{"1 = 1"}
	var args []any
	if f.Query != "" {
		pattern := likePattern(f.Query)
		where = append(where, `(b.title LIKE ? ESCAPE '\' OR b.author LIKE ? ESCAPE '\' OR b.isbn = ?)`)
		args = append(args, pattern, pattern, f.Query)
	}
	if f.OnlyAvailable {
		where = append(where, `b.copies > `+openLoansForBook)
	}
	condition := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books b WHERE `+condition, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("contar libros: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bookColumns+` FROM books b WHERE `+condition+` ORDER BY b.title COLLATE NOCASE, b.id LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("listar libros: %w", err)
	}
	defer rows.Close()

	books := []Book{}
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, 0, err
		}
		books = append(books, b)
	}
	return books, total, rows.Err()
}
