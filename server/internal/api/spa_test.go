package api

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Anciank/agentTracer/server/internal/config"
)

func TestUISPAServesIndexAndAssets(t *testing.T) {
	srv := New(config.Config{CORSOrigins: []string{"*"}}, nil)
	h := srv.Handler()

	cases := []struct {
		path   string
		wantCT string
		want   string
	}{
		{"/ui", "text/html", "<!DOCTYPE html>"},
		{"/ui/", "text/html", "agentTracer"},
		{"/ui/p/support", "text/html", "<!DOCTYPE html>"},
		{"/ui/traces", "text/html", "<!DOCTYPE html>"},
		{"/ui/traces/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "text/html", "<!DOCTYPE html>"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", c.path, nil)
		req.Header.Set("Accept", "text/html")
		req.Header.Set("Sec-Fetch-Dest", "document")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 200 {
			body := rr.Body.String()
			if len(body) > 80 {
				body = body[:80]
			}
			t.Fatalf("%s: status %d body=%s", c.path, rr.Code, body)
		}
		ct := rr.Header().Get("Content-Type")
		if !strings.Contains(ct, c.wantCT) {
			t.Fatalf("%s: content-type %q", c.path, ct)
		}
		if !strings.Contains(rr.Body.String(), c.want) {
			t.Fatalf("%s: body missing %q", c.path, c.want)
		}
	}

	req := httptest.NewRequest("GET", "/ui/logo.svg", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("logo: %d", rr.Code)
	}

	req = httptest.NewRequest("GET", "/ui/", nil)
	req.Header.Set("Accept", "text/html")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	body := rr.Body.String()
	re := regexp.MustCompile(`/ui/assets/([A-Za-z0-9._-]+)`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		snip := body
		if len(snip) > 200 {
			snip = snip[:200]
		}
		t.Fatalf("index missing /ui/assets/ ref: %s", snip)
	}
	asset := m[1]
	req = httptest.NewRequest("GET", "/ui/assets/"+asset, nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("asset %s: %d", asset, rr.Code)
	}
}
