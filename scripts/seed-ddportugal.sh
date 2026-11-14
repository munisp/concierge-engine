#!/usr/bin/env bash
# seed-ddportugal.sh — DDPortugal (ddportugal.com) as a reference brownfield
# tenant on concierge-engine (Phase 29 demo).
#
# DDPortugal is a Lisbon luxury tours + chauffeur operator whose site is
# Portuguese-only with a contact-form funnel. This seed demonstrates the
# replatform play: bilingual site (pt-PT default + English), per-service
# i18n, instant booking CTAs and a WhatsApp concierge entry point.
#
# NOTE: names, durations and prices below are ILLUSTRATIVE demo data shaped
# after DDPortugal's public tour catalogue — not their real rates. A real
# onboarding imports the tenant's own catalog via the same calls.
#
# Usage:  CE_API=http://localhost:8000 sh scripts/seed-ddportugal.sh
set -euo pipefail
API="${CE_API:-http://localhost:8000}"

echo "== create tenant (DDPortugal)"
TENANT=$(curl -sf "$API/v1/platform/tenants" -H 'Content-Type: application/json' -d '{
  "slug":"ddportugal","name":"DDPortugal","archetype":"brownfield",
  "pricing_rules":{"currency":"EUR","deposit_pct":0.2,"quote_ttl_seconds":7200,"platform_fee_pct":0.03},
  "sla_config":{"quote_sla_seconds":3600}}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
echo "tenant: $TENANT"
H=(-H "X-Tenant-ID: $TENANT" -H 'Content-Type: application/json')

echo "== site config: pt-PT default + English, localized brand, WhatsApp entry"
curl -sf -X PUT "$API/v1/tenant" "${H[@]}" -d '{
  "brand":{"whatsapp_number":"+351900000000","tagline":"Luxury transport and private tours across Portugal."},
  "site_config":{
    "locales":["pt-PT","en"],
    "custom_domains":["www.ddportugal.com","ddportugal.com"],
    "brand_i18n":{
      "pt-PT":{"tagline":"Transporte de luxo e tours privados por todo o Portugal."},
      "en":{"tagline":"Luxury transport and private tours across Portugal."}
    },
    "social_links":["https://instagram.com/ddportugal"]
  }}' >/dev/null

add_tour() {
  local v="$1" name="$2" pt="$3" en_d="$4" pt_d="$5" base="$6"
  curl -sf "$API/v1/catalog" "${H[@]}" -d "$(python3 - "$v" "$name" "$pt" "$en_d" "$pt_d" "$base" <<'PY'
import json,sys
v,name,pt,en_d,pt_d,base=sys.argv[1:7]
print(json.dumps({"vertical":v,"name":name,"base_minor":int(base),"currency":"EUR","active":True,
 "name_i18n":{"pt-PT":pt},"desc_i18n":{"en":en_d,"pt-PT":pt_d}}))
PY
)" >/dev/null
  echo "  + $name / $pt"
}

echo "== catalog: tours, transfers, events (illustrative EUR pricing)"
add_tour PRIVATE_TOUR "Lisbon Half-Day Tour (4H)" "Tour de Lisboa (4H)" \
  "Lisbon's essentials in four hours with a private driver-guide." \
  "O essencial de Lisboa em quatro horas com motorista-guia privado." 24000
add_tour PRIVATE_TOUR "Lisbon Full-Day Tour (8H)" "Tour de Lisboa (8H)" \
  "A full private day across Lisbon's neighborhoods, viewpoints and waterfront." \
  "Um dia inteiro privado pelos bairros, miradouros e frente ribeirinha de Lisboa." \
  42000
add_tour PRIVATE_TOUR "Sintra Half-Day Tour (4H)" "Tour de Sintra (4H)" \
  "Pena Palace and the Sintra mountains on a private half-day escape." \
  "O Palácio da Pena e a serra de Sintra numa escapadela privada de meio dia." \
  26000
add_tour PRIVATE_TOUR "Sintra Full-Day Tour (8H)" "Tour de Sintra (8H)" \
  "Sintra, Cabo da Roca and Cascais — a private full-day coastal circuit." \
  "Sintra, Cabo da Roca e Cascais — um circuito costeiro privado de dia inteiro." \
  45000
add_tour PRIVATE_TOUR "Fatima Sanctuary Tour (5H)" "Tour de Fátima (5H)" \
  "Private pilgrimage transport to the Sanctuary of Fatima." \
  "Transporte privado de peregrinação ao Santuário de Fátima." 30000
add_tour PRIVATE_TOUR "Evora Full-Day Tour (8H)" "Tour de Évora (8H)" \
  "Alentejo wine country and Evora's Roman heritage, privately." \
  "O vinho do Alentejo e o património romano de Évora, em privado." 46000
add_tour PRIVATE_TOUR "Porto Day Tour (10H)" "Tour do Porto (10H)" \
  "Lisbon to Porto and back in one private, chauffeured day." \
  "De Lisboa ao Porto e regresso num dia privado com motorista." 65000
add_tour PRIVATE_TOUR "Custom Private Tour" "Tour Personalizado" \
  "Your route, your pace — a fully custom private itinerary." \
  "O seu percurso, ao seu ritmo — um itinerário totalmente personalizado." 0
add_tour AIRPORT_TRANSFER "Lisbon Airport Transfer" "Transfer de Aeroporto" \
  "Meet-and-greet airport transfers with flight tracking." \
  "Transfers de aeroporto com receção e monitorização de voo." 6500
add_tour EVENT_TRANSPORT "Event & Wedding Transport" "Transporte para Eventos" \
  "Coordinated guest transport for weddings and corporate events." \
  "Transporte coordenado de convidados para casamentos e eventos empresariais." 0

echo "== service areas"
area() {
  curl -sf "$API/v1/geo/areas" "${H[@]}" -d "$(python3 - "$1" "$2" <<'PY'
import json,sys
name,pt=sys.argv[1],sys.argv[2]
boxes={"Lisbon":[[38.69,-9.23],[38.80,-9.09]],"Sintra":[[38.75,-9.48],[38.85,-9.35]],
 "Fatima":[[39.58,-8.72],[39.68,-8.60]],"Evora":[[38.54,-8.05],[38.62,-7.85]],
 "Porto":[[41.10,-8.72],[41.23,-8.55]]}
b=boxes[name]
print(json.dumps({"name":name,"name_i18n":{"pt-PT":pt},
 "polygon":[[b[0][0],b[0][1]],[b[1][0],b[0][1]],[b[1][0],b[1][1]],[b[0][0],b[1][1]]]}))
PY
)" >/dev/null
  echo "  + $1"
}
area "Lisbon" "Lisboa"
area "Sintra" "Sintra"
area "Fatima" "Fátima"
area "Evora" "Évora"
area "Porto" "Porto"

echo
echo "== done. Public site:"
echo "   $API/site/ddportugal/        (pt-PT, default)"
echo "   $API/site/ddportugal/en/     (English)"
echo "   $API/site/ddportugal/sitemap.xml"
echo "   $API/site/ddportugal/links   (link-in-bio)"
