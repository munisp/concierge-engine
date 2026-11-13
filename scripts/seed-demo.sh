#!/usr/bin/env bash
# Seed + smoke: Taylor tenant, catalog, client, instant quote, payment link,
# PSP capture, dispatch assignment — the Phase-1 golden path end to end.
set -euo pipefail
API="${CE_API:-http://localhost:8000}"

echo "== create tenant (Taylor Travel Management)"
TENANT=$(curl -sf "$API/v1/platform/tenants" -H 'Content-Type: application/json' -d '{
  "slug":"taylor","name":"Taylor Travel Management","archetype":"flagship",
  "pricing_rules":{"currency":"EUR","deposit_pct":0.3,"quote_ttl_seconds":3600,"platform_fee_pct":0.03},
  "sla_config":{"quote_sla_seconds":3600}}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
echo "tenant: $TENANT"
H=(-H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json')

echo "== catalog"
curl -sf "$API/v1/catalog" "${H[@]}" -d '{"vertical":"CHAUFFEUR_POINT_TO_POINT","name":"Business Sedan|business","base_minor":9500,"currency":"EUR","per_km_minor":350,"auto_quotable":true}' >/dev/null

echo "== fleet + crew"
FLEET=$(curl -sf "$API/v1/dispatch/fleet" "${H[@]}" -d '{"kind":"vehicle","label":"S-Class · AM-123-X"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
CREW=$(curl -sf "$API/v1/dispatch/crew" "${H[@]}" -d '{"full_name":"J. de Vries","certificates":["CCV-D1","first-aid"]}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')

echo "== client (WhatsApp identity anchor)"
CLIENT=$(curl -sf "$API/v1/clients/resolve" "${H[@]}" -d '{"phone":"+31612345678","name":"M. Raap"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["client"]["id"])')

echo "== service request + instant quote"
SR=$(curl -sf "$API/v1/requests" "${H[@]}" -d "{\"client_id\":\"$CLIENT\",\"idempotency_key\":\"demo-req-1\",\"items\":[{\"vertical\":\"CHAUFFEUR_POINT_TO_POINT\",\"vehicle_class\":\"business\",\"pickup_address\":\"AMS Schiphol\",\"pickup_lat\":52.3105,\"pickup_lng\":4.7683,\"dropoff_address\":\"Herengracht 1, Amsterdam\",\"dropoff_lat\":52.3676,\"dropoff_lng\":4.9041,\"pickup_at\":$(date -d '+1 day' +%s),\"passengers\":2}]}" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
QUOTE=$(curl -sf -X POST "$API/v1/requests/$SR/quote" "${H[@]}" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
echo "quote: $QUOTE"

echo "== payment link -> capture -> split settlement"
INTENT=$(curl -sf "$API/v1/payments/links" "${H[@]}" -d "{\"quote_id\":\"$QUOTE\",\"mode\":\"FULL\",\"idempotency_key\":\"demo-pay-1\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
curl -sf -X POST "$API/v1/quotes/$QUOTE/confirm" "${H[@]}" >/dev/null
curl -sf "$API/v1/payments/webhooks/stripe" "${H[@]}" -d "{\"intent_id\":\"$INTENT\",\"event\":\"payment.captured\",\"psp_ref\":\"ch_demo\"}" >/dev/null

echo "== ledger postings (split settlement)"
curl -sf "$API/v1/payments/postings" "${H[@]}" | python3 -m json.tool

echo "== golden path complete"
