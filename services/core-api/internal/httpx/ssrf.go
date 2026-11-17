// SSRF egress guard (Phase 50). Any HTTP client that fetches a URL a
// tenant or end user can influence (webhook delivery targets, remote
// imports) MUST use SafeTransport/NewSafeClient: the dial hook resolves the
// host and refuses loopback, RFC-1918, link-local (incl. the 169.254.169.254
// cloud-metadata address), CGNAT, multicast and unspecified addresses before
// connecting, and dials the resolved IP directly so DNS cannot rebind
// between check and connect. Redirects re-enter the same guarded dialer, so
// a 302 to an internal address is blocked too.
//
// Operator-configured service URLs (adapters, PSPs) are exempt by design —
// they come from deployment config, not request input. Tests set
// allowPrivate to reach local httptest receivers.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// ErrSSRFBlocked is returned when an outbound target resolves to a
// non-public address.
var ErrSSRFBlocked = errors.New("ssrf: target address is not public")

// isPublicIP reports whether ip is globally routable. Unmapped so an
// IPv4-mapped IPv6 literal (::ffff:127.0.0.1) cannot smuggle a private v4
// past the checks.
func isPublicIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback(), ip.IsPrivate(), ip.IsUnspecified(),
		ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsMulticast():
		return false
	}
	if ip.Is4() {
		b := ip.As4()
		// CGNAT 100.64.0.0/10 — carrier space, never a public web target.
		if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
			return false
		}
		// 192.0.0.0/24 (IETF protocol assignments) and 240.0.0.0/4 (reserved).
		if b[0] == 192 && b[1] == 0 && b[2] == 0 {
			return false
		}
		if b[0] >= 240 {
			return false
		}
	}
	return true
}

// CheckOutboundURL validates scheme and resolved addresses for a URL about
// to be fetched. http/https only; every resolved address must be public
// (unless allowPrivate). Exported for validation-time checks; the dial-time
// guard in SafeTransport is the enforcement point.
func CheckOutboundURL(ctx context.Context, raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("ssrf: bad url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("ssrf: scheme %q not allowed", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("ssrf: empty host")
	}
	if allowPrivate {
		return nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isPublicIP(ip) {
			return ErrSSRFBlocked
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("ssrf: resolve %s: %w", host, err)
	}
	for _, a := range addrs {
		if ip, ok := netip.AddrFromSlice(a.IP); ok && !isPublicIP(ip) {
			return ErrSSRFBlocked
		}
	}
	return nil
}

// resolvePublic resolves host and returns an IP that passed the public
// checks. Dialing the returned address (instead of letting the dialer
// re-resolve) closes the DNS-rebinding TOCTOU gap.
func resolvePublic(ctx context.Context, host string, allowPrivate bool) (netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		if !allowPrivate && !isPublicIP(ip) {
			return netip.Addr{}, ErrSSRFBlocked
		}
		return ip.Unmap(), nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, a := range addrs {
		if ip, ok := netip.AddrFromSlice(a.IP); ok {
			ip = ip.Unmap()
			if allowPrivate || isPublicIP(ip) {
				return ip, nil
			}
		}
	}
	return netip.Addr{}, ErrSSRFBlocked
}

// SafeTransport returns an *http.Transport whose dialer enforces the SSRF
// egress policy above.
func SafeTransport(allowPrivate bool) *http.Transport {
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ip, err := resolvePublic(ctx, host, allowPrivate)
			if err != nil {
				return nil, err
			}
			return d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// NewSafeClient builds an *http.Client on SafeTransport with a bounded
// redirect chain (each hop re-enters the guarded dialer).
func NewSafeClient(timeout time.Duration, allowPrivate bool) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: SafeTransport(allowPrivate),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("ssrf: too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return fmt.Errorf("ssrf: redirect to scheme %q refused", req.URL.Scheme)
			}
			return nil
		},
	}
}
