package config

import (
	"os"
	"strconv"
)

type Config struct {
	ListenAddr       string
	StoreKind        string // "memory" (default) | "pg"
	DatabaseURL      string
	PolicyEngineURL  string // Rust policy engine (guardrails)
	LedgerAdapterURL string // Rust TigerBeetle adapter
	MojaloopURL      string // Rust FSPIOP adapter (Phase 2 payouts)
	AvinodeURL       string // charter supply adapter; empty = disabled
	AvinodeKey       string
	FieldKeyB64      string // CE_FIELD_KEY: 32-byte base64, VIP field encryption
	TripSecret       string // CE_TRIP_SECRET: chauffeur magic-link HMAC
	PublicBase       string // public base URL for adapter callbacks
	StripeKey        string // CE_STRIPE_KEY: secret key; empty = sandbox links
	StripeBase       string // CE_STRIPE_BASE_URL override (tests/sandbox)
	StripeWHSec      string // CE_STRIPE_WEBHOOK_SECRET: webhook signature
	PaystackKey      string // CE_PAYSTACK_KEY: secret key; empty = disabled
	PaystackBase     string // CE_PAYSTACK_BASE_URL: override (tests)
	AmadeusBase      string // CE_AMADEUS_BASE_URL: default test.api.amadeus.com
	AmadeusID        string // CE_AMADEUS_CLIENT_ID; empty = mock GDS
	AmadeusSecret    string // CE_AMADEUS_CLIENT_SECRET
	AgentURL         string // CE_AGENT_URL: concierge-agent (case AI assist); empty = core rules only
	WhatsAppURL      string // CE_WHATSAPP_ADAPTER_URL: outbound channel for case replies; empty = draft-only
	EmailURL         string // CE_EMAIL_ADAPTER_URL: Phase 39 email channel adapter; empty = record-only
	OperatorPhone    string // CE_OPERATOR_PHONE: operator WhatsApp for reply-failure alerts; empty = events only
	GeocodeURL       string // CE_GEOCODE_URL: Nominatim-compatible geocoder; empty = built-in gazetteer
	GeocodeKey       string // CE_GEOCODE_API_KEY
	ConsoleBase      string // CE_CONSOLE_BASE: console SPA public origin (SEO CTA links); empty = same-origin
	// PayoutCallbackSecret (CE_PAYOUT_CALLBACK_SECRET) authenticates Mojaloop
	// adapter callbacks via X-Callback-Secret (Phase 48); empty = dev mode.
	PayoutCallbackSecret string
	// BlobAdapterURL (CE_BLOB_ADAPTER_URL) wires Apache OpenDAL media
	// storage via the blob-adapter sidecar (Phase 49). Empty = legacy
	// inline store (postgres bytea / memory).
	BlobAdapterURL string
	// CE_WEBHOOK_ROTATION_WINDOW_HOURS: dual-secret signing window after a
	// webhook secret rotation; default 24 (domain.WebhookRotationWindow).
	WebhookRotationWindowHours int
	// Phase 50: edge rate limits (requests/sec + burst per bucket).
	// CE_RATE_LIMIT=off disables the limiter entirely (load tests, dev).
	RatePublicRPS   float64 // CE_RATE_PUBLIC_RPS, default 30 (per source IP, anonymous)
	RatePublicBurst int     // CE_RATE_PUBLIC_BURST, default 60
	RateTenantRPS   float64 // CE_RATE_TENANT_RPS, default 200 (per tenant)
	RateTenantBurst int     // CE_RATE_TENANT_BURST, default 400
	RateLimitOff    bool    // CE_RATE_LIMIT=off
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func Load() Config {
	return Config{
		ListenAddr:                 getenv("CE_LISTEN_ADDR", ":8000"),
		StoreKind:                  getenv("CE_STORE", "memory"),
		DatabaseURL:                getenv("DATABASE_URL", "postgres://concierge:concierge@localhost:5432/concierge?sslmode=disable"),
		PolicyEngineURL:            getenv("CE_POLICY_ENGINE_URL", "http://localhost:8200"),
		LedgerAdapterURL:           getenv("CE_LEDGER_ADAPTER_URL", "http://localhost:8210"),
		MojaloopURL:                getenv("CE_MOJALOOP_ADAPTER_URL", "http://localhost:8220"),
		AvinodeURL:                 getenv("CE_AVINODE_BASE_URL", ""),
		AvinodeKey:                 getenv("CE_AVINODE_API_KEY", ""),
		FieldKeyB64:                getenv("CE_FIELD_KEY", ""),
		TripSecret:                 getenv("CE_TRIP_SECRET", ""),
		PublicBase:                 getenv("CE_PUBLIC_BASE_URL", "http://localhost:8000"),
		StripeKey:                  getenv("CE_STRIPE_KEY", ""),
		StripeBase:                 getenv("CE_STRIPE_BASE_URL", ""),
		StripeWHSec:                getenv("CE_STRIPE_WEBHOOK_SECRET", ""),
		PaystackKey:                getenv("CE_PAYSTACK_KEY", ""),
		PaystackBase:               getenv("CE_PAYSTACK_BASE_URL", ""),
		AmadeusBase:                getenv("CE_AMADEUS_BASE_URL", ""),
		AmadeusID:                  getenv("CE_AMADEUS_CLIENT_ID", ""),
		AmadeusSecret:              getenv("CE_AMADEUS_CLIENT_SECRET", ""),
		AgentURL:                   getenv("CE_AGENT_URL", "http://localhost:8100"),
		WhatsAppURL:                getenv("CE_WHATSAPP_ADAPTER_URL", ""),
		EmailURL:                   getenv("CE_EMAIL_ADAPTER_URL", ""),
		OperatorPhone:              getenv("CE_OPERATOR_PHONE", ""),
		GeocodeURL:                 getenv("CE_GEOCODE_URL", ""),
		GeocodeKey:                 getenv("CE_GEOCODE_API_KEY", ""),
		ConsoleBase:                getenv("CE_CONSOLE_BASE", ""),
		PayoutCallbackSecret:       getenv("CE_PAYOUT_CALLBACK_SECRET", ""),
		BlobAdapterURL:             getenv("CE_BLOB_ADAPTER_URL", ""),
		WebhookRotationWindowHours: getenvInt("CE_WEBHOOK_ROTATION_WINDOW_HOURS", 24),
		RatePublicRPS:              getenvFloat("CE_RATE_PUBLIC_RPS", 30),
		RatePublicBurst:            getenvInt("CE_RATE_PUBLIC_BURST", 60),
		RateTenantRPS:              getenvFloat("CE_RATE_TENANT_RPS", 200),
		RateTenantBurst:            getenvInt("CE_RATE_TENANT_BURST", 400),
		RateLimitOff:               getenv("CE_RATE_LIMIT", "") == "off",
	}
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func getenvFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			return n
		}
	}
	return def
}
