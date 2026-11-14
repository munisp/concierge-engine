package server

// Phase 29: multi-locale site engine. A tenant's public site serves each
// configured locale at its own URL tree (/site/{slug}/{locale}/...) with a
// full hreflang cluster — this is what lets a Portuguese operator rank for
// the English/German/French searches their customers actually make.
//
// Content strategy: UI chrome and guide/FAQ prose are template-translated
// (dictionaries below — human-reviewed, finite); catalog and area names
// come from per-resource i18n maps the tenant sets. de/fr/es ship with
// full chrome; guide body prose falls back to English there rather than
// machine-translated guesses (honest degrade — documented in
// docs/phase29.md).

import (
	"fmt"
	"strings"

	"concierge-engine/services/core-api/internal/model"
)

// siteDict is the finite set of UI strings a site page renders.
type siteDict struct {
	BookYourConcierge string
	BookNow           string
	BookPrefix        string // "Book " (precedes service name)
	Services          string
	WhereWeOperate    string
	CommonQuestions   string
	OurPartners       string
	ClientReviews     string // plural label after a count
	FromPrefix        string // "from" before a price
	ChatWhatsApp      string
	SecurePayment     string
	WhatsAppConfirm   string
	FooterTagline     string
}

var siteDicts = map[string]siteDict{
	"en": {
		BookYourConcierge: "Book your concierge", BookNow: "Book now", BookPrefix: "Book ",
		Services: "Services", WhereWeOperate: "Where we operate", CommonQuestions: "Common questions",
		OurPartners: "Our partners", ClientReviews: "client reviews", FromPrefix: "from",
		ChatWhatsApp: "Chat on WhatsApp", SecurePayment: "Secure online payment",
		WhatsAppConfirm: "WhatsApp confirmation", FooterTagline: "concierge, arranged end to end.",
	},
	"pt-PT": {
		BookYourConcierge: "Reserve o seu concierge", BookNow: "Reserve já", BookPrefix: "Reservar ",
		Services: "Serviços", WhereWeOperate: "Onde operamos", CommonQuestions: "Perguntas frequentes",
		OurPartners: "Os nossos parceiros", ClientReviews: "avaliações de clientes", FromPrefix: "desde",
		ChatWhatsApp: "Fale connosco no WhatsApp", SecurePayment: "Pagamento online seguro",
		WhatsAppConfirm: "Confirmação por WhatsApp", FooterTagline: "concierge, organizado de ponta a ponta.",
	},
	"de": {
		BookYourConcierge: "Ihren Concierge buchen", BookNow: "Jetzt buchen", BookPrefix: "Buchen: ",
		Services: "Leistungen", WhereWeOperate: "Einsatzgebiete", CommonQuestions: "Häufige Fragen",
		OurPartners: "Unsere Partner", ClientReviews: "Kundenbewertungen", FromPrefix: "ab",
		ChatWhatsApp: "Per WhatsApp chatten", SecurePayment: "Sichere Online-Zahlung",
		WhatsAppConfirm: "Bestätigung per WhatsApp", FooterTagline: "Concierge — von Anfang bis Ende organisiert.",
	},
	"fr": {
		BookYourConcierge: "Réservez votre concierge", BookNow: "Réserver", BookPrefix: "Réserver : ",
		Services: "Services", WhereWeOperate: "Nos zones d'intervention", CommonQuestions: "Questions fréquentes",
		OurPartners: "Nos partenaires", ClientReviews: "avis clients", FromPrefix: "à partir de",
		ChatWhatsApp: "Discuter sur WhatsApp", SecurePayment: "Paiement en ligne sécurisé",
		WhatsAppConfirm: "Confirmation par WhatsApp", FooterTagline: "concierge, organisé de bout en bout.",
	},
	"es": {
		BookYourConcierge: "Reserve su concierge", BookNow: "Reservar", BookPrefix: "Reservar: ",
		Services: "Servicios", WhereWeOperate: "Dónde operamos", CommonQuestions: "Preguntas frecuentes",
		OurPartners: "Nuestros socios", ClientReviews: "reseñas de clientes", FromPrefix: "desde",
		ChatWhatsApp: "Chatear por WhatsApp", SecurePayment: "Pago online seguro",
		WhatsAppConfirm: "Confirmación por WhatsApp", FooterTagline: "conserjería, organizada de principio a fin.",
	},
}

