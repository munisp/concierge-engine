package server_test

// Phase 48 server tests: settlement batch endpoints end to end over HTTP,
// the partner settlement statement, the ops sweep endpoint, and callback
// authentication — when CE_PAYOUT_CALLBACK_SECRET is configured, a callback
// without the matching X-Callback-Secret header is a 403, and a batch
// transfer callback routes to the batch saga (not the single-payout one).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

// fakeMojaloop48 is the server-test variant of the scheme adapter: quotes
// lock to 2030, transfers start PENDING.
func fakeMojaloop48(t *testing.T) *httptest.Server {
	t.Helper()
	var n int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/quotes":
			n++
			_ = json.NewEncoder(w).Encode(domain.MLQuoteResponse{
				QuoteID: fmt.Sprintf("q-%d", n), FXRate: 1.0, ReceiveMinor: 9500, FeesMinor: 300,
				ExpiresAt: "2030-01-01T00:00:00Z",
			})
		case "/v1/transfers":
			n++
			_ = json.NewEncoder(w).Encode(domain.MLTransferResponse{
				TransferID: fmt.Sprintf("tr-%d", n), State: "PENDING",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// seedPartner48 onboards and vets a Mojaloop-rail partner with `payouts`
// EUR payables, via the service layer (the HTTP surface under test is the
// settlement endpoints, not partner admin).
func seedPartner48(t *testing.T, env *testEnv, payouts int64) *model.Partner {
	t.Helper()
	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Safari Wings", "payout_rail": "MOJALOOP", "payout_ref": "fsiop-safari",
	}, http.StatusCreated, &p)
	env.do("PUT", "/v1/partners/"+p.ID+"/status", env.tenantID, map[string]any{
		"status": "VETTED",
	}, http.StatusOK, nil)
	ctx := context.Background()
	for i := int64(0); i < payouts; i++ {
		if _, err := env.svc.Payouts.Create(ctx, env.tenantID, p.ID,
			fmt.Sprintf("bk-%d", i), 10000+i*5000, "EUR", fmt.Sprintf("srv-po48-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	return &p
}

func TestSettlementEndpoints48(t *testing.T) {
	adapter := fakeMojaloop48(t)
	defer adapter.Close()
	env := newEnvDeps(t, domain.Deps{MojaloopURL: adapter.URL, PublicBase: "http://core"})
	partner := seedPartner48(t, env, 3)

	// Assemble: 3 pending EUR payouts -> one batch.
	var b model.SettlementBatch
	env.do("POST", "/v1/settlements/batches", env.tenantID, map[string]any{
		"partner_id": partner.ID, "currency": "EUR", "idempotency_key": "srv-asm-1",
	}, http.StatusCreated, &b)
	if b.State != model.BatchOpen || b.AmountMinor != 10000+15000+20000 {
		t.Fatalf("batch: %+v", b)
	}
	// Replay returns the same batch.
	var replay model.SettlementBatch
	env.do("POST", "/v1/settlements/batches", env.tenantID, map[string]any{
		"partner_id": partner.ID, "currency": "EUR", "idempotency_key": "srv-asm-1",
	}, http.StatusCreated, &replay)
	if replay.ID != b.ID {
		t.Fatalf("assemble replay id = %s, want %s", replay.ID, b.ID)
	}
	// Cross-tenant access is a 404, not an existence leak.
	env.do("GET", "/v1/settlements/batches/"+b.ID, "other-tenant", nil, http.StatusNotFound, nil)

	// Quote, execute.
	env.do("POST", "/v1/settlements/batches/"+b.ID+"/quote", env.tenantID, nil, http.StatusOK, &b)
	if b.State != model.BatchQuoted || b.ReceiveMinor != 9500 {
		t.Fatalf("quoted batch: %+v", b)
	}
	env.do("POST", "/v1/settlements/batches/"+b.ID+"/execute", env.tenantID, nil, http.StatusOK, &b)
	if b.State != model.BatchInFlight || b.TransferID == "" {
		t.Fatalf("executed batch: %+v", b)
	}

	// Callback routes to the batch saga by transfer id.
	env.do("POST", "/v1/payouts/callbacks/mojaloop", env.tenantID, map[string]any{
		"transfer_id": b.TransferID, "event_id": "ev-1", "kind": "transfer.fulfilled",
	}, http.StatusOK, &b)
	if b.State != model.BatchSettled {
		t.Fatalf("settled batch: %+v", b)
	}

	// List + statement.
	var batches []model.SettlementBatch
	env.do("GET", "/v1/settlements/batches", env.tenantID, nil, http.StatusOK, &batches)
	if len(batches) != 1 {
		t.Fatalf("batches: %+v", batches)
	}
	var st domain.SettlementStatement
	env.do("GET", "/v1/partners/"+partner.ID+"/settlement-statement", env.tenantID, nil, http.StatusOK, &st)
	if len(st.Currencies) != 1 || st.Currencies[0].SettledMinor != 45000 || st.Currencies[0].SettledReceiveMinor != 9500 {
		t.Fatalf("statement: %+v", st)
	}

	// Ops sweep endpoint: nothing stale right now.
	var sweep map[string]int
	env.do("POST", "/v1/payouts/sweep", env.tenantID, nil, http.StatusOK, &sweep)
	if sweep["payouts_compensated"] != 0 || sweep["batches_compensated"] != 0 {
		t.Fatalf("sweep: %+v", sweep)
	}
}

// Callback auth: with the secret configured, no header or a wrong header is
// a 403; the right header passes through to the saga.
func TestPayoutCallbackAuth48(t *testing.T) {
	adapter := fakeMojaloop48(t)
	defer adapter.Close()
	env := newEnvDeps(t, domain.Deps{
		MojaloopURL: adapter.URL, PublicBase: "http://core", PayoutCallbackSecret: "s3cret",
	})
	partner := seedPartner48(t, env, 1)

	// Single payout through the saga over HTTP.
	var p model.Payout
	env.do("GET", "/v1/payouts", env.tenantID, nil, http.StatusOK, &[]model.Payout{})
	payouts, err := env.svc.Payouts.List(context.Background(), env.tenantID)
	if err != nil || len(payouts) != 1 {
		t.Fatalf("payouts: %v %v", payouts, err)
	}
	p = payouts[0]
	env.do("POST", "/v1/payouts/"+p.ID+"/quote", env.tenantID, nil, http.StatusOK, &p)
	env.do("POST", "/v1/payouts/"+p.ID+"/execute", env.tenantID, nil, http.StatusOK, &p)

	call := func(secret string, want int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"transfer_id": p.TransferID, "event_id": "ev-a", "kind": "transfer.fulfilled",
		})
		req, err := http.NewRequest("POST", env.server.URL+"/v1/payouts/callbacks/mojaloop", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-ID", env.tenantID)
		if secret != "" {
			req.Header.Set("X-Callback-Secret", secret)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("callback with secret %q: got %d want %d", secret, resp.StatusCode, want)
		}
	}
	call("", http.StatusForbidden)
	call("wrong", http.StatusForbidden)
	call("s3cret", http.StatusOK)

	// Unknown transfer in the right tenant is a 404 (neither saga owns it).
	body, _ := json.Marshal(map[string]any{"transfer_id": "tr-nope", "event_id": "ev-b", "kind": "transfer.fulfilled"})
	req, _ := http.NewRequest("POST", env.server.URL+"/v1/payouts/callbacks/mojaloop", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", env.tenantID)
	req.Header.Set("X-Callback-Secret", "s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown transfer: got %d", resp.StatusCode)
	}
	_ = partner
}
