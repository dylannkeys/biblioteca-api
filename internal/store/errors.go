package store

// Kind clasifica los errores esperables para que la API elija el código HTTP.
type Kind int

const (
	KindNotFound Kind = iota + 1 // el recurso no existe
	KindConflict                 // choca con datos existentes (ISBN o correo repetido...)
	KindRule                     // incumple una regla de negocio (límite de préstamos...)
)

// Error es un error esperable con un código estable y un mensaje para el usuario.
type Error struct {
	Kind    Kind
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func notFound(message string) error {
	return &Error{Kind: KindNotFound, Code: "not_found", Message: message}
}

func conflict(code, message string) error {
	return &Error{Kind: KindConflict, Code: code, Message: message}
}

func ruleViolation(code, message string) error {
	return &Error{Kind: KindRule, Code: code, Message: message}
}
