# --- Etapa 1: compilar ---
FROM golang:1.23-alpine AS build
WORKDIR /src

# Descarga las dependencias primero para aprovechar la caché de Docker.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Binario estático (sin cgo): funciona en una imagen mínima sin librerías del sistema.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
    && mkdir -p /out/data

# --- Etapa 2: imagen final (unos 15 MB, sin shell y sin usuario root) ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
COPY --from=build --chown=nonroot:nonroot /out/data /data

ENV PORT=8080 \
    DB_PATH=/data/biblioteca.db
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
