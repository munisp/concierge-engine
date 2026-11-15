#!/usr/bin/env bash
# seed-taylor.sh — Taylor Travel Management Group as a reference flagship
# tenant on concierge-engine (Phase 34 demo).
#
# Taylor is a real VIP travel management group (taylortravelmanagement.com):
# chauffeur services, private jets, helicopters, yachts and close protection
# for corporates, celebrities, governments and family offices, headquartered
# at Amsterdam Schiphol's General Aviation Terminal with a Dubai office.
# Their site today is a brochure with a "Get a Quote" form (1-hour email
# response). This seed demonstrates the replatform play: instant quoting and
# online booking on the service lines that can be priced, honest
# on-request/manual-quote handling for the ones that can't (jets, yachts,
# security), English default + German locale for the Germany hub, and
# service areas covering the real footprint.
#
# NOTE: names and prices below are ILLUSTRATIVE demo data shaped after
# Taylor's public service lines — not their real rates. A real onboarding
# imports the tenant's own catalog via the same calls. The WhatsApp number
# is Taylor's published Schiphol office line.
#
# Usage:  CE_API=http://localhost:8000 sh scripts/seed-taylor.sh
set -euo pipefail
API="${CE_API:-http://localhost:8000}"

echo "== create tenant (Taylor Travel Management Group)"
TENANT=$(curl -sf "$API/v1/platform/tenants" -H 'Content-Type: application/json' -d '{
  "slug":"taylor","name":"Taylor Travel Management Group","archetype":"flagship",
  "pricing_rules":{"currency":"EUR","deposit_pct":0.3,"quote_ttl_seconds":7200,"platform_fee_pct":0.03},
  "sla_config":{"quote_sla_seconds":3600}}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
echo "tenant: $TENANT"
H=(-H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json')

echo "== tenant config: NL VAT, dispatch windows, English default + German"
curl -sf -X PUT "$API/v1/tenant" "${H[@]}" -d '{
  "brand":{"whatsapp_number":"+31854013539","tagline":"Redefining the standards of luxury travel."},
  "tax_config":{"engine":"flat","country":"NL","vat_rate_bp":2100,"prices_include_tax":false},
  "dispatch_config":{"default_duration_minutes":180,"turnaround_minutes":45},
  "site_config":{
    "locales":["en","de"],
    "brand_i18n":{
      "en":{"tagline":"Redefining the standards of luxury travel."},
      "de":{"tagline":"Wir definieren die Standards luxuriösen Reisens neu."}
    }
  }}' >/dev/null

# svc <vertical> <name> <base_minor> <per_km> <per_hour> <auto_quotable> <desc>
svc() {
  curl -sf "$API/v1/catalog" "${H[@]}" -d "$(python3 - "$@" <<'PY'
import json,sys
v,name,base,pkm,ph,auto,desc=sys.argv[1:8]
print(json.dumps({"vertical":v,"name":name,"base_minor":int(base),
 "per_km_minor":int(pkm),"per_hour_minor":int(ph),"currency":"EUR",
 "active":True,"auto_quotable":auto=="true","desc_i18n":{"en":desc}}))
PY
)" >/dev/null
  echo "  + $2"
}

echo "== catalog: the real service lines (illustrative EUR pricing)"
svc CHAUFFEUR_POINT_TO_POINT "Schiphol Airport Transfer (Business Class)" 9500 210 0 true \
  "Meet-and-greet at Schiphol's General Aviation Terminal with flight tracking, door-to-door in a business-class sedan."
svc CHAUFFEUR_HOURLY "Hourly Chauffeur (Business Class)" 7500 0 6500 true \
  "A chauffeur and vehicle at your disposal — roadshows, multi-stop days, evenings."
svc AIRPORT_MEET_GREET "VIP Meet & Greet (Schiphol GAT)" 8500 0 0 true \
  "Personal greeter at the General Aviation Terminal, luggage handling, seamless curb-to-cabin."
svc CHAUFFEUR_POINT_TO_POINT "Intercity Transfer (Amsterdam–Brussels/Paris)" 14500 260 0 true \
  "Long-distance intercity transfers across the Benelux and northern France in first-class comfort."
svc EVENT_TRANSPORT "Event & Roadshow Transport" 0 0 0 false \
  "Coordinated multi-vehicle guest transport for events, roadshows and delegations — quoted per programme."
svc HELICOPTER "Helicopter Transfer (on request)" 0 0 0 false \
  "Point-to-point helicopter transfers across the region — quoted per routing and aircraft."
svc PRIVATE_JET "Private Jet Charter (on request)" 0 0 0 false \
  "Worldwide private jet charter from Schiphol GAT and Dubai — quoted per mission."
svc YACHT "Yacht Charter (on request)" 0 0 0 false \
  "Crewed yacht charter through Taylor's partner network — quoted per itinerary."
svc SECURITY "Close Protection / VIP Security (on request)" 0 0 0 false \
  "Licensed close-protection officers for principals, families and delegations — quoted per detail."

echo "== service areas: the real footprint"
area() {
  curl -sf "$API/v1/geo/areas" "${H[@]}" -d "$(python3 - "$1" "$2" "$3" "$4" "$5" <<'PY'
import json,sys
name,lat1,lng1,lat2,lng2=sys.argv[1],float(sys.argv[2]),float(sys.argv[3]),float(sys.argv[4]),float(sys.argv[5])
print(json.dumps({"name":name,
 "polygon":[[lat1,lng1],[lat2,lng1],[lat2,lng2],[lat1,lng2]]}))
PY
)" >/dev/null
  echo "  + $1"
}
area "Amsterdam & Schiphol" 52.28 4.70 52.45 5.00
area "Dubai" 25.05 55.10 25.30 55.45

echo
echo "== done. Public site:"
echo "   $API/site/taylor/            (English default)"
echo "   $API/site/taylor/de/         (German)"
echo "   $API/site/taylor/sitemap.xml"
echo "   $API/site/taylor/links       (link-in-bio)"
