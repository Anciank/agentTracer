package ui

import (
	"net/http"
	"strings"
)

// PrefersHTML reports whether the client wants the SPA shell rather than JSON.
func PrefersHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "text/html") {
		return true
	}
	return r.Header.Get("Sec-Fetch-Dest") == "document"
}

// ServeIndex writes the SPA shell (index.html).
func ServeIndex(w http.ResponseWriter, _ *http.Request) {
	serveSPA(w)
}

// ServeSPA is the catch-all for client-side routes under /ui/* (never 404 the shell).
func ServeSPA(w http.ResponseWriter, _ *http.Request) {
	serveSPA(w)
}

func serveSPA(w http.ResponseWriter) {
	data, err := Static.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "ui missing", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// ServeLogo serves static/logo.svg.
func ServeLogo(w http.ResponseWriter, _ *http.Request) {
	data, _ := Static.ReadFile("static/logo.svg")
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write(data)
}

// ServeFavicon serves static/favicon.svg.
func ServeFavicon(w http.ResponseWriter, _ *http.Request) {
	data, _ := Static.ReadFile("static/favicon.svg")
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write(data)
}

// ServeAsset serves hashed Vite assets under static/assets/.
func ServeAsset(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	if p == "" || strings.Contains(p, "..") {
		http.NotFound(w, r)
		return
	}
	data, err := Static.ReadFile("static/assets/" + p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(p, ".js"):
		w.Header().Set("Content-Type", "application/javascript")
	case strings.HasSuffix(p, ".css"):
		w.Header().Set("Content-Type", "text/css")
	case strings.HasSuffix(p, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(p, ".map"):
		w.Header().Set("Content-Type", "application/json")
	case strings.HasSuffix(p, ".woff2"):
		w.Header().Set("Content-Type", "font/woff2")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(data)
}

// HTMLOr serves the SPA shell when the client prefers HTML; otherwise runs json.
func HTMLOr(json http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if PrefersHTML(r) {
			serveSPA(w)
			return
		}
		json(w, r)
	}
}

// Mount registers SPA static routes (index, assets, logo, favicon, catch-all).
// Call after more-specific /ui/projects and /ui/traces JSON routes.
func Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/logo.svg", ServeLogo)
	mux.HandleFunc("GET /ui/favicon.svg", ServeFavicon)
	mux.HandleFunc("GET /ui/assets/{path...}", ServeAsset)
	mux.HandleFunc("GET /ui", ServeIndex)
	mux.HandleFunc("GET /ui/{$}", ServeIndex)
	mux.HandleFunc("GET /ui/{path...}", ServeSPA)
}
