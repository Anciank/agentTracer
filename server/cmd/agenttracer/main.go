package main

import (
	"context"
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
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()
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
		n, err := st.BackfillGraphCache(ctx, 0)
		if err != nil {
			log.Fatalf("graph cache backfill: %v", err)
		}
		log.Printf("graph cache backfill wrote %d traces", n)
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

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}
