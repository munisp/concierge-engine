package server_test

// Phase 54 tests: marketer-editable content blocks (Mautic feature-borrow
// A3) — a closed vocabulary of plain-text copy slots on the public site,
// validated on the write path, resolved per-locale at render.

import (
	"net/http"
	"strings"
	"testing"
)

func TestContentBlocksRenderAndValidate54(t *testing.T) {
	env := newEnv(t) // slug "taylor"

	// Invalid blocks never persist: unknown slot, markup, oversized copy.
	for name, blocks := range map[string]any{
		"unknown slot": map[string]any{"made_up": map[string]string{"en": "hello"}},
		"markup":       map[string]any{"hero_tagline": map[string]string{"en": "Best <b>concierge</b> ever"}},
		"oversized":    map[string]any{"cta_label": map[string]string{"en": strings.Repeat("x", 281)}},
		"bad locale":   map[string]any{"cta_label": map[string]string{"ENGLISH": "Book now"}},
	} {
		env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
			"site_config": map[string]any{"content_blocks": blocks},
		}, http.StatusUnprocessableEntity, nil)
		_, _, body := getHost(t, env.server.URL, "/site/taylor/", "", "", "")
		if strings.Contains(body, "hello") && name == "unknown slot" {
			t.Fatalf("%s: invalid block must not render", name)
		}
	}

	// Valid blocks render on the landing page.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"content_blocks": map[string]any{
				"hero_tagline": map[string]string{"en": "Chauffeurs, jets and yachts — one message away."},
				"cta_label":    map[string]string{"en": "Reserve your chauffeur"},
				"footer_note":  map[string]string{"en": "quiet luxury, arranged."},
				"announcement": map[string]string{"en": "Summer fleet: new S-Class arrivals"},
			},
		},
	}, http.StatusOK, nil)

	status, _, body := getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("landing: %d", status)
	}
	for _, want := range []string{
		"Chauffeurs, jets and yachts — one message away.",
		"Reserve your chauffeur",
		"one message away.</p>",         // hero paragraph carries the override
		`class="announce">Summer fleet`, // the bar renders when set
		"quiet luxury, arranged.",       // footer override
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("landing missing %q", want)
		}
	}

	// Meta/OG description stays engine-derived — display-only override.
	if strings.Contains(body, `og:description" content="Chauffeurs, jets`) {
		t.Fatal("content block must NOT leak into meta/OG — SEO copy stays engine-derived")
	}

	// Per-locale: a de-only block does not render on the en landing; with
	// an en default-locale entry it falls back for unconfigured locales.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"content_blocks": map[string]any{
				"hero_tagline": map[string]string{"de": "Chauffeure, Jets und Yachts."},
			},
		},
	}, http.StatusOK, nil)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	if strings.Contains(body, "Chauffeure, Jets und Yachts.") {
		t.Fatal("de-only block must not render on the en landing")
	}

	// Clearing a block restores the engine copy.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"content_blocks": map[string]any{
				"hero_tagline": map[string]string{"de": ""},
				"announcement": map[string]string{"en": ""},
			},
		},
	}, http.StatusOK, nil)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	if strings.Contains(body, `class="announce"`) {
		t.Fatal("cleared announcement must remove the bar from the DOM")
	}
}

// The editorial layout family renders the same blocks — a marketer
// override never depends on the design choice.
func TestContentBlocksEditorial54(t *testing.T) {
	env := newEnv(t)
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"design": "editorial",
			"content_blocks": map[string]any{
				"hero_tagline": map[string]string{"en": "Editorial hero override."},
				"announcement": map[string]string{"en": "Editorial announcement."},
			},
		},
	}, http.StatusOK, nil)
	_, _, body := getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	if !strings.Contains(body, "Editorial hero override.") || !strings.Contains(body, "Editorial announcement.") {
		t.Fatal("editorial design must render content blocks identically")
	}
}