// dictFor resolves the UI dictionary; unknown locales get English chrome.
func dictFor(locale string) siteDict {
	if d, ok := siteDicts[locale]; ok {
		return d
	}
	// try the language part (e.g. pt-BR → no entry → en)
	return siteDicts["en"]
}

// tenantLocales is the ordered locale set: site_config.locales when
// configured (first = default), else the single site_config.locale,
// else English.
func tenantLocales(t *model.Tenant) []string {
	if len(t.SiteConfig.Locales) > 0 {
		return t.SiteConfig.Locales
	}
	if t.SiteConfig.Locale != "" {
		return []string{t.SiteConfig.Locale}
	}
	return []string{"en"}
}

func defaultLocale(t *model.Tenant) string { return tenantLocales(t)[0] }

// validLocale reports whether the locale path segment is configured for
// this tenant (non-configured prefixes 404 rather than render duplicates).
func validLocale(t *model.Tenant, locale string) bool {
	for _, l := range tenantLocales(t) {
		if l == locale {
			return true
		}
	}
	return false
}

// localizedServiceName / localizedAreaName resolve per-resource i18n
// overrides; absent entries fall back to the canonical (default-locale) name.
func localizedServiceName(s model.CatalogService, locale string) string {
	if locale != "" {
		if v, ok := s.NameI18n[locale]; ok && v != "" {
			return v
		}
	}
	return s.Name
}

func localizedServiceDesc(s model.CatalogService, locale string) string {
	if locale != "" {
		if v, ok := s.DescI18n[locale]; ok && v != "" {
			return v
		}
	}
	return ""
}

func localizedAreaName(a model.ServiceArea, locale string) string {
	if locale != "" {
		if v, ok := a.NameI18n[locale]; ok && v != "" {
			return v
		}
	}
	return a.Name
}

// localizedBrand resolves the tenant tagline/description for a locale from
// site_config.brand_i18n, falling back to the brand map then the default.
func localizedBrand(t *model.Tenant, locale string) string {
	if locale != "" && t.SiteConfig.BrandI18n != nil {
		if m, ok := t.SiteConfig.BrandI18n[locale]; ok {
			if v := m["tagline"]; v != "" {
				return v
			}
			if v := m["description"]; v != "" {
				return v
			}
		}
	}
	return brandDescription(t)
}

// verticalLabel humanizes the vertical enum per locale. Unknown verticals
// fall back to the humanized English enum (never an empty string).
func verticalLabel(vertical, locale string) string {
	v := strings.ToLower(strings.ReplaceAll(vertical, "_", " "))
	localized := map[string]map[string]string{
		"pt-PT": {
			"chauffeur point to point": "transporte privado",
			"chauffeur hourly":         "motorista privado à hora",
			"airport transfer":         "transfer de aeroporto",
			"private tour":             "tour privado",
			"event transport":          "transporte para eventos",
		},
		"de": {
			"chauffeur point to point": "Privatfahrt",
			"airport transfer":         "Flughafentransfer",
			"private tour":             "Private Tour",
		},
		"fr": {
			"chauffeur point to point": "transport privé",
			"airport transfer":         "transfert aéroport",
			"private tour":             "visite privée",
		},
		"es": {
			"chauffeur point to point": "transporte privado",
			"airport transfer":         "traslado al aeropuerto",
			"private tour":             "tour privado",
		},
	}
	if m, ok := localized[locale]; ok {
		if lv, ok := m[v]; ok {
			return lv
		}
	}
	return v
}

// asciiFold strips diacritics for the OG card renderer — the embedded
// 7x13 font is ASCII-only. Page HTML itself is full UTF-8; this applies
// only to the bitmap card.
func asciiFold(s string) string {
	repl := strings.NewReplacer(
		"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
		"Á", "A", "À", "A", "Â", "A", "Ã", "A", "Ä", "A",
		"é", "e", "è", "e", "ê", "e", "ë", "e", "É", "E", "È", "E", "Ê", "E",
		"í", "i", "ì", "i", "î", "i", "Í", "I",
		"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o", "Ó", "O", "Ô", "O", "Õ", "O",
		"ú", "u", "ù", "u", "û", "u", "ü", "u", "Ú", "U", "Ü", "U",
		"ç", "c", "Ç", "C", "ñ", "n", "Ñ", "N", "ß", "ss",
	)
	return repl.Replace(s)
}

