package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Anciank/agentTracer/server/internal/config"
)

func TestRootRedirectsBrowsersKeepsJSONForAPI(t *testing.T) {
	srv := New(config.Config{CORSOrigins: []string{"*"}}, nil)
	h := srv.Handler()

	t.Run("browser Accept redirects to /ui", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusFound {
			t.Fatalf("status=%d want 302", rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != "/ui" {
			t.Fatalf("Location=%q want /ui", loc)
		}
	})

	t.Run("application/json keeps JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d want 200", rr.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json: %v body=%s", err, rr.Body.String())
		}
		if body["service"] != "agenttracer" {
			t.Fatalf("service=%v", body["service"])
		}
		if body["version"] != ServerVersion {
			t.Fatalf("version=%v", body["version"])
		}
	})

	t.Run("no Accept keeps JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d want 200", rr.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("json: %v body=%s", err, rr.Body.String())
		}
		if body["service"] != "agenttracer" {
			t.Fatalf("service=%v", body["service"])
		}
	})

	t.Run("star Accept without text/html keeps JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", "*/*")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d want 200", rr.Code)
		}
	})

	t.Run("HEAD with browser Accept redirects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodHead, "/", nil)
		req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusFound {
			t.Fatalf("status=%d want 302", rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != "/ui" {
			t.Fatalf("Location=%q want /ui", loc)
		}
	})

	t.Run("HEAD without Accept keeps 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodHead, "/", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d want 200", rr.Code)
		}
	})
}
