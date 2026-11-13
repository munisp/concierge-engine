SHELL := /bin/bash

.PHONY: dev-infra migrate run-core run-policy run-ledger run-ai run-wa run-web test build

dev-infra:
	docker compose up -d postgres redis kafka temporal keycloak tigerbeetle opensearch

migrate:
	psql "$${DATABASE_URL:-postgres://concierge:concierge@localhost:5432/concierge?sslmode=disable}" -f db/migrations/0001_init.sql

run-core:
	cd services/core-api && go run ./cmd/core-api

run-policy:
	cd crates/policy-engine && cargo run

run-ledger:
	cd crates/ledger-adapter && cargo run

run-ai:
	cd ai/concierge-agent && uvicorn concierge_agent.main:app --port 8100 --reload

run-wa:
	cd ai/whatsapp-adapter && uvicorn whatsapp_adapter.main:app --port 8110 --reload

run-web:
	cd web/console && npm install && npm run dev

test:
	cd services/core-api && go test ./...
	cd crates && cargo test --workspace
	cd ai/concierge-agent && python -m pytest -q
	cd ai/whatsapp-adapter && python -m pytest -q

build:
	cd services/core-api && go build ./...
	cd crates && cargo build --workspace --release
	cd web/console && npm ci && npm run build
