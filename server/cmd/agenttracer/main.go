package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Anciank/agentTracer/server/internal/api"
	"github.com/Anciank/agentTracer/server/internal/config"
	"github.com/Anciank/agentTracer/server/internal/store"
	"github.com/Anciank/agentTracer/server/migrations"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "archive" {
		os.Exit(runArchive(os.Args[2:]))
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	for i := 0; i < 30; i++ {
		if err := pool.Ping(ctx); err == nil {
			break
		} else if i == 29 {
			log.Fatalf("db ping: %v", err)
		}
		time.Sleep(time.Second)
	}

	if err := migrations.Up(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	blob := store.NewS3Blob(cfg)
	for i := 0; i < 30; i++ {
		if err := blob.EnsureBucket(ctx); err == nil {
			break
		} else if i == 29 {
			log.Printf("warn: minio bucket: %v (continuing)", err)
		}
		time.Sleep(time.Second)
	}

	st := store.New(pool, blob)
	if os.Getenv("AGENTTRACER_BACKFILL_GRAPH_CACHE") == "1" {
		wrote, failed, err := st.BackfillGraphCache(ctx, 0)
		if err != nil {
			log.Printf("graph cache backfill: list error: %v (continuing)", err)
		} else {
			log.Printf("graph cache backfill wrote %d traces (%d failed)", wrote, failed)
		}
	}
	if cfg.ColdEnabled {
		log.Printf("cold archiver enabled (hot_retention=%s interval=%s batch=%d); no TTL delete",
			cfg.HotRetention, cfg.ColdArchiveInterval, cfg.ColdArchiveBatch)
		st.StartColdArchiver(ctx, cfg.HotRetention, time.Hour, cfg.ColdArchiveInterval, cfg.ColdArchiveBatch)
	}

	srv := api.New(cfg, st)
	handler := api.LogRequests(srv.Handler())

	httpSrv := &http.Server{Addr: cfg.ListenAddr, Handler: handler}
	go func() {
		log.Printf("agenttracer listening on %s", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func runArchive(args []string) int {
	fs := flag.NewFlagSet("archive", flag.ExitOnError)
	older := fs.Duration("older-than", 0, "archive roots with end_time older than this (0 = any finished)")
	guard := fs.Duration("ingest-guard", time.Hour, "skip traces with any run updated_at within this window (0 disables)")
	limit := fs.Int("limit", 50, "max traces per batch")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		log.Printf("config: %v", err)
		return 1
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Printf("db: %v", err)
		return 1
	}
	defer pool.Close()
	if err := migrations.Up(ctx, pool); err != nil {
		log.Printf("migrate: %v", err)
		return 1
	}
	blob := store.NewS3Blob(cfg)
	_ = blob.EnsureBucket(ctx)
	st := store.New(pool, blob)
	n, err := st.ArchiveBatch(ctx, store.ColdArchiveOpts{
		OlderThan: *older, IngestGuard: *guard, Limit: *limit,
	})
	if err != nil {
		log.Printf("archive: %v", err)
		return 1
	}
	fmt.Printf("archived %d traces\n", n)
	return 0
}
