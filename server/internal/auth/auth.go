package auth

import (
	"net/http"
	"strings"
)

// Middleware requires x-api-key (or Authorization: Bearer) matching configured key.
// Paths under /ui, /health, and static assets are exempt for local UI browsing.
func Middleware(apiKey string) func(http.Handler) http.Handler {
	exemptPrefixes := []string{"/ui", "/health", "/favicon"}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/" || path == "/info" {
				// /info is probed by SDK before auth in some versions; still require key
				// for /info when keyed — but SDK sends key. Keep /info keyed.
			}
			for _, p := range exemptPrefixes {
				if path == p || strings.HasPrefix(path, p+"/") {
					next.ServeHTTP(w, r)
					return
				}
			}
			if path == "/" {
				next.ServeHTTP(w, r)
				return
			}
			got := r.Header.Get("x-api-key")
			if got == "" {
				authz := r.Header.Get("Authorization")
				if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
					got = strings.TrimSpace(authz[7:])
				}
			}
			if got == "" || got != apiKey {
				http.Error(w, `{"detail":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
