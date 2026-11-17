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

	"concierge-engine/services/core-api/internal/blobstore"
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
	// Phase 49: Apache OpenDAL media storage. When CE_BLOB_ADAPTER_URL is
	// set, media bytes route through the blob-adapter (OpenDAL: fs/s3/gcs/
	// azblob/...) and rows keep metadata + storage_key; legacy inline rows
	// keep serving and POST /v1/media/backfill migrates them.
	storageEngine := "memory"
	if cfg.StoreKind == "pg" {
		storageEngine = "postgres-bytea"
	}
	if cfg.BlobAdapterURL != "" {
		st = store.WithBlobMedia(st, blobstore.New(cfg.BlobAdapterURL))
		storageEngine = "opendal"
		log.Println("media storage: apache-opendal via blob-adapter", cfg.BlobAdapterURL)
	}
	defer st.Close()

	if cfg.WebhookRotationWindowHours > 0 {
		domain.WebhookRotationWindow = time.Duration(cfg.WebhookRotationWindowHours) * time.Hour
	}
	svc := domain.NewServices(st, domain.Deps{
		PolicyURL:            cfg.PolicyEngineURL,
		LedgerURL:            cfg.LedgerAdapterURL,
		MojaloopURL:          cfg.MojaloopURL,
		AvinodeURL:           cfg.AvinodeURL,
		AvinodeKey:           cfg.AvinodeKey,
		FieldKeyB64:          cfg.FieldKeyB64,
		TripSecret:           cfg.TripSecret,
		PublicBase:           cfg.PublicBase,
		StripeKey:            cfg.StripeKey,
		StripeBase:           cfg.StripeBase,
		StripeWHSec:          cfg.StripeWHSec,
		PaystackKey:          cfg.PaystackKey,
		PaystackBase:         cfg.PaystackBase,
		AmadeusBase:          cfg.AmadeusBase,
		AmadeusID:            cfg.AmadeusID,
		AmadeusSecret:        cfg.AmadeusSecret,
		AgentURL:             cfg.AgentURL,
		WhatsAppURL:          cfg.WhatsAppURL,
		EmailURL:             cfg.EmailURL,
		OperatorPhone:        cfg.OperatorPhone,
		GeocodeURL:           cfg.GeocodeURL,
		GeocodeKey:           cfg.GeocodeKey,
		ConsoleBase:          cfg.ConsoleBase,
		PayoutCallbackSecret: cfg.PayoutCallbackSecret,
		StorageEngine:        storageEngine,
		Now:                  time.Now,
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

	// Phase 25: background lifecycle sweeper — hourly per tenant, sends
	// post-trip follow-ups and consent-gated win-backs through the outreach
	// engine. Idempotency keys make hourly re-runs safe; the config's
	// Disabled flag or per-tenant cadence decide what fires.
	go func() {
		ticker := time.NewTicker(time.Hour)
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
					res, err := svc.FollowUps.Run(dispatchCtx, t.ID)
					if err != nil {
						log.Printf("lifecycle sweep: tenant %s — %v", t.Slug, err)
						continue
					}
					if res.PostTripSent+res.WinBackSent+res.WinBackHeld > 0 {
						log.Printf("lifecycle sweep: tenant %s — %d post-trip, %d win-back sent, %d held",
							t.Slug, res.PostTripSent, res.WinBackSent, res.WinBackHeld)
					}
					// Phase 35: deliver due scheduled sends (quiet-hours
					// deferrals, send-time optimization, drip steps).
					if n, err := svc.Outreach.RunDue(dispatchCtx, t.ID); err == nil && n > 0 {
						log.Printf("scheduled-send sweep: tenant %s — %d due", t.Slug, n)
					}
					// Phase 35 (O3): advance drip sequences — per-client
					// progress derived from the sends ledger.
					if res, err := svc.Sequences.Run(dispatchCtx, t.ID); err == nil && res.Sent+res.Held > 0 {
						log.Printf("sequence sweep: tenant %s — %d sent, %d held", t.Slug, res.Sent, res.Held)
					}
					// Phase 36: speed-to-lead SLA — page the operator roster
					// for NEW leads past the response window (once per lead).
					if n, err := svc.Leads.SweepSLA(dispatchCtx, t.ID); err == nil && n > 0 {
						log.Printf("lead SLA sweep: tenant %s — %d alerted", t.Slug, n)
					}
					// Phase 26: weekly acquisition pacing alert (Mondays
					// 09:00 UTC inside this hourly sweep — fires once).
					svc.Acquisition.PacingAlertIfDue(dispatchCtx, t.ID)
					// Phase 46: retention sweep — purge aged geo stamps,
					// scrub aged closed-conversation message bodies, purge
					// aged attribution events per the tenant's config.
					if res, err := svc.Retention.Run(dispatchCtx, t.ID); err != nil {
						log.Printf("retention sweep: tenant %s — %v", t.Slug, err)
					} else if res.GeoStampsPurged+res.MessageBodiesScrubbed+res.AttributionPurged > 0 {
						log.Printf("retention sweep: tenant %s — %d geo, %d bodies, %d attribution",
							t.Slug, res.GeoStampsPurged, res.MessageBodiesScrubbed, res.AttributionPurged)
					}
					// Phase 47: expire stale undecided KYC cases (document
					// freshness — a case never finished must not be
					// decidible forever).
					if n, err := svc.Kyc.ExpireStale(dispatchCtx, t.ID); err != nil {
						log.Printf("kyc expiry sweep: tenant %s — %v", t.Slug, err)
					} else if n > 0 {
						log.Printf("kyc expiry sweep: tenant %s — %d cases expired", t.Slug, n)
					}
					// Phase 48: settlement-timeout recovery — adapter
					// callbacks are fire-and-forget, so the core recovers
					// stale IN_FLIGHT payouts and batches on its own.
					if n, err := svc.Payouts.SweepTimeouts(dispatchCtx, t.ID); err != nil {
						log.Printf("payout timeout sweep: tenant %s — %v", t.Slug, err)
					} else if n > 0 {
						log.Printf("payout timeout sweep: tenant %s — %d compensated", t.Slug, n)
					}
					if n, err := svc.Settlements.SweepBatchTimeouts(dispatchCtx, t.ID); err != nil {
						log.Printf("batch timeout sweep: tenant %s — %v", t.Slug, err)
					} else if n > 0 {
						log.Printf("batch timeout sweep: tenant %s — %d compensated", t.Slug, n)
					}
				}
			}
		}
	}()

	// Phase 50: edge security — security headers on every response, tiered
	// rate limiting (per-IP anonymous / per-tenant), env-tunable.
	var srvOpts []server.Option
	if cfg.RateLimitOff {
		srvOpts = append(srvOpts, server.WithRateLimitDisabled())
	} else {
		srvOpts = append(srvOpts, server.WithRateLimits(
			cfg.RatePublicRPS, cfg.RatePublicBurst, cfg.RateTenantRPS, cfg.RateTenantBurst))
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.New(svc, srvOpts...),
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
