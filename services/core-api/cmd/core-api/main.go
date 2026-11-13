// Command core-api runs the Concierge Engine modular monolith:
// tenancy, catalog, booking & quote engine, dispatch, payments and the
// unified conversation inbox — one binary, well-bounded modules
// (Master Spec §9: modular monolith first, extraction path open).
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"concierge-engine/services/core-api/internal/config"
	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/server"
	"concierge-engine/services/core-api/internal/store"
)

func main() {
	cfg := config.Load()

	// Default: in-memory stores (fast local dev, tests, demos).
	// CE_STORE=pg selects the Postgres implementation (db/migrations).
	var st store.Store
	switch cfg.StoreKind {
	case "pg":
		pg, err := store.NewPostgres(cfg.DatabaseURL)
		if err != nil {
			log.Fatalf("postgres: %v", err)
		}
		st = pg
		log.Println("store: postgres (RLS enforced)")
	default:
		st = store.NewMemory()
		log.Println("store: in-memory")
	}
	defer st.Close()

	svc := domain.NewServices(st, domain.Deps{
		PolicyURL: cfg.PolicyEngineURL,
		LedgerURL: cfg.LedgerAdapterURL,
		Now:       time.Now,
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.New(svc),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("core-api listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
