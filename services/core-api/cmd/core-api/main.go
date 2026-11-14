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
		PolicyURL:     cfg.PolicyEngineURL,
		LedgerURL:     cfg.LedgerAdapterURL,
		MojaloopURL:   cfg.MojaloopURL,
		AvinodeURL:    cfg.AvinodeURL,
		AvinodeKey:    cfg.AvinodeKey,
		FieldKeyB64:   cfg.FieldKeyB64,
		TripSecret:    cfg.TripSecret,
		PublicBase:    cfg.PublicBase,
		StripeKey:     cfg.StripeKey,
		StripeBase:    cfg.StripeBase,
		StripeWHSec:   cfg.StripeWHSec,
		AmadeusBase:   cfg.AmadeusBase,
		AmadeusID:     cfg.AmadeusID,
		AmadeusSecret: cfg.AmadeusSecret,
		AgentURL:      cfg.AgentURL,
		WhatsAppURL:   cfg.WhatsAppURL,
		GeocodeURL:    cfg.GeocodeURL,
		GeocodeKey:    cfg.GeocodeKey,
		Now:           time.Now,
	})
	if cfg.FieldKeyB64 == "" {
		log.Println("WARNING: CE_FIELD_KEY not set — VIP field-level encryption disabled (dev only)")
	}
	if cfg.TripSecret == "" {
		log.Println("WARNING: CE_TRIP_SECRET not set — using dev chauffeur-link secret")
	}
	if cfg.StripeKey == "" {
		log.Println("WARNING: CE_STRIPE_KEY not set — payment links use the deterministic sandbox URL")
	}
	if cfg.StripeKey != "" && cfg.StripeWHSec == "" {
		log.Println("WARNING: CE_STRIPE_WEBHOOK_SECRET not set — Stripe webhooks will all be rejected")
	}
	if cfg.AmadeusID == "" {
		log.Println("GDS: no Amadeus credentials — deterministic mock supply (dev/CI)")
	}

	// Phase 3: background webhook dispatcher (at-least-once delivery,
	// exponential backoff). Runs in-process; the outbox stays the durable spine.
	dispatchCtx, stopDispatch := context.WithCancel(context.Background())
	defer stopDispatch()
	go svc.Webhooks.RunDispatcher(dispatchCtx, 15*time.Second)

	// Phase 4: background case-SLA sweeper — every minute, escalate overdue
	// open cases per tenant (Taylor's SLA promise is operational, not manual).
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-dispatchCtx.Done():
				return
			case <-ticker.C:
				tenants, err := svc.Tenants.List(dispatchCtx)
				if err != nil {
					continue
				}
				for _, t := range tenants {
					if n, err := svc.Cases.ScanSLA(dispatchCtx, t.ID); err == nil && n > 0 {
						log.Printf("case SLA sweep: tenant %s — %d escalated", t.Slug, n)
					}
				}
			}
		}
	}()

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
