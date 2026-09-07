package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
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
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	driver := environment("HARNESS_GATEWAY_DB_DRIVER", "sqlite")
	dsn := strings.TrimSpace(os.Getenv("HARNESS_GATEWAY_DB_DSN"))
	if dsn == "" && strings.EqualFold(strings.TrimSpace(driver), "sqlite") {
		dsn = "file:mss-boot-admin-local.db?_pragma=busy_timeout(5000)"
	}
	dialector, err := gatewayDatabaseDialector(driver, dsn)
	if err != nil {
		return err
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return errors.New("open Gateway persistence")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return errors.New("own Gateway persistence pool")
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	defer sqlDB.Close()
	if err := store.VerifyAllSchema(db); err != nil {
		return errors.New("Gateway persistence is not ready")
	}
	persistence, err := store.New(db)
	if err != nil {
		return err
	}
	trust, err := gateway.LoadOrCreateTrustState(
		environment("HARNESS_GATEWAY_TRUST_FILE", ".mss/run/private/gateway-trust.json"),
		rand.Reader,
		time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	handler, err := gateway.NewHandler(gateway.Config{
		AllowedOrigin:        environment("HARNESS_GATEWAY_ALLOWED_ORIGIN", "http://127.0.0.1:8001"),
		ExternalOrigin:       environment("HARNESS_GATEWAY_EXTERNAL_ORIGIN", "http://127.0.0.1:8082"),
		NativeExternalOrigin: environment("HARNESS_GATEWAY_NATIVE_EXTERNAL_ORIGIN", "http://127.0.0.1:8082"),
		VerificationURI:      environment("HARNESS_GATEWAY_VERIFICATION_URI", "http://127.0.0.1:8001/harness/enrollments"),
		Trust:                trust,
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

func gatewayDatabaseDialector(driver, dsn string) (gorm.Dialector, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("Gateway persistence DSN is required")
	}
	switch driver {
	case "sqlite":
		return sqlite.Open(dsn), nil
	case "postgres":
		return postgres.Open(dsn), nil
	default:
		return nil, fmt.Errorf("Gateway persistence driver %q is not supported", driver)
	}
}

func environment(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
