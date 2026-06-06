// Package server provides the HTTP server lifecycle management for the
// Ingestion Mesh webhook and API endpoints.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/decisionstack/ingestion/internal/config"
)

// Server wraps an HTTP server with lifecycle management.
type Server struct {
	http   *http.Server
	log    *slog.Logger
	router *chi.Mux
}

// NewServer creates a new Server with the given configuration and dependencies.
func NewServer(cfg *config.Config, deps *Dependencies) *Server {
	router := NewRouter(cfg, deps)

	addr := fmt.Sprintf("%s:%s", cfg.ServerHost, cfg.ServerPort)

	return &Server{
		http: &http.Server{
			Addr:         addr,
			Handler:      router,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			// IdleTimeout:  cfg.ReadTimeout,
		},
		log:    deps.Log,
		router: router,
	}
}

// Start begins listening and serving HTTP requests.
// It blocks until the server is stopped.
func (s *Server) Start() error {
	s.log.Info("starting http server", slog.String("addr", s.http.Addr))
	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// Stop performs a graceful shutdown of the HTTP server.
// It uses the provided context for the shutdown timeout.
func (s *Server) Stop(ctx context.Context) error {
	s.log.Info("stopping http server gracefully")

	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("http server shutdown: %w", err)
	}

	s.log.Info("http server stopped")
	return nil
}

// Run starts the server and listens for shutdown signals.
// It blocks until a SIGTERM or SIGINT is received, then performs graceful shutdown.
func (s *Server) Run() error {
	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		if err := s.Start(); err != nil {
			errChan <- err
		}
	}()

	// Wait for signal or error
	select {
	case sig := <-sigChan:
		s.log.Info("received shutdown signal", slog.String("signal", sig.String()))
	case err := <-errChan:
		return err
	}

	// Graceful shutdown with 30s timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return s.Stop(shutdownCtx)
}

// Router returns the underlying chi router (useful for testing).
func (s *Server) Router() *chi.Mux {
	return s.router
}
