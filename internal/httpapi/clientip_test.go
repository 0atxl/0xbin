package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestResolveClientIPIgnoresSpoofedForwardingFromUntrustedRemote(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.8:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	got := resolveClientIP(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got.String() != "198.51.100.8" {
		t.Fatalf("client IP = %s, want remote IP", got)
	}
}

func TestResolveClientIPUsesFirstUntrustedForwardedHop(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.10:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.8, 10.0.0.9")
	got := resolveClientIP(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got.String() != "198.51.100.8" {
		t.Fatalf("client IP = %s, want first untrusted hop", got)
	}
}

func TestResolveClientIPFallsBackOnMalformedForwarding(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.10:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.8, bad-hop")
	got := resolveClientIP(request, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got.String() != "10.0.0.10" {
		t.Fatalf("client IP = %s, want remote IP", got)
	}
}
