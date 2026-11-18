// Comando api arranca el servidor HTTP de la biblioteca.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dylannkeys/biblioteca-api/internal/api"
	"github.com/dylannkeys/biblioteca-api/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("el servidor se detuvo con un error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	port := getenv("PORT", "8080")
	dbPath := getenv("DB_PATH", "biblioteca.db")

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// Con SEED=1 se carga un catálogo de ejemplo si la base está vacía.
	if os.Getenv("SEED") == "1" {
		seeded, err := st.SeedIfEmpty(context.Background())
		if err != nil {
			return err
		}
		if seeded {
			logger.Info("datos de ejemplo cargados")
		}
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           api.New(st, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Apagado ordenado: al recibir Ctrl+C o SIGTERM (Docker), termina las peticiones en curso.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("API escuchando", "addr", "http://localhost:"+port, "db", dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("apagando el servidor…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
