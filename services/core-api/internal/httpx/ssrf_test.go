package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckOutboundURLBlockedTargets(t *testing.T) {
	ctx := context.Background()
	blocked := []string{
		"http://127.0.0.1:8080/admin",
		"http://[::1]/",
		"http://10.0.0.5/internal",
		"http://192.168.1.1/",
		"http://172.16.0.9/",
		"http://169.254.169.254/latest/meta-data", // cloud metadata
		"http://100.64.1.1/",                      // CGNAT
		"http://0.0.0.0/",
		"http://[::ffff:127.0.0.1]/", // v4-mapped loopback smuggle
		"http://240.0.0.1/",          // reserved
		"ftp://example.com/file",
		"file:///etc/passwd",
		"http://",
	}
	for _, u := range blocked {
		if err := CheckOutboundURL(ctx, u, false); err == nil {
			t.Fatalf("%s must be blocked", u)
		}
	}
	// Public IP literal passes without any DNS lookup.
	if err := CheckOutboundURL(ctx, "https://93.184.216.34/", false); err != nil {
		t.Fatalf("public literal must pass: %v", err)
	}
	// allowPrivate is the explicit test/dev escape hatch.
	if err := CheckOutboundURL(ctx, "http://127.0.0.1:8080/", true); err != nil {
		t.Fatalf("allowPrivate must permit loopback: %v", err)
	}
}

func TestSafeClientRefusesLoopback(t *testing.T) {
	reached := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	guarded := NewSafeClient(3*time.Second, false)
	if _, err := guarded.Get(receiver.URL); !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("loopback delivery must fail with ErrSSRFBlocked, got %v", err)
	}
	if reached != 0 {
		t.Fatal("guarded client reached a loopback server")
	}

	plain := NewSafeClient(3*time.Second, true)
	resp, err := plain.Get(receiver.URL)
	if err != nil {
		t.Fatalf("allowPrivate client must reach the receiver: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || reached != 1 {
		t.Fatalf("status %d reached %d", resp.StatusCode, reached)
	}
}
