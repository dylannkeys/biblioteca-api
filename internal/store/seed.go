package store

import (
	"context"
	"fmt"
	"time"
)

// SeedIfEmpty carga un catálogo de ejemplo si la base está vacía. Devuelve true si cargó datos.
func (s *Store) SeedIfEmpty(ctx context.Context) (bool, error) {
	var books int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books`).Scan(&books); err != nil {
		return false, err
	}
	if books > 0 {
		return false, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	now := s.clock()
	created := formatTime(now.Add(-60 * 24 * time.Hour))

	catalog := []BookInput{
		{Title: "Cien años de soledad", Author: "Gabriel García Márquez", Year: 1967, Genre: "Novela", Copies: 3},
		{Title: "Huasipungo", Author: "Jorge Icaza", Year: 1934, Genre: "Novela", Copies: 2},
		{Title: "La ciudad y los perros", Author: "Mario Vargas Llosa", Year: 1963, Genre: "Novela", Copies: 2},
		{Title: "Rayuela", Author: "Julio Cortázar", Year: 1963, Genre: "Novela", Copies: 1},
		{Title: "Ficciones", Author: "Jorge Luis Borges", Year: 1944, Genre: "Cuentos", Copies: 2},
		{Title: "Pedro Páramo", Author: "Juan Rulfo", Year: 1955, Genre: "Novela", Copies: 2},
		{Title: "El principito", Author: "Antoine de Saint-Exupéry", Year: 1943, Genre: "Infantil", Copies: 4},
		{Title: "Clean Code", Author: "Robert C. Martin", Year: 2008, Genre: "Programación", Copies: 2},
		{Title: "The Go Programming Language", Author: "Alan Donovan y Brian Kernighan", Year: 2015, Genre: "Programación", Copies: 1},
		{Title: "Sapiens", Author: "Yuval Noah Harari", Year: 2011, Genre: "Historia", Copies: 2},
	}
	bookIDs := map[string]int64{}
	for _, b := range catalog {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO books (title, author, year, genre, copies, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			b.Title, b.Author, b.Year, b.Genre, b.Copies, created)
		if err != nil {
			return false, fmt.Errorf("cargar libro %q: %w", b.Title, err)
		}
		bookIDs[b.Title], _ = res.LastInsertId()
	}

	members := []MemberInput{
		{Name: "Ana Torres", Email: "ana.torres@example.com"},
		{Name: "Luis Paredes", Email: "luis.paredes@example.com"},
		{Name: "María José Cevallos", Email: "mariajose.cevallos@example.com"},
		{Name: "Carlos Andrade", Email: "carlos.andrade@example.com"},
	}
	memberIDs := map[string]int64{}
	for _, m := range members {
		res, err := tx.ExecContext(ctx, `INSERT INTO members (name, email, created_at) VALUES (?, ?, ?)`, m.Name, m.Email, created)
		if err != nil {
			return false, fmt.Errorf("cargar socio %q: %w", m.Name, err)
		}
		memberIDs[m.Name], _ = res.LastInsertId()
	}

	days := func(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }
	loans := []struct {
		book, member       string
		loanedAgo, backAgo int // días; backAgo = 0 significa que sigue prestado
	}{
		{"Cien años de soledad", "Ana Torres", 3, 0},
		{"Clean Code", "Ana Torres", 5, 0},
		{"Huasipungo", "Luis Paredes", 20, 0}, // vencido
		{"Pedro Páramo", "María José Cevallos", 1, 0},
		{"Rayuela", "María José Cevallos", 30, 25},
		{"El principito", "Carlos Andrade", 16, 1}, // devuelto con retraso
		{"Cien años de soledad", "Carlos Andrade", 40, 30},
		{"Ficciones", "Ana Torres", 35, 22},
	}
	for _, l := range loans {
		loanedAt := now.Add(-days(l.loanedAgo))
		var returnedAt any
		if l.backAgo > 0 {
			returnedAt = formatTime(now.Add(-days(l.backAgo)))
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO loans (book_id, member_id, loaned_at, due_at, returned_at) VALUES (?, ?, ?, ?, ?)`,
			bookIDs[l.book], memberIDs[l.member], formatTime(loanedAt), formatTime(loanedAt.Add(LoanPeriod)), returnedAt)
		if err != nil {
			return false, fmt.Errorf("cargar préstamo: %w", err)
		}
	}

	return true, tx.Commit()
}
