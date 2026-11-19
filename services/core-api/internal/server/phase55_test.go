package server_test

// Phase 55 server tests: industry pack landing pages (resolvability
// honesty, per-locale pitch, attribution-stamped CTA, sitemap listing)
// and the one-click launch endpoint (vertical-derived segment, campaign
// naming, multi-vertical notice).

import (
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

func seedPackFixture55(t *testing.T, env *testEnv) (jetID, yachtID string) {
	t.Helper()
	var jet model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "PRIVATE_JETS", Name: "Light Jet Charter",
		BaseMinor: 900000, Currency: "EUR", AutoQuotable: true,
	}, http.StatusCreated, &jet)
	var yacht model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "YACHTS", Name: "Day Yacht Charter",
		BaseMinor: 450000, Currency: "EUR", AutoQuotable: true,
	}, http.StatusCreated, &yacht)
	return jet.ID, yacht.ID
}

func TestIndustryPackPage55(t *testing.T) {
	env := newEnv(t)
	jetID, _ := seedPackFixture55(t, env)

	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"packs": []map[string]any{{
				"slug": "f1-weekend", "name": "F1 Race Weekend",
				"service_ids": []string{jetID},
				"pitch":       map[string]string{"en": "Cars, jets and paddock — one message."},
				"active":      true,
			}},
		},
	}, http.StatusOK, nil)

	status, _, body := getHost(t, env.server.URL, "/site/taylor/packs/f1-weekend", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("pack page: %d", status)
	}
	for _, want := range []string{
		"F1 Race Weekend",
		"Cars, jets and paddock — one message.",
		"Light Jet Charter",            // real catalog service
		"utm_campaign=pack-f1-weekend", // attribution-stamped CTA
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("pack page missing %q", want)
		}
	}

	// Inactive pack → 404 (no shell page).
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"packs": []map[string]any{{
				"slug": "f1-weekend", "name": "F1 Race Weekend",
				"service_ids": []string{jetID}, "active": false,
			}},
		},
	}, http.StatusOK, nil)
	status, _, _ = getHost(t, env.server.URL, "/site/taylor/packs/f1-weekend", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("inactive pack must 404, got %d", status)
	}

	// Dangling services → nothing bookable → 404.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"packs": []map[string]any{{
				"slug": "ghost", "name": "Ghost Pack",
				"service_ids": []string{"deleted-service"}, "active": true,
			}},
		},
	}, http.StatusOK, nil)
	status, _, _ = getHost(t, env.server.URL, "/site/taylor/packs/ghost", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("pack with no bookable services must 404, got %d", status)
	}

	// Sitemap lists the resolvable pack only.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"packs": []map[string]any{
				{"slug": "f1-weekend", "name": "F1 Race Weekend", "service_ids": []string{jetID}, "active": true},
				{"slug": "ghost", "name": "Ghost Pack", "service_ids": []string{"deleted-service"}, "active": true},
			},
		},
	}, http.StatusOK, nil)
	status, _, sm := getHost(t, env.server.URL, "/site/taylor/sitemap.xml", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("sitemap: %d", status)
	}
	if !strings.Contains(sm, "/packs/f1-weekend") {
		t.Fatal("sitemap must list the resolvable pack")
	}
	if strings.Contains(sm, "/packs/ghost") {
		t.Fatal("sitemap must NOT list an unresolvable pack")
	}
}

func TestPackLaunch55(t *testing.T) {
	env := newEnv(t)
	jetID, yachtID := seedPackFixture55(t, env)

	// Single-vertical pack: the segment derives the vertical rule.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"packs": []map[string]any{{
				"slug": "jet-set", "name": "Jet Set",
				"service_ids": []string{jetID}, "active": true,
			}},
		},
	}, http.StatusOK, nil)
	var out struct {
		Campaign string `json:"campaign"`
		Notice   string `json:"notice"`
		PackURL  string `json:"pack_url"`
		Segment  struct {
			Vertical string `json:"vertical"`
		} `json:"segment"`
		Result struct {
			Matched int `json:"matched"`
		} `json:"result"`
	}
	env.do("POST", "/v1/packs/jet-set/launch", env.tenantID, map[string]any{
		"body": "Your jet is waiting.",
	}, http.StatusOK, &out)
	if out.Campaign != "pack-jet-set" || out.PackURL != "/site/taylor/packs/jet-set" {
		t.Fatalf("campaign naming + pack url: %+v", out)
	}
	if out.Segment.Vertical != "PRIVATE_JETS" {
		t.Fatalf("single-vertical pack derives the vertical rule: %+v", out.Segment)
	}
	if out.Notice != "" {
		t.Fatalf("no notice expected for a single-vertical pack: %q", out.Notice)
	}

	// Multi-vertical pack: honest notice, no invented vertical.
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"packs": []map[string]any{{
				"slug": "grand-tour", "name": "Grand Tour",
				"service_ids": []string{jetID, yachtID}, "active": true,
			}},
		},
	}, http.StatusOK, nil)
	out = struct {
		Campaign string `json:"campaign"`
		Notice   string `json:"notice"`
		PackURL  string `json:"pack_url"`
		Segment  struct {
			Vertical string `json:"vertical"`
		} `json:"segment"`
		Result struct {
			Matched int `json:"matched"`
		} `json:"result"`
	}{}
	env.do("POST", "/v1/packs/grand-tour/launch", env.tenantID, map[string]any{
		"body": "The grand tour awaits.",
	}, http.StatusOK, &out)
	if out.Segment.Vertical != "" || !strings.Contains(out.Notice, "spans") {
		t.Fatalf("multi-vertical pack must say so: %+v", out)
	}

	// Unknown pack → 404.
	env.do("POST", "/v1/packs/nope/launch", env.tenantID, map[string]any{
		"body": "x",
	}, http.StatusNotFound, nil)
}
