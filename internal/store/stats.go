package store

import (
	"context"
	"fmt"
)

// Stats resume el estado de la biblioteca.
type Stats struct {
	Books    int       `json:"books"`
	Copies   int       `json:"copies"`
	OnLoan   int       `json:"on_loan"`
	Overdue  int       `json:"overdue"`
	Members  int       `json:"members"`
	TopBooks []TopBook `json:"top_books"`
}

// TopBook es uno de los libros más prestados.
type TopBook struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Loans  int    `json:"loans"`
}

// Stats calcula los totales y los 5 libros más prestados.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM books),
			(SELECT COALESCE(SUM(copies), 0) FROM books),
			(SELECT COUNT(*) FROM loans WHERE returned_at IS NULL),
			(SELECT COUNT(*) FROM loans WHERE returned_at IS NULL AND due_at < ?),
			(SELECT COUNT(*) FROM members)`,
		formatTime(s.clock())).Scan(&st.Books, &st.Copies, &st.OnLoan, &st.Overdue, &st.Members)
	if err != nil {
		return Stats{}, fmt.Errorf("calcular estadísticas: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT b.id, b.title, b.author, COUNT(l.id) AS total
		FROM books b JOIN loans l ON l.book_id = b.id
		GROUP BY b.id
		ORDER BY total DESC, b.title
		LIMIT 5`)
	if err != nil {
		return Stats{}, fmt.Errorf("libros más prestados: %w", err)
	}
	defer rows.Close()

	st.TopBooks = []TopBook{}
	for rows.Next() {
		var t TopBook
		if err := rows.Scan(&t.ID, &t.Title, &t.Author, &t.Loans); err != nil {
			return Stats{}, err
		}
		st.TopBooks = append(st.TopBooks, t)
	}
	return st, rows.Err()
}
