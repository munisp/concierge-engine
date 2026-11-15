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
	// Phase 31: premium template sections. en + pt-PT are fully translated;
	// other locales backfill from English in dictFor (honest degrade — the
	// same policy as guide prose, no machine-translated marketing copy).
	HowItWorks     string
	Step1Title     string
	Step1Body      string
	Step2Title     string
	Step2Body      string
	Step2BodyNoPay string // no payment rail enabled — no payment-link claim
	Step3Title     string
	Step3Body      string
	PlatformBand   string // dark platform-features band heading
	WidgetTitle    string
	WidgetBody     string
	ConciergeTitle string
	ConciergeBody  string
	TrackingTitle  string
	TrackingBody   string
	PaymentsTitle  string
	PaymentsBody   string
	FleetTitle     string
	FleetBody      string
	ReviewsHeading string
	ServicesWord   string // stats strip plural: "services"
	AreasWord      string // stats strip plural: "areas"
	// Phase 35 (A10): social-proof band labels.
	ClientsServed  string // "clients served"
	TripsCompleted string // "trips completed"
	CTABandTitle   string
	CTABandBody    string
	VerifiedClient string // review attribution line
}

var siteDicts = map[string]siteDict{
	"en": {
		BookYourConcierge: "Book your concierge", BookNow: "Book now", BookPrefix: "Book ",
		Services: "Services", WhereWeOperate: "Where we operate", CommonQuestions: "Common questions",
		OurPartners: "Our partners", ClientReviews: "client reviews", FromPrefix: "from",
		ChatWhatsApp: "Chat on WhatsApp", SecurePayment: "Secure online payment",
		WhatsAppConfirm: "WhatsApp confirmation", FooterTagline: "concierge, arranged end to end.",
		HowItWorks: "How it works",
		Step1Title: "Tell us what you need", Step1Body: "Book online or message us on WhatsApp — dates, pickup, passengers, wishes.",
		Step2Title: "Your concierge confirms", Step2Body: "A dedicated concierge confirms every detail and sends one secure payment link.",
		Step2BodyNoPay: "A dedicated concierge confirms every detail and the full price with you before anything is booked.",
		Step3Title:     "Travel with live tracking", Step3Body: "Driver details before pickup, live trip tracking, and receipts in the same thread.",
		PlatformBand:   "One platform behind every booking",
		WidgetTitle:    "Instant online booking",
		WidgetBody:     "Real-time quotes and confirmed bookings right on this page — no phone calls, no waiting.",
		ConciergeTitle: "WhatsApp concierge",
		ConciergeBody:  "An AI-assisted concierge answers in minutes, 24/7, with a human specialist on hand for anything complex.",
		TrackingTitle:  "Live trip tracking",
		TrackingBody:   "Every trip carries a live tracking link — passengers and bookers see the driver arrive in real time.",
		PaymentsTitle:  "Secure payments",
		PaymentsBody:   "One secure payment link per booking — major cards and local payment methods, receipts issued automatically.",
		FleetTitle:     "Vetted fleet & chauffeurs",
		FleetBody:      "Quality-scored vehicles and certified professional chauffeurs, assigned and monitored per trip.",
		ReviewsHeading: "What clients say",
		ServicesWord:   "services",
		AreasWord:      "areas",
		ClientsServed:  "clients served",
		TripsCompleted: "trips completed",
		CTABandTitle:   "Ready when you are",
		CTABandBody:    "Book in under a minute — your concierge takes it from there.",
		VerifiedClient: "Verified client",
	},
	"pt-PT": {
		BookYourConcierge: "Reserve o seu concierge", BookNow: "Reserve já", BookPrefix: "Reservar ",
		Services: "Serviços", WhereWeOperate: "Onde operamos", CommonQuestions: "Perguntas frequentes",
		OurPartners: "Os nossos parceiros", ClientReviews: "avaliações de clientes", FromPrefix: "desde",
		ChatWhatsApp: "Fale connosco no WhatsApp", SecurePayment: "Pagamento online seguro",
		WhatsAppConfirm: "Confirmação por WhatsApp", FooterTagline: "concierge, organizado de ponta a ponta.",
		HowItWorks: "Como funciona",
		Step1Title: "Diga-nos o que precisa", Step1Body: "Reserve online ou envie-nos mensagem no WhatsApp — datas, recolha, passageiros, preferências.",
		Step2Title: "O seu concierge confirma", Step2Body: "Um concierge dedicado confirma cada detalhe e envia um único link de pagamento seguro.",
		Step2BodyNoPay: "Um concierge dedicado confirma cada detalhe e o preço total consigo antes de reservar.",
		Step3Title:     "Viaje com acompanhamento", Step3Body: "Dados do motorista antes da recolha, acompanhamento em direto e recibos na mesma conversa.",
		PlatformBand:   "Uma plataforma por trás de cada reserva",
		WidgetTitle:    "Reserva online imediata",
		WidgetBody:     "Preços em tempo real e reservas confirmadas nesta página — sem telefonemas, sem esperas.",
		ConciergeTitle: "Concierge no WhatsApp",
		ConciergeBody:  "Um concierge assistido por IA responde em minutos, 24/7, com um especialista humano disponível para o que for complexo.",
		TrackingTitle:  "Acompanhamento em direto",
		TrackingBody:   "Cada viagem tem um link de acompanhamento — passageiros e clientes veem o motorista chegar em tempo real.",
		PaymentsTitle:  "Pagamentos seguros",
		PaymentsBody:   "Um link de pagamento seguro por reserva — cartões e métodos locais, recibos emitidos automaticamente.",
		FleetTitle:     "Frota e motoristas certificados",
		FleetBody:      "Veículos avaliados por qualidade e motoristas profissionais certificados, atribuídos e monitorizados por viagem.",
		ReviewsHeading: "O que dizem os clientes",
		ServicesWord:   "serviços",
		AreasWord:      "zonas",
		ClientsServed:  "clientes servidos",
		TripsCompleted: "viagens concluídas",
		CTABandTitle:   "Prontos quando quiser",
		CTABandBody:    "Reserve em menos de um minuto — o seu concierge trata do resto.",
		VerifiedClient: "Cliente verificado",
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
// Phase 31 section strings backfill from English when a locale leaves them
// empty (honest degrade — no machine-translated marketing copy).
func dictFor(locale string) siteDict {
	d, ok := siteDicts[locale]
	if !ok {
		d = siteDicts["en"]
	}
	en := siteDicts["en"]
	for _, f := range []struct{ dst, src *string }{
		{&d.HowItWorks, &en.HowItWorks},
		{&d.Step1Title, &en.Step1Title}, {&d.Step1Body, &en.Step1Body},
		{&d.Step2Title, &en.Step2Title}, {&d.Step2Body, &en.Step2Body},
		{&d.Step2BodyNoPay, &en.Step2BodyNoPay},
		{&d.Step3Title, &en.Step3Title}, {&d.Step3Body, &en.Step3Body},
		{&d.PlatformBand, &en.PlatformBand},
		{&d.WidgetTitle, &en.WidgetTitle}, {&d.WidgetBody, &en.WidgetBody},
		{&d.ConciergeTitle, &en.ConciergeTitle}, {&d.ConciergeBody, &en.ConciergeBody},
		{&d.TrackingTitle, &en.TrackingTitle}, {&d.TrackingBody, &en.TrackingBody},
		{&d.PaymentsTitle, &en.PaymentsTitle}, {&d.PaymentsBody, &en.PaymentsBody},
		{&d.FleetTitle, &en.FleetTitle}, {&d.FleetBody, &en.FleetBody},
		{&d.ReviewsHeading, &en.ReviewsHeading},
		{&d.ServicesWord, &en.ServicesWord}, {&d.AreasWord, &en.AreasWord},
		{&d.ClientsServed, &en.ClientsServed}, {&d.TripsCompleted, &en.TripsCompleted},
		{&d.CTABandTitle, &en.CTABandTitle}, {&d.CTABandBody, &en.CTABandBody},
		{&d.VerifiedClient, &en.VerifiedClient},
	} {
		if *f.dst == "" {
			*f.dst = *f.src
		}
	}
	return d
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
func faqsForServiceL(t *model.Tenant, svc *model.CatalogService, svcName string, areas []model.ServiceArea, locale string, liveRails []string) []siteFAQ {
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
	// Phase 32: answer from LIVE rails (platform keys ∧ tenant flags), never
	// from flags alone — otherwise the FAQ promises a Stripe link on an
	// instance that can only mint sandbox links.
	has := func(rail string) bool {
		for _, r := range liveRails {
			if r == rail {
				return true
			}
		}
		return false
	}
	switch {
	case len(liveRails) == 0:
		if pt {
			faqs = append(faqs, siteFAQ{payQ, "O seu concierge combina o pagamento consigo diretamente ao confirmar a reserva."})
		} else {
			faqs = append(faqs, siteFAQ{payQ, "Your concierge arranges payment with you directly when confirming the booking."})
		}
	case !has("stripe"):
		if pt {
			faqs = append(faqs, siteFAQ{payQ, "Recebe um link de pagamento seguro Paystack quando a reserva é confirmada — cartão, transferência bancária ou outros métodos locais suportados."})
		} else {
			faqs = append(faqs, siteFAQ{payQ, "You receive a secure Paystack payment link when your booking is confirmed — card, bank transfer or mobile money where supported."})
		}
	case !has("paystack"):
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
