package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"llm-refund/internal/httpapi"
	"llm-refund/internal/refundworker"
	"llm-refund/internal/store"
)

func main() {
	creditEmail := flag.String("credit-email", "", "credit an existing user, then exit")
	creditCents := flag.Int64("credit-cents", 0, "credit amount in hundredths of a point")
	creditNote := flag.String("credit-note", "人工入账", "ledger note")
	flag.Parse()
	if err := run(*creditEmail, *creditCents, *creditNote); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
func run(email string, cents int64, note string) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	master, err := base64.StdEncoding.DecodeString(os.Getenv("MASTER_KEY"))
	if err != nil || len(master) != 32 {
		return errors.New("MASTER_KEY must contain 32 base64-encoded bytes; keep it with database backups")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := store.Open(ctx, dsn, master)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	if err = db.Bootstrap(ctx, os.Getenv("BOOTSTRAP_EMAIL"), os.Getenv("BOOTSTRAP_PASSWORD"), os.Getenv("BOOTSTRAP_NAME")); err != nil {
		return err
	}
	if email != "" {
		return db.Credit(ctx, email, cents, note)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "18740"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("invalid PORT")
	}
	host := os.Getenv("LISTEN_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	server := &http.Server{Addr: host + ":" + port, Handler: httpapi.New(db, os.Getenv("COOKIE_SECURE") == "true"), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	workerDone := make(chan struct{})
	riskDone := make(chan struct{})
	go func() { defer close(riskDone); db.RunRiskMonitor(workerCtx) }()
	runner := refundworker.DefaultRunner()
	go func() { defer close(workerDone); (&refundworker.Worker{DB: db, Runner: runner}).Run(workerCtx) }()
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopped)
	go func() {
		<-stopped
		stopWorker()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(c)
	}()
	slog.Info("HUIXU workspace ready", "address", server.Addr, "database", "PostgreSQL", "refund_provider", "merge_v2")
	err = server.ListenAndServe()
	stopWorker()
	<-workerDone
	<-riskDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
