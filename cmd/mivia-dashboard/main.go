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

	dashboardconfig "github.com/MiviaLabs/go-mivia/internal/dashboardapp/config"
	dashboardhttpapi "github.com/MiviaLabs/go-mivia/internal/dashboardapp/httpapi"
	dashboardproxy "github.com/MiviaLabs/go-mivia/internal/dashboardapp/proxy"
	"github.com/MiviaLabs/go-mivia/internal/platform/health"
	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/platform/logging"
)

const serviceName = "mivia-dashboard"

func main() {
	if err := run(); err != nil {
		logger := logging.New(serviceName)
		logger.Error("service stopped", slog.String("error_category", "startup"), slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := dashboardconfig.Load()
	if err != nil {
		return err
	}
	logger := logging.New(serviceName)
	server, err := newServer(cfg, logger)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("service listening", slog.String("addr", cfg.HTTPAddr))
		errCh <- server.ListenAndServe()
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-signalCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func newServer(cfg dashboardconfig.Config, logger *slog.Logger) (*http.Server, error) {
	upstream, err := cfg.UpstreamBaseURL()
	if err != nil {
		return nil, err
	}
	proxyHandler := dashboardproxy.New(upstream)
	checker := health.NewChecker(health.Check{
		Name:    "mivia_server",
		Fn:      dashboardproxy.ReadyCheck(upstream),
		Timeout: 2 * time.Second,
	})

	mux := http.NewServeMux()
	dashboardhttpapi.RegisterRoutes(mux, dashboardhttpapi.Options{
		Proxy:     proxyHandler,
		StaticDir: cfg.StaticDir,
	})
	mux.HandleFunc("GET /healthz", health.LivenessHandler)
	mux.Handle("GET /readyz", health.ReadinessHandler(checker, logger))

	handler := httpserver.Chain(
		mux,
		httpserver.RequestID,
		httpserver.Recover(logger),
		httpserver.Timeout(cfg.RequestTimeout),
		httpserver.MaxBytes(cfg.MaxRequestBytes),
	)
	return &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}, nil
}
