// Package store guarda los datos de la biblioteca en SQLite y aplica las reglas de negocio.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Driver de SQLite escrito en Go puro: no necesita cgo ni compilador de C.
)

// Reglas de negocio de la biblioteca.
const (
	MaxActiveLoans = 3                   // préstamos simultáneos por socio
	LoanPeriod     = 14 * 24 * time.Hour // duración de un préstamo
	FinePerDay     = 0.25                // multa en USD por día de retraso
)

const schema = `
CREATE TABLE IF NOT EXISTS books (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	title      TEXT    NOT NULL,
	author     TEXT    NOT NULL,
	isbn       TEXT    UNIQUE,
	year       INTEGER,
	genre      TEXT    NOT NULL DEFAULT '',
	copies     INTEGER NOT NULL CHECK (copies >= 0),
	created_at TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS members (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT NOT NULL,
	email      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS loans (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	book_id     INTEGER NOT NULL REFERENCES books (id) ON DELETE RESTRICT,
	member_id   INTEGER NOT NULL REFERENCES members (id) ON DELETE RESTRICT,
	loaned_at   TEXT    NOT NULL,
	due_at      TEXT    NOT NULL,
	returned_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_loans_open_by_book ON loans (book_id) WHERE returned_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_loans_by_member ON loans (member_id, returned_at);
`

// Store da acceso a la base de datos.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Option configura un Store.
type Option func(*Store)

// WithClock cambia el reloj del Store. Los tests lo usan para simular el paso del tiempo.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// Open abre (o crea) la base de datos en path. Usa ":memory:" para una base temporal.
func Open(path string, opts ...Option) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if path != ":memory:" {
		dsn += "&_pragma=journal_mode(WAL)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir base de datos: %w", err)
	}
	// SQLite admite un único escritor a la vez. Con una sola conexión las transacciones
	// se ejecutan en orden (sin carreras al comprobar ejemplares disponibles) y la base
	// en memoria de los tests es la misma para todas las consultas.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("crear esquema: %w", err)
	}
	return s, nil
}

// Close cierra la base de datos.
func (s *Store) Close() error { return s.db.Close() }

// Ping comprueba que la base de datos responde.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// querier lo cumplen *sql.DB y *sql.Tx, así las funciones sirven dentro y fuera de una transacción.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface {
	Scan(dest ...any) error
}

// Las fechas se guardan como texto RFC 3339 en UTC: se pueden comparar como cadenas.
const timeFormat = time.RFC3339

func (s *Store) clock() time.Time { return s.now().UTC().Truncate(time.Second) }

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(value string) time.Time {
	t, _ := time.Parse(timeFormat, value)
	return t
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// likePattern convierte un texto de búsqueda en un patrón LIKE, escapando % y _.
func likePattern(query string) string {
	escaper := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + escaper.Replace(query) + "%"
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
