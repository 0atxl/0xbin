package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientIPKey struct{}

func clientIP(next http.Handler, trusted []netip.Prefix) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := resolveClientIP(r, trusted)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip.String())))
	})
}

func clientIPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}

func resolveClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	remote, ok := remoteIP(r.RemoteAddr)
	if !ok || !isTrusted(remote, trusted) {
		return remote
	}
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return remote
	}
	hops := strings.Split(forwarded, ",")
	parsed := make([]netip.Addr, len(hops))
	for index, hop := range hops {
		ip, err := netip.ParseAddr(strings.TrimSpace(hop))
		if err != nil {
			return remote
		}
		parsed[index] = ip.Unmap()
	}
	for index := len(parsed) - 1; index >= 0; index-- {
		if !isTrusted(parsed[index], trusted) {
			return parsed[index]
		}
	}
	return parsed[0]
}

func remoteIP(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

func isTrusted(ip netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