// ---------------------------------------------------------------------------
// Guide + FAQ prose. Full translation: en, pt-PT. de/fr/es render localized
// chrome with English body prose (honest degrade — no machine-translated
// marketing copy).
func proseLocale(locale string) string {
	if locale == "pt-PT" {
		return "pt-PT"
	}
	return "en"
}

func guideServiceIntro(t *model.Tenant, svc *model.CatalogService, locale, price string) string {
	vertical := verticalLabel(svc.Vertical, locale)
	if proseLocale(locale) == "pt-PT" {
		if price != "" {
			return t.Name + " disponibiliza " + vertical + " desde " + price + ", organizado de ponta a ponta. Cada reserva inclui um concierge dedicado, acompanhamento em direto e atualizações por WhatsApp, da confirmação à conclusão."
		}
		return t.Name + " disponibiliza " + vertical + " organizado de ponta a ponta. Cada reserva inclui um concierge dedicado, acompanhamento em direto e atualizações por WhatsApp, da confirmação à conclusão."
	}
	if price != "" {
		return t.Name + " provides " + vertical + " from " + price + ", arranged end to end. Every booking includes a dedicated concierge, live trip tracking and WhatsApp updates from confirmation to completion."
	}
	return t.Name + " provides " + indefinite(vertical) + " arranged end to end. Every booking includes a dedicated concierge, live trip tracking and WhatsApp updates from confirmation to completion."
}

func guideCoverage(svcName, brand string, areas []model.ServiceArea, locale string) string {
	if len(areas) == 0 {
		return ""
	}
	names := make([]string, 0, len(areas))
	for _, a := range areas {
		names = append(names, localizedAreaName(a, locale))
	}
	if proseLocale(locale) == "pt-PT" {
		return svcName + " está disponível em " + joinNames(names, 6) + ". A cobertura é verificada em cada reserva — se o ponto de recolha estiver fora das áreas listadas, o concierge confirma a disponibilidade antes de cotar."
	}
	return svcName + " is available across " + joinNames(names, 6) + ". Coverage is verified per booking — if your pickup sits outside a listed area, your concierge will confirm availability before quoting."
}

func guideAreaIntro(t *model.Tenant, areaName string, parts []string, n int, locale string) string {
	if proseLocale(locale) == "pt-PT" {
		return fmt.Sprintf("%s opera %d %s de concierge em %s: %s. Cada um é reservado com preço em direto e confirmado por um concierge dedicado no WhatsApp.",
			t.Name, n, map[bool]string{true: "serviço", false: "serviços"}[n == 1], areaName, joinNames(parts, 8))
	}
	return fmt.Sprintf("%s operates %d concierge %s in %s: %s. Each is booked with live pricing and confirmed by a dedicated concierge on WhatsApp.",
		t.Name, n, pluralizeCount(n, "service", "services"), areaName, joinNames(parts, 8))
}

func guideAreaTrust(areaName, locale string) string {
	if proseLocale(locale) == "pt-PT" {
		return "As reservas em " + areaName + " incluem os dados do motorista ou fornecedor partilhados antes da recolha, acompanhamento em direto para o passageiro e recibo pós-viagem — tudo numa única conversa de WhatsApp."
	}
	return "Bookings in " + areaName + " include driver or provider details shared before pickup, live trip tracking for the passenger, and post-trip receipts — all in one WhatsApp thread."
}

