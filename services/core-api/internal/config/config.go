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
	}
}
