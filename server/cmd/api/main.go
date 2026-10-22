package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	dbmigrations "github.com/shangyue/font-label/server/db"
	"github.com/shangyue/font-label/server/internal/app"
	"github.com/shangyue/font-label/server/internal/platform/config"
	"github.com/shangyue/font-label/server/internal/platform/db"
	"github.com/shangyue/font-label/server/internal/platform/storage"
)

func main() {
	cfg := config.Load()
	level := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	if err := run(cfg); err != nil {
		slog.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := migrate(cfg.DatabaseURL); err != nil {
		return err
	}

	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	store, err := storage.New(cfg.StorageRoot)
	if err != nil {
		return err
	}

	server := app.New(cfg, database, store)
	if err := server.EnsureSeedAdmin(ctx); err != nil {
		return err
	}
	if cfg.WorkerInAPI {
		server.StartWorker(ctx)
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("listening", "addr", cfg.HTTPAddr, "storage", store.Root())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func migrate(dsn string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	goose.SetBaseFS(dbmigrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.Up(sqlDB, dbmigrations.Dir); err != nil {
		return err
	}
	slog.Info("migrations applied")
	return nil
}
