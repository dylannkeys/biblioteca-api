package api

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dylannkeys/biblioteca-api/internal/store"
)

// fieldErrors acumula el primer error de cada campo.
type fieldErrors map[string]string

func (f fieldErrors) check(ok bool, field, message string) {
	if _, exists := f[field]; !ok && !exists {
		f[field] = message
	}
}

func length(s string) int { return utf8.RuneCountInString(s) }

// validateBook limpia y valida los datos de un libro. Al crear, copies por defecto es 1.
func validateBook(in *store.BookInput, creating bool) fieldErrors {
	in.Title = strings.TrimSpace(in.Title)
	in.Author = strings.TrimSpace(in.Author)
	in.Genre = strings.TrimSpace(in.Genre)
	in.ISBN = normalizeISBN(in.ISBN)
	if creating && in.Copies == 0 {
		in.Copies = 1
	}

	errs := fieldErrors{}
	errs.check(in.Title != "", "title", "El título es obligatorio.")
	errs.check(length(in.Title) <= 200, "title", "Máximo 200 caracteres.")
	errs.check(in.Author != "", "author", "El autor es obligatorio.")
	errs.check(length(in.Author) <= 120, "author", "Máximo 120 caracteres.")
	errs.check(length(in.Genre) <= 60, "genre", "Máximo 60 caracteres.")
	errs.check(in.Copies >= 0 && in.Copies <= 1000, "copies", "Debe estar entre 0 y 1000.")
	if in.Year != 0 {
		maxYear := time.Now().Year() + 1
		errs.check(in.Year >= 1000 && in.Year <= maxYear, "year", fmt.Sprintf("Debe estar entre 1000 y %d.", maxYear))
	}
	if in.ISBN != "" {
		errs.check(validISBN(in.ISBN), "isbn", "ISBN no válido: deben ser 10 o 13 dígitos con el dígito de control correcto.")
	}
	return errs
}

// validateMember limpia y valida los datos de un socio.
func validateMember(in *store.MemberInput) fieldErrors {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))

	errs := fieldErrors{}
	errs.check(length(in.Name) >= 2, "name", "El nombre debe tener al menos 2 caracteres.")
	errs.check(length(in.Name) <= 100, "name", "Máximo 100 caracteres.")
	addr, err := mail.ParseAddress(in.Email)
	errs.check(err == nil && addr.Address == in.Email && len(in.Email) <= 254, "email", "Correo no válido.")
	return errs
}

// normalizeISBN quita guiones y espacios, y pasa la x final a mayúscula.
func normalizeISBN(isbn string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(isbn)))
}

// validISBN comprueba el dígito de control de un ISBN-10 o ISBN-13 ya normalizado.
func validISBN(isbn string) bool {
	switch len(isbn) {
	case 10:
		// ISBN-10: suma de cada dígito por su peso (10, 9, ..., 1) divisible entre 11. La X vale 10.
		sum := 0
		for i, c := range isbn {
			var digit int
			switch {
			case c >= '0' && c <= '9':
				digit = int(c - '0')
			case c == 'X' && i == 9:
				digit = 10
			default:
				return false
			}
			sum += digit * (10 - i)
		}
		return sum%11 == 0
	case 13:
		// ISBN-13: pesos alternos 1 y 3; la suma debe ser divisible entre 10.
		sum := 0
		for i, c := range isbn {
			if c < '0' || c > '9' {
				return false
			}
			weight := 1
			if i%2 == 1 {
				weight = 3
			}
			sum += int(c-'0') * weight
		}
		return sum%10 == 0
	default:
		return false
	}
}
