package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/gateway"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dsn := environment("HARNESS_GATEWAY_DB_DSN", "file:mss-boot-admin-local.db?_pragma=busy_timeout(5000)")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return errors.New("open Gateway persistence")
	}
	if err := store.VerifyAllSchema(db); err != nil {
		return errors.New("Gateway persistence is not ready")
	}
	persistence, err := store.New(db)
	if err != nil {
		return err
	}
	handler, err := gateway.NewHandler(gateway.Config{
		AllowedOrigin:  environment("HARNESS_GATEWAY_ALLOWED_ORIGIN", "http://127.0.0.1:8001"),
		ExternalOrigin: environment("HARNESS_GATEWAY_EXTERNAL_ORIGIN", "http://127.0.0.1:8082"),
	}, persistence, nil, nil)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              environment("HARNESS_GATEWAY_ADDR", "127.0.0.1:8082"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("Harness Gateway listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func environment(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
