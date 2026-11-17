package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"productor-api/internal/api"
	"productor-api/internal/config"
	"productor-api/internal/database"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if cfg.AppEnv == "production" {
		log.Fatal("production startup is disabled until a real SMS sender is configured")
	}
	if cfg.OTPMode != "development" {
		log.Fatal("this build only supports OTP_MODE=development; configure a real SMS sender before production")
	}
	startup, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	pool, err := database.Open(startup, cfg.DatabaseURL)
	cancelStartup()
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	if cfg.AppEnv != "production" {
		migrationCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		if err := database.Migrate(migrationCtx, pool); err != nil {
			cancel()
			log.Fatal(err)
		}
		cancel()
	}
	if err := os.MkdirAll(cfg.UploadDir, 0750); err != nil {
		log.Fatalf("create upload directory: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := api.New(pool, cfg, logger)
	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdown, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	handler.StartMaintenance(shutdown)
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("Productor API listening", "address", "http://localhost"+server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	case <-shutdown.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}
}
