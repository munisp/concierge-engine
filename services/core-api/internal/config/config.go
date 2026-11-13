package config

import "os"

type Config struct {
	ListenAddr       string
	StoreKind        string // "memory" (default) | "pg"
	DatabaseURL      string
	PolicyEngineURL  string // Rust policy engine (guardrails)
	LedgerAdapterURL string // Rust TigerBeetle adapter
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
	}
}
