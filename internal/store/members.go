package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Member es un socio de la biblioteca.
type Member struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Email       string    `json:"email"`
	ActiveLoans int       `json:"active_loans"`
	CreatedAt   time.Time `json:"created_at"`
}

// MemberInput son los datos para dar de alta un socio.
type MemberInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// MemberFilter filtra y pagina los socios.
type MemberFilter struct {
	Query  string // busca en nombre o correo
	Limit  int
	Offset int
}

const memberColumns = `m.id, m.name, m.email,
	(SELECT COUNT(*) FROM loans l WHERE l.member_id = m.id AND l.returned_at IS NULL), m.created_at`

func scanMember(row scanner) (Member, error) {
	var m Member
	var created string
	err := row.Scan(&m.ID, &m.Name, &m.Email, &m.ActiveLoans, &created)
	m.CreatedAt = parseTime(created)
	return m, err
}

// GetMember devuelve un socio por su id.
func (s *Store) GetMember(ctx context.Context, id int64) (Member, error) {
	m, err := scanMember(s.db.QueryRowContext(ctx, `SELECT `+memberColumns+` FROM members m WHERE m.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, notFound("Socio no encontrado.")
	}
	return m, err
}

// CreateMember da de alta un socio. El correo no se puede repetir (sin distinguir mayúsculas).
func (s *Store) CreateMember(ctx context.Context, in MemberInput) (Member, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO members (name, email, created_at) VALUES (?, ?, ?)`,
		in.Name, in.Email, formatTime(s.clock()))
	if isUniqueViolation(err) {
		return Member{}, conflict("duplicate_email", "Ya existe un socio con ese correo.")
	}
	if err != nil {
		return Member{}, fmt.Errorf("crear socio: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Member{}, err
	}
	return s.GetMember(ctx, id)
}

// ListMembers devuelve una página de socios y el total de resultados.
func (s *Store) ListMembers(ctx context.Context, f MemberFilter) ([]Member, int, error) {
	condition := "1 = 1"
	var args []any
	if f.Query != "" {
		pattern := likePattern(f.Query)
		condition = `(m.name LIKE ? ESCAPE '\' OR m.email LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM members m WHERE `+condition, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("contar socios: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+memberColumns+` FROM members m WHERE `+condition+` ORDER BY m.name COLLATE NOCASE, m.id LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("listar socios: %w", err)
	}
	defer rows.Close()

	members := []Member{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, 0, err
		}
		members = append(members, m)
	}
	return members, total, rows.Err()
}
