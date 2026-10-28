package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
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

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		log.Fatalf("migration list: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 4 && e.Name()[len(e.Name())-4:] == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		sqlBytes, err := migrations.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("migration %s: %v", name, err)
		}
		if err := store.Migrate(ctx, pool, string(sqlBytes)); err != nil {
			log.Fatalf("migrate %s: %v", name, err)
		}
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
