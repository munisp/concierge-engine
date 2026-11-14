package config

import "os"

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
	AmadeusBase      string // CE_AMADEUS_BASE_URL: default test.api.amadeus.com
	AmadeusID        string // CE_AMADEUS_CLIENT_ID; empty = mock GDS
	AmadeusSecret    string // CE_AMADEUS_CLIENT_SECRET
	AgentURL         string // CE_AGENT_URL: concierge-agent (case AI assist); empty = core rules only
	WhatsAppURL      string // CE_WHATSAPP_ADAPTER_URL: outbound channel for case replies; empty = draft-only
	OperatorPhone    string // CE_OPERATOR_PHONE: operator WhatsApp for reply-failure alerts; empty = events only
	GeocodeURL       string // CE_GEOCODE_URL: Nominatim-compatible geocoder; empty = built-in gazetteer
	GeocodeKey       string // CE_GEOCODE_API_KEY
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func Load() Config {
	return Config{
		ListenAddr:       getenv("CE_LISTEN_ADDR", ":8000"),
		StoreKind:        getenv("CE_STORE", "memory"),
		DatabaseURL:      getenv("DATABASE_URL", "postgres://concierge:concierge@localhost:5432/concierge?sslmode=disable"),
		PolicyEngineURL:  getenv("CE_POLICY_ENGINE_URL", "http://localhost:8200"),
		LedgerAdapterURL: getenv("CE_LEDGER_ADAPTER_URL", "http://localhost:8210"),
		MojaloopURL:      getenv("CE_MOJALOOP_ADAPTER_URL", "http://localhost:8220"),
		AvinodeURL:       getenv("CE_AVINODE_BASE_URL", ""),
		AvinodeKey:       getenv("CE_AVINODE_API_KEY", ""),
		FieldKeyB64:      getenv("CE_FIELD_KEY", ""),
		TripSecret:       getenv("CE_TRIP_SECRET", ""),
		PublicBase:       getenv("CE_PUBLIC_BASE_URL", "http://localhost:8000"),
		StripeKey:        getenv("CE_STRIPE_KEY", ""),
		StripeBase:       getenv("CE_STRIPE_BASE_URL", ""),
		StripeWHSec:      getenv("CE_STRIPE_WEBHOOK_SECRET", ""),
		AmadeusBase:      getenv("CE_AMADEUS_BASE_URL", ""),
		AmadeusID:        getenv("CE_AMADEUS_CLIENT_ID", ""),
		AmadeusSecret:    getenv("CE_AMADEUS_CLIENT_SECRET", ""),
		AgentURL:         getenv("CE_AGENT_URL", "http://localhost:8100"),
		WhatsAppURL:      getenv("CE_WHATSAPP_ADAPTER_URL", ""),
		OperatorPhone:    getenv("CE_OPERATOR_PHONE", ""),
		GeocodeURL:       getenv("CE_GEOCODE_URL", ""),
		GeocodeKey:       getenv("CE_GEOCODE_API_KEY", ""),
	}
}
