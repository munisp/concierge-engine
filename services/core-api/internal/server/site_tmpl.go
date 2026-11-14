package server

// Phase 31: site template v2 — the premium marketing surface. Sticky glass
// nav, full-bleed hero (tenant media image with gradient overlay, theme
// gradient fallback), stats strip, 16:9 image service cards (monogram
// tile fallback), how-it-works, the dark platform-features band with the
// inline booking widget, consented review quotes, area pills, FAQ, CTA
// band and the sticky mobile CTA. All chrome strings come from siteDict;
// layout never depends on theme tokens (Phase 30 contract holds).

import "html/template"

var siteTmpl = template.Must(template.New("site").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="{{.Description}}">
<link rel="canonical" href="{{.Canonical}}">
{{range .HrefLangs}}<link rel="alternate" hreflang="{{.Lang}}" href="{{.URL}}">
{{end}}{{if .Verification}}<meta name="google-site-verification" content="{{.Verification}}">{{end}}
<meta property="og:type" content="website">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta property="og:url" content="{{.Canonical}}">
{{if .OGImage}}<meta property="og:image" content="{{.OGImage}}">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:image" content="{{.OGImage}}">{{else}}<meta name="twitter:card" content="summary">{{end}}
<script type="application/ld+json">{{.JSONLD}}</script>
<style>
:root{--ink:#0e1626;--accent:#c9a227;--muted:#5b6672;--bg:#faf9f6;--radius:8px;--font-main:Georgia,'Times New Roman',serif}
{{.ThemeCSS}}
*{box-sizing:border-box}
body{font-family:var(--font-main);margin:0;color:var(--ink);background:var(--bg)}
.nav{position:sticky;top:0;z-index:60;display:flex;align-items:center;justify-content:space-between;gap:16px;padding:14px 28px;background:color-mix(in srgb,var(--ink) 88%,transparent);backdrop-filter:blur(12px);-webkit-backdrop-filter:blur(12px);color:#fff;font-family:system-ui,sans-serif}
.nav .brand{font-family:var(--font-main);font-size:1.25rem;letter-spacing:.08em;color:#fff;text-decoration:none}
.nav .links{display:flex;gap:22px;align-items:center}
.nav .links a{color:rgba(255,255,255,.82);text-decoration:none;font-size:.9rem}
.nav .links a:hover{color:#fff}
.nav .cta{background:var(--accent);color:var(--ink);padding:10px 22px;border-radius:var(--radius);text-decoration:none;font-weight:600;font-size:.9rem;white-space:nowrap}
.nav .cta:hover{filter:brightness(1.1)}
@media (max-width:760px){.nav .links{display:none}}
.hero{position:relative;background:linear-gradient(160deg,var(--ink) 0%,color-mix(in srgb,var(--ink) 72%,white) 130%);color:#fff;text-align:center;overflow:hidden}
.hero .hero-bg{position:absolute;inset:0;width:100%;height:100%;object-fit:cover;opacity:.55}
.hero::after{content:"";position:absolute;inset:0;background:linear-gradient(180deg,color-mix(in srgb,var(--ink) 55%,transparent) 0%,color-mix(in srgb,var(--ink) 82%,transparent) 78%,var(--ink) 100%)}
.hero .hero-inner{position:relative;z-index:1;padding:110px 24px 92px;max-width:860px;margin:0 auto}
.hero .brand{font-size:3rem;letter-spacing:.1em;margin:0 0 14px;font-weight:400}
.hero .tag{margin:0 auto;opacity:.88;font-size:1.15rem;font-family:system-ui,sans-serif;max-width:640px;line-height:1.55}
.rating{display:inline-block;margin-top:20px;padding:7px 18px;border:1px solid var(--accent);border-radius:999px;color:var(--accent);font-family:system-ui,sans-serif;font-size:.9rem;background:color-mix(in srgb,var(--ink) 55%,transparent)}
.cta-row{margin-top:32px;display:flex;gap:14px;justify-content:center;flex-wrap:wrap}
.btn{display:inline-block;padding:15px 36px;border-radius:var(--radius);font-weight:600;font-family:system-ui,sans-serif;text-decoration:none;font-size:1.02rem}
.btn-primary{background:var(--accent);color:var(--ink)}
.btn-primary:hover{filter:brightness(1.1)}
.btn-ghost{border:1px solid rgba(255,255,255,.55);color:#fff}
.btn-ghost:hover{border-color:#fff}
main{max-width:1060px;margin:0 auto;padding:0 20px 120px}
.trust{display:flex;gap:10px;flex-wrap:wrap;justify-content:center;margin:-22px 0 40px;position:relative;z-index:2}
.trust span{background:#fff;border:1px solid #e8e2d2;border-radius:999px;padding:9px 18px;font-family:system-ui,sans-serif;font-size:.82rem;color:var(--muted);box-shadow:0 2px 10px rgba(14,22,38,.06)}
.stats{display:flex;justify-content:center;margin:0 0 48px;border:1px solid #ece7d8;border-radius:var(--radius);background:#fff;overflow:hidden}
.stats div{flex:1;padding:22px 12px;text-align:center;font-family:system-ui,sans-serif}
.stats div+div{border-left:1px solid #f0ebdc}
.stats .v{font-size:1.6rem;font-weight:700;color:var(--ink)}
.stats .l{font-size:.8rem;color:var(--muted);letter-spacing:.06em;text-transform:uppercase;margin-top:2px}
h2{font-size:1.2rem;letter-spacing:.1em;text-transform:uppercase;font-family:system-ui,sans-serif;color:var(--ink);margin:56px 0 20px;display:flex;align-items:center;gap:14px}
h2::after{content:"";flex:1;height:1px;background:#e3ddc9}
.cards{display:grid;grid-template-columns:1fr 1fr;gap:22px;list-style:none;padding:0}
.cards li{background:#fff;border:1px solid #ece7d8;border-radius:var(--radius);overflow:hidden;transition:transform .15s,box-shadow .15s}
.cards li:hover{transform:translateY(-3px);box-shadow:0 14px 34px rgba(14,22,38,.13)}
.card-top{background:linear-gradient(135deg,color-mix(in srgb,var(--ink) 85%,white),var(--ink));color:var(--accent);font-size:2rem;display:flex;align-items:center;justify-content:center;aspect-ratio:16/9;letter-spacing:.1em;overflow:hidden}
.card-top img{width:100%;height:100%;object-fit:cover;display:block}
.card-body{padding:18px}
.card-body .nm{font-size:1.12rem;margin:0 0 6px}
.card-body .ds{font-family:system-ui,sans-serif;font-size:.88rem;color:var(--muted);margin:0 0 14px;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;line-height:1.5}
.card-foot{display:flex;justify-content:space-between;align-items:center;font-family:system-ui,sans-serif}
.card-foot .pr{color:var(--muted);font-size:.9rem}
.card-foot a{color:var(--ink);font-weight:600;text-decoration:none;border-bottom:2px solid var(--accent)}
.guide{line-height:1.75;color:#3a4350;font-size:1.04rem;max-width:760px}
.steps{display:grid;grid-template-columns:repeat(3,1fr);gap:18px;list-style:none;padding:0}
.steps li{background:#fff;border:1px solid #ece7d8;border-radius:var(--radius);padding:24px 20px}
.steps .n{display:inline-flex;width:34px;height:34px;border-radius:50%;background:var(--ink);color:var(--accent);align-items:center;justify-content:center;font-family:system-ui,sans-serif;font-weight:700;margin-bottom:12px}
.steps .st{font-weight:600;margin:0 0 6px;font-size:1.02rem}
.steps .sb{font-family:system-ui,sans-serif;font-size:.88rem;color:var(--muted);line-height:1.55;margin:0}
.band-wrap{max-width:1060px;margin:0 auto;padding:0 20px}
.band{background:linear-gradient(165deg,var(--ink),color-mix(in srgb,var(--ink) 88%,black));color:#fff;border-radius:calc(var(--radius) + 6px);padding:52px 40px;margin-top:8px}
.band h2{color:#fff;margin-top:0}
.band h2::after{background:rgba(255,255,255,.18)}
.band .feats{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:22px;margin:26px 0 34px}
.band .feat{background:rgba(255,255,255,.06);border:1px solid rgba(255,255,255,.12);border-radius:var(--radius);padding:20px}
.band .feat .ic{color:var(--accent);display:block;margin-bottom:10px}
.band .feat .ft{font-weight:600;font-family:system-ui,sans-serif;font-size:.98rem;margin:0 0 6px}
.band .feat .fb{font-family:system-ui,sans-serif;font-size:.85rem;color:rgba(255,255,255,.72);line-height:1.55;margin:0}
.band .widget{background:#fff;border-radius:var(--radius);overflow:hidden;max-width:520px;margin:0 auto}
.band .widget-cap{font-family:system-ui,sans-serif;font-size:.8rem;letter-spacing:.08em;text-transform:uppercase;color:rgba(255,255,255,.6);text-align:center;margin:0 0 14px}
.reviews{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:18px;list-style:none;padding:0}
.reviews li{background:#fff;border:1px solid #ece7d8;border-radius:var(--radius);padding:22px}
.reviews .stars{color:var(--accent);letter-spacing:.15em;margin-bottom:10px}
.reviews .rt{font-family:system-ui,sans-serif;font-size:.92rem;line-height:1.6;color:#3a4350;margin:0 0 12px}
.reviews .ra{font-family:system-ui,sans-serif;font-size:.8rem;color:var(--muted)}
.areas{list-style:none;padding:0;display:flex;flex-wrap:wrap;gap:10px}
.areas li a{display:block;background:#fff;border:1px solid #e8e2d2;border-radius:999px;padding:10px 20px;color:var(--ink);text-decoration:none;font-family:system-ui,sans-serif;font-size:.9rem}
.areas li a:hover{border-color:var(--accent)}
dl dt{font-weight:600;margin-top:16px;font-size:1.02rem}
dl dd{margin:6px 0 0;color:#4a5560;line-height:1.6;font-family:system-ui,sans-serif;font-size:.92rem}
ul.plist{list-style:none;padding:0}
ul.plist li{padding:12px 0;border-bottom:1px solid #eee;font-family:system-ui,sans-serif}
.cta-band{background:var(--accent);color:var(--ink);text-align:center;padding:64px 24px;margin-top:72px}
.cta-band .t{font-size:2rem;margin:0 0 10px}
.cta-band .b{font-family:system-ui,sans-serif;margin:0 0 26px;opacity:.85}
.cta-band .btn{background:var(--ink);color:#fff}
.sticky{position:fixed;bottom:0;left:0;right:0;background:var(--ink);padding:12px 20px;display:flex;gap:10px;justify-content:center;z-index:50}
.sticky .btn{padding:12px 24px;font-size:.95rem}
footer{text-align:center;color:#8a8f98;font-size:.85rem;padding:36px 24px 112px;font-family:system-ui,sans-serif}
footer a{color:#8a8f98}
.gallery{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:12px;list-style:none;padding:0;margin:0 0 8px}
.gallery img{width:100%;aspect-ratio:4/3;object-fit:cover;border-radius:var(--radius);display:block}
@media (max-width:640px){.cards{grid-template-columns:1fr}.steps{grid-template-columns:1fr}.hero .brand{font-size:2.1rem}.hero .hero-inner{padding:76px 20px 68px}.band{padding:36px 22px}.stats{flex-direction:column}.stats div+div{border-left:0;border-top:1px solid #f0ebdc}}
@media (min-width:900px){.sticky{display:none}}
</style>
{{if .DesignCSS}}<style>{{.DesignCSS}}</style>{{end}}
</head>
<body class="design-{{.Design}}">
<nav class="nav">
<a class="brand" href="{{.HomeURL}}">{{.TenantName}}</a>
<div class="links">
{{if and .Services (.Show "services")}}<a href="#services">{{.D.Services}}</a>{{end}}
{{if and .Areas (.Show "areas")}}<a href="#areas">{{.D.WhereWeOperate}}</a>{{end}}
{{if and .Reviews (.Show "reviews")}}<a href="#reviews">{{.D.ReviewsHeading}}</a>{{end}}
<a href="{{.PartnersURL}}">{{.D.OurPartners}}</a>
</div>
<a class="cta" href="{{.CTA}}">{{.D.BookNow}}</a>
</nav>
<header class="hero">
{{if .HeroImageURL}}<img class="hero-bg" src="{{.HeroImageURL}}" alt="" fetchpriority="high">{{end}}
<div class="hero-inner">
<h1 class="brand">{{.Heading}}</h1>
<p class="tag">{{.Description}}</p>
{{if .ReviewCount}}<span class="rating">★ {{.ReviewAvg}} · {{.ReviewCount}} {{.D.ClientReviews}}</span>{{end}}
<div class="cta-row">
<a class="btn btn-primary" href="{{.CTA}}">{{.CTALabel}}</a>
{{if .WhatsApp}}<a class="btn btn-ghost" href="{{.WhatsApp}}">{{.D.ChatWhatsApp}}</a>{{end}}
</div>
</div>
</header>
<main>
{{if .Show "trust"}}<div class="trust">
{{if .TrustedPayment}}<span>✓ {{.D.SecurePayment}}</span>{{end}}
{{if .WhatsApp}}<span>✓ {{.D.WhatsAppConfirm}}</span>{{end}}
{{if .ReviewCount}}<span>★ {{.ReviewAvg}} ({{.ReviewCount}})</span>{{end}}
</div>{{end}}
{{if and .Stats (.Show "stats")}}<div class="stats">{{range .Stats}}<div><div class="v">{{.Value}}</div><div class="l">{{.Label}}</div></div>{{end}}</div>{{end}}
{{range .Sections}}<p class="guide">{{.}}</p>{{end}}
{{if and .Services (.Show "services")}}<h2 id="services">{{.D.Services}}</h2><ul class="cards">{{range .Services}}
<li><div class="card-top">{{if .ImageURL}}<img src="{{.ImageURL}}" alt="{{.Name}}" loading="lazy">{{else}}{{.Initial}}{{end}}</div><div class="card-body"><p class="nm">{{.Name}}</p>{{if .Desc}}<p class="ds">{{.Desc}}</p>{{end}}
<div class="card-foot">{{if .PriceLabel}}<span class="pr">{{$.D.FromPrefix}} {{.PriceLabel}}</span>{{else}}<span></span>{{end}}<a href="{{.URL}}">{{$.D.BookNow}} →</a></div></div></li>
{{end}}</ul>{{end}}
{{if and .Gallery (.Show "gallery")}}<ul class="gallery">{{range .Gallery}}<li><img src="{{.}}" alt="" loading="lazy"></li>{{end}}</ul>{{end}}
{{if .Show "steps"}}<h2>{{.D.HowItWorks}}</h2>
<ul class="steps">
<li><span class="n">1</span><p class="st">{{.D.Step1Title}}</p><p class="sb">{{.D.Step1Body}}</p></li>
<li><span class="n">2</span><p class="st">{{.D.Step2Title}}</p><p class="sb">{{.D.Step2Body}}</p></li>
<li><span class="n">3</span><p class="st">{{.D.Step3Title}}</p><p class="sb">{{.D.Step3Body}}</p></li>
</ul>{{end}}
</main>
{{if .Show "platform"}}<div class="band-wrap">
<div class="band">
<h2>{{.D.PlatformBand}}</h2>
<div class="feats">{{range .Features}}
<div class="feat"><span class="ic">{{.Icon}}</span><p class="ft">{{.Title}}</p><p class="fb">{{.Body}}</p></div>
{{end}}</div>
{{if .EmbedURL}}<p class="widget-cap">{{.D.WidgetTitle}}</p>
<div class="widget"><iframe src="{{.EmbedURL}}" style="width:100%;border:0;height:400px;display:block" title="{{.D.WidgetTitle}}" loading="lazy"></iframe></div>{{end}}
</div>
</div>{{end}}
<main>
{{if and .Reviews (.Show "reviews")}}<h2 id="reviews">{{.D.ReviewsHeading}}</h2><ul class="reviews">{{range .Reviews}}
<li><div class="stars">{{.Stars}}</div><p class="rt">“{{.Text}}”</p><span class="ra">— {{$.D.VerifiedClient}}</span></li>
{{end}}</ul>{{end}}
{{if and .Areas (.Show "areas")}}<h2 id="areas">{{.D.WhereWeOperate}}</h2><ul class="areas">{{range .Areas}}
<li><a href="{{.URL}}">{{.Name}}</a></li>
{{end}}</ul>{{end}}
{{if .Partners}}<h2>{{.D.OurPartners}}</h2><ul class="plist">{{range .Partners}}
<li>{{.Name}}{{if .Verticals}} <span style="color:var(--muted)">— {{.Verticals}}</span>{{end}}</li>
{{end}}</ul>{{end}}
{{if .FAQs}}<h2>{{.D.CommonQuestions}}</h2><dl>{{range .FAQs}}
<dt>{{.Q}}</dt><dd>{{.A}}</dd>
{{end}}</dl>{{end}}
</main>
{{if .Show "cta"}}<div class="cta-band">
<p class="t">{{.D.CTABandTitle}}</p>
<p class="b">{{.D.CTABandBody}}</p>
<a class="btn" href="{{.CTA}}">{{.CTALabel}}</a>
</div>{{end}}
<div class="sticky">
<a class="btn btn-primary" href="{{.CTA}}">{{.CTALabel}}</a>
{{if .WhatsApp}}<a class="btn btn-ghost" href="{{.WhatsApp}}">{{.D.ChatWhatsApp}}</a>{{end}}
</div>
<footer>
{{.TenantName}} — {{.D.FooterTagline}}<br>
<a href="{{.HomeURL}}">{{.TenantName}}</a> · <a href="{{.PartnersURL}}">{{.D.OurPartners}}</a> · <a href="{{.LinksURL}}">{{.D.BookNow}}</a>
</footer>
</body>
</html>`))

// editorialCSS (Phase 32): the "editorial" layout family — a CSS re-flow of
// the classic DOM, so one semantic structure serves two genuinely distinct
// designs with zero template drift. Oversized serif display, left-aligned
// hero with a masked side image, hairline rules, alternating service rows,
// baseline stats strip. Light-surface design: hero text renders in --ink
// on --bg (the image carries the visual weight, not a dark overlay).
const editorialCSS = template.CSS(`
body.design-editorial{--radius:2px}
body.design-editorial .nav{position:static;background:transparent;color:var(--ink);backdrop-filter:none;-webkit-backdrop-filter:none;border-bottom:1px solid #e3ddc9}
body.design-editorial .nav .brand{color:var(--ink)}
body.design-editorial .nav .links a{color:var(--muted)}
body.design-editorial .nav .links a:hover{color:var(--ink)}
body.design-editorial .hero{text-align:left;background:var(--bg);color:var(--ink)}
body.design-editorial .hero::after{display:none}
body.design-editorial .hero .hero-bg{left:52%;right:0;width:auto;opacity:1;-webkit-mask-image:linear-gradient(90deg,transparent,#000 24%);mask-image:linear-gradient(90deg,transparent,#000 24%)}
body.design-editorial .hero .hero-inner{max-width:1160px;padding:96px 24px 88px}
body.design-editorial .hero .brand{font-size:clamp(2.6rem,6vw,4.8rem);letter-spacing:-.02em;line-height:1.04;max-width:9em}
body.design-editorial .hero .tag{margin:18px 0 0;max-width:480px;color:var(--muted)}
body.design-editorial .rating{color:var(--ink);background:transparent}
body.design-editorial .cta-row{justify-content:flex-start}
body.design-editorial .btn-ghost{border-color:var(--ink);color:var(--ink)}
body.design-editorial .btn-ghost:hover{background:var(--ink);color:#fff}
body.design-editorial main,body.design-editorial .band-wrap{max-width:1160px}
body.design-editorial h2{font-size:.95rem}
body.design-editorial .trust{justify-content:flex-start;margin-top:0}
body.design-editorial .trust span{border:0;border-bottom:1px solid #e3ddc9;border-radius:0;box-shadow:none;background:transparent;padding:9px 4px}
body.design-editorial .stats{border:0;border-top:1px solid #e3ddc9;border-bottom:1px solid #e3ddc9;border-radius:0;background:transparent}
body.design-editorial .stats div{padding:26px 12px;text-align:left}
body.design-editorial .stats .v{font-size:2.2rem;font-weight:400}
body.design-editorial .cards{grid-template-columns:1fr;gap:0}
body.design-editorial .cards li{display:flex;background:transparent;border:0;border-top:1px solid #e3ddc9;border-radius:0}
body.design-editorial .cards li:hover{transform:none;box-shadow:none;background:#fff}
body.design-editorial .cards .card-top{flex:0 0 38%;aspect-ratio:auto;font-size:1.4rem}
body.design-editorial .cards li:nth-child(even){flex-direction:row-reverse}
body.design-editorial .cards .card-body{flex:1;padding:28px 26px;align-self:center}
body.design-editorial .cards .card-body .nm{font-size:1.5rem}
body.design-editorial .cards .card-body .ds{-webkit-line-clamp:3;font-size:.95rem}
body.design-editorial .steps li{background:transparent;border:0;border-top:2px solid var(--ink);border-radius:0;padding:20px 4px}
body.design-editorial .band{border-radius:0;padding:64px 48px}
body.design-editorial .reviews li{background:transparent;border:0;border-top:1px solid #e3ddc9;border-radius:0}
body.design-editorial .gallery{grid-template-columns:repeat(4,1fr)}
body.design-editorial .gallery img{border-radius:0}
body.design-editorial .areas li a{border-radius:0}
body.design-editorial .cta-band{text-align:left;padding:72px 48px}
@media (max-width:760px){body.design-editorial .hero .hero-bg{left:0;width:100%;opacity:.16;-webkit-mask-image:none;mask-image:none}body.design-editorial .cards li{flex-direction:column}body.design-editorial .cards li:nth-child(even){flex-direction:column}body.design-editorial .gallery{grid-template-columns:1fr 1fr}body.design-editorial .cta-band{padding:56px 24px}}
`)