// faqsForServiceL is the locale-aware FAQ builder (Phase 29 superset of
// faqsForService). Full translation en + pt-PT; other locales fall back to
// English body text.
func faqsForServiceL(t *model.Tenant, svc *model.CatalogService, svcName string, areas []model.ServiceArea, locale string) []siteFAQ {
	pt := proseLocale(locale) == "pt-PT"
	var faqs []siteFAQ
	pl := priceLabel(*svc)
	var costQ, costA string
	if pt {
		costQ = "Quanto custa " + svcName + "?"
		if pl != "" {
			costA = svcName + " começa em " + pl + "."
			if svc.PerKMMinor > 0 {
				costA += fmt.Sprintf(" Distância adicional é cobrada a %s por quilómetro.", perUnitLabel(svc.Currency, svc.PerKMMinor))
			}
			if svc.PerHourMinor > 0 {
				costA += fmt.Sprintf(" Tempo adicional é cobrado a %s por hora.", perUnitLabel(svc.Currency, svc.PerHourMinor))
			}
		} else {
			costA = "Cada reserva é cotada em direto antes de qualquer compromisso — o seu concierge confirma o preço total e o cliente aprova primeiro."
		}
	} else {
		costQ = "How much does " + svcName + " cost?"
		if pl != "" {
			costA = svcName + " starts at " + pl + "."
			if svc.PerKMMinor > 0 {
				costA += fmt.Sprintf(" Additional distance is billed at %s per kilometre.", perUnitLabel(svc.Currency, svc.PerKMMinor))
			}
			if svc.PerHourMinor > 0 {
				costA += fmt.Sprintf(" Additional time is billed at %s per hour.", perUnitLabel(svc.Currency, svc.PerHourMinor))
			}
		} else {
			costA = "Every booking is quoted live before you commit — your concierge confirms the full price and you approve it first."
		}
	}
	faqs = append(faqs, siteFAQ{costQ, costA})

	if len(areas) > 0 {
		names := make([]string, 0, len(areas))
		for _, a := range areas {
			names = append(names, localizedAreaName(a, locale))
		}
		if pt {
			faqs = append(faqs, siteFAQ{"Onde está disponível " + svcName + "?",
				fmt.Sprintf("%s opera atualmente em %s.", t.Name, joinNames(names, 6))})
		} else {
			faqs = append(faqs, siteFAQ{"Where is " + svcName + " available?",
				fmt.Sprintf("%s currently serves %s.", t.Name, joinNames(names, 6))})
		}
	}

	if pt {
		faqs = append(faqs, siteFAQ{"Como reservo?",
			"Toque no botão de reserva nesta página — o seu concierge confirma os detalhes por WhatsApp em poucos minutos."})
	} else {
		faqs = append(faqs, siteFAQ{"How do I book?",
			"Tap the booking button on this page — your concierge confirms the details on WhatsApp within minutes."})
	}

	payQ := "How do I pay?"
	if pt {
		payQ = "Como pago?"
	}
	switch {
	case t.PaymentConfig.DisableStripe && t.PaymentConfig.DisablePaystack:
		if pt {
			faqs = append(faqs, siteFAQ{payQ, "O seu concierge combina o pagamento consigo diretamente ao confirmar a reserva."})
		} else {
			faqs = append(faqs, siteFAQ{payQ, "Your concierge arranges payment with you directly when confirming the booking."})
		}
	case t.PaymentConfig.DisableStripe:
		if pt {
			faqs = append(faqs, siteFAQ{payQ, "Recebe um link de pagamento seguro Paystack quando a reserva é confirmada — cartão, transferência bancária ou outros métodos locais suportados."})
		} else {
			faqs = append(faqs, siteFAQ{payQ, "You receive a secure Paystack payment link when your booking is confirmed — card, bank transfer or mobile money where supported."})
		}
	case t.PaymentConfig.DisablePaystack:
		if pt {
			faqs = append(faqs, siteFAQ{payQ, "Recebe um link de pagamento seguro Stripe quando a reserva é confirmada — cartões e métodos de pagamento locais suportados."})
		} else {
			faqs = append(faqs, siteFAQ{payQ, "You receive a secure Stripe payment link when your booking is confirmed — all major cards and local payment methods where supported."})
		}
	default:
		if pt {
			faqs = append(faqs, siteFAQ{payQ, "Recebe um link de pagamento seguro quando a reserva é confirmada — cartões e métodos locais suportados."})
		} else {
			faqs = append(faqs, siteFAQ{payQ, "You receive a secure payment link when your booking is confirmed — all major cards and local payment methods where supported."})
		}
	}
	return faqs
}
