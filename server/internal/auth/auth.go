package auth

import (
	"net/http"
	"strings"
)

// Middleware requires x-api-key (or Authorization: Bearer) matching configured key.
// When apiKey is empty, all paths are open (local/dev).
// Paths under /ui (legacy SPA), /health, and /favicon are exempt so the public
// health probe and embedded UI assets work without a key. /api/ui is NOT exempt.
func Middleware(apiKey string) func(http.Handler) http.Handler {
	exemptPrefixes := []string{"/ui", "/health", "/favicon"}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}
			path := r.URL.Path
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
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"detail":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
