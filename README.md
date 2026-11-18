# Biblioteca API

API REST para gestionar una biblioteca: catálogo de libros, socios y préstamos con reglas de negocio reales (límite de préstamos, ejemplares disponibles, vencimientos y multas). Escrita en **Go** usando solo la librería estándar y **SQLite**, con tests, Docker y CI.

![CI](https://github.com/dylannkeys/biblioteca-api/actions/workflows/ci.yml/badge.svg)

> **De dónde viene:** es la reescritura de [Sistema-biblioteca](https://github.com/dylannkeys/Sistema-biblioteca), un proyecto mío del primer año. Aquel código abría una conexión nueva a la base de datos en cada consulta, dependía de un SQL Server local con la cadena de conexión escrita en el código, no validaba datos, no tenía tests y ni siquiera compilaba. Esta versión aplica lo que he aprendido desde entonces.

## Funcionalidades

- **Catálogo**: alta, edición y baja de libros con varios ejemplares, búsqueda por título, autor o ISBN, filtro de disponibles y paginación.
- **Socios**: alta con correo único (sin distinguir mayúsculas) y préstamos activos de cada uno.
- **Préstamos**: prestar, devolver y consultar por estado (`active`, `overdue`, `returned`).
- **Estadísticas**: totales, préstamos vencidos y los 5 libros más prestados.
- **Validación** de todos los datos de entrada, incluido el **dígito de control del ISBN-10 e ISBN-13**.
- **Errores en JSON** con un código estable para cada caso (también en rutas inexistentes o métodos no permitidos).

### Reglas de negocio

| Regla | Respuesta si se incumple |
| --- | --- |
| Un socio puede tener como máximo **3 préstamos** a la vez | `422 loan_limit_reached` |
| Solo se presta si quedan **ejemplares disponibles** | `422 no_copies_available` |
| Un socio con **préstamos vencidos** no puede pedir más | `422 member_has_overdue` |
| No se puede pedir dos veces **el mismo libro** a la vez | `422 already_borrowed` |
| Un préstamo dura **14 días**; cada día de retraso suma **$0,25** de multa | se calcula en `days_late` y `fine` |
| No se pueden dejar **menos ejemplares** que los prestados | `422 copies_below_loans` |
| Un libro con **historial de préstamos** no se borra | `409 book_has_loans` |

## Ejecutar

### Con Go (1.23 o superior)

```bash
git clone https://github.com/dylannkeys/biblioteca-api.git
cd biblioteca-api
SEED=1 go run ./cmd/api        # en PowerShell: $env:SEED=1; go run ./cmd/api
```

La API queda en `http://localhost:8080`. `SEED=1` carga 10 libros, 4 socios y algunos préstamos de ejemplo (uno de ellos vencido).

### Con Docker

```bash
docker build -t biblioteca-api .
docker run -p 8080:8080 -e SEED=1 -v biblioteca-data:/data biblioteca-api
```

### Variables de entorno

| Variable | Por defecto | Descripción |
| --- | --- | --- |
| `PORT` | `8080` | Puerto HTTP |
| `DB_PATH` | `biblioteca.db` | Archivo de SQLite (`:memory:` para una base temporal) |
| `SEED` | — | Con `1`, carga datos de ejemplo si la base está vacía |

## Endpoints

| Método | Ruta | Descripción |
| --- | --- | --- |
| `GET` | `/health` | Estado del servicio y de la base de datos |
| `GET` | `/stats` | Totales y libros más prestados |
| `GET` | `/books?q=&available=&page=&page_size=` | Listar y buscar libros |
| `POST` | `/books` | Crear libro |
| `GET` `PUT` `DELETE` | `/books/{id}` | Ver, reemplazar o borrar un libro |
| `GET` | `/members?q=` | Listar y buscar socios |
| `POST` | `/members` | Crear socio |
| `GET` | `/members/{id}` | Ver socio |
| `GET` | `/members/{id}/loans?status=` | Préstamos de un socio |
| `GET` | `/loans?status=&member_id=&book_id=` | Listar préstamos |
| `POST` | `/loans` | Prestar un libro |
| `GET` | `/loans/{id}` | Ver préstamo |
| `POST` | `/loans/{id}/return` | Devolver un libro |

Las listas responden con el formato `{"data": [...], "page": 1, "page_size": 20, "total": 42}`.

### Ejemplos

```bash
# Buscar libros disponibles
curl "localhost:8080/books?q=borges&available=true"

# Crear un socio
curl -X POST localhost:8080/members -d '{"name": "Ana Torres", "email": "ana@example.com"}'

# Prestar el libro 1 al socio 1
curl -X POST localhost:8080/loans -d '{"book_id": 1, "member_id": 1}'

# Devolverlo
curl -X POST localhost:8080/loans/1/return
```

Respuesta de un préstamo vencido:

```json
{
  "id": 3,
  "book_id": 2,
  "book_title": "Huasipungo",
  "member_id": 2,
  "member_name": "Luis Paredes",
  "loaned_at": "2026-10-29T18:38:21Z",
  "due_at": "2026-11-12T18:38:21Z",
  "returned_at": null,
  "status": "overdue",
  "days_late": 6,
  "fine": 1.5
}
```

Respuesta de error:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "Hay datos no válidos.",
    "fields": { "isbn": "ISBN no válido: deben ser 10 o 13 dígitos con el dígito de control correcto." }
  }
}
```

## Tests

```bash
go test -cover ./...
```

- **Capa de datos** (`internal/store`): cada regla de negocio con tests *table-driven*. Un **reloj simulado** permite avanzar el tiempo para probar vencimientos y multas sin esperar 14 días.
- **Capa HTTP** (`internal/api`): peticiones reales con `httptest` contra una base SQLite en memoria (validación, códigos de estado, flujo completo de préstamo y devolución).
- **CI** (GitHub Actions): formato con `gofmt`, `go vet`, tests con el **detector de carreras** (`-race`), construcción de la imagen Docker y una prueba de humo contra el contenedor.

## Estructura

```
biblioteca-api/
├── cmd/api/            # main: configuración, arranque y apagado ordenado
├── internal/
│   ├── api/            # HTTP: rutas, validación, JSON, middlewares (logs y recuperación de panics)
│   └── store/          # datos y reglas de negocio sobre SQLite
├── Dockerfile          # compilación en dos etapas, imagen final distroless sin root
└── .github/workflows/  # CI
```

## Decisiones técnicas

- **Solo librería estándar para HTTP.** Desde Go 1.22, `net/http` enruta por método y parámetros (`GET /books/{id}`), así que no hace falta un framework. Los logs usan `log/slog`.
- **SQLite con un driver en Go puro** (`modernc.org/sqlite`): no necesita compilador de C, el binario es estático y la imagen Docker final pesa unos 15 MB.
- **Una sola conexión a la base de datos.** SQLite admite un escritor a la vez; con una conexión las transacciones se serializan y no hay carreras al comprobar si quedan ejemplares. Para mucho tráfico concurrente cambiaría a PostgreSQL.
- **Reglas de negocio dentro de transacciones**: las comprobaciones y la inserción de un préstamo son atómicas.
- **Errores tipados** (`store.Error` con `Kind` y `Code`): la capa de datos no sabe nada de HTTP y la API traduce cada tipo a su código de estado (404, 409 o 422).
- **El reloj se inyecta** (`store.WithClock`) para que el tiempo sea controlable en los tests.
- **Apagado ordenado**: al recibir `SIGTERM` (por ejemplo, de Docker) termina las peticiones en curso antes de cerrar.

## Posibles mejoras

- Autenticación (por ejemplo, con API keys para el personal de la biblioteca).
- Reservas cuando no quedan ejemplares, con aviso al socio.
- Especificación OpenAPI y documentación interactiva.
- Migraciones versionadas del esquema.

## Licencia

[MIT](LICENSE)
