package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

// Server wraps http.Server with graceful shutdown support.
type Server struct {
	httpServer *http.Server
	log        *slog.Logger
}

// New creates a Server with the provided handler and timeouts.
func New(handler http.Handler, port int, readTimeout, writeTimeout time.Duration, log *slog.Logger) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      handler,
			ReadTimeout:  readTimeout,
			WriteTimeout: writeTimeout,
		},
		log: log,
	}
}

// Run starts the server and blocks until SIGINT or SIGTERM is received,
// then performs a graceful shutdown within shutdownTimeout.
func (s *Server) Run(shutdownTimeout time.Duration) error {
	// Create a context that cancels on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("server: listen on %s: %w", s.httpServer.Addr, err)
	}
	s.log.Info("server listening", "addr", ln.Addr().String())

	// Start serving in background
	serveErr := make(chan error, 1)
	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
		close(serveErr)
	}()

	// Wait for shutdown signal or serve error
	select {
	case <-ctx.Done():
		s.log.Info("shutdown signal received, draining connections...")
	case err := <-serveErr:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server: graceful shutdown failed: %w", err)
	}

	s.log.Info("server shutdown complete")
	return nil
}
