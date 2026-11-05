package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-refund/internal/httpapi"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestBrandingHTTPPermissionsAndPublicIcon(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "branding-member@test.invalid", "member")
	o := testutil.Member(t, s, a, "branding-ops@test.invalid", "operator")
	ctx := context.Background()
	ad, _ := s.NewSession(ctx, a.ID)
	mem, _ := s.NewSession(ctx, u.ID)
	ops, _ := s.NewSession(ctx, o.ID)
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	call := func(method, path, body, token, origin string, want int) (string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "text/html") // Ignore client-declared MIME.
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: token})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d expected %d", method, path, res.StatusCode, want)
		}
		return string(raw), res.Header
	}
	path := "/api/admin/site-settings/branding"
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		call(method, path, "", "", "", 401)
		call(method, path, "", mem, "", 403)
		call(method, path, "", ops, "", 403)
	}
	icon, headers := call("GET", "/api/public/brand-icon", "", "", "", 200)
	if icon != string(store.DefaultBrandIcon) || headers.Get("Content-Type") != "image/svg+xml" || headers.Get("Cache-Control") != "no-store" || !strings.Contains(headers.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("default icon headers/content invalid")
	}
	call("HEAD", "/api/public/brand-icon", "", "", "", 200)
	call("GET", "/brand-default.svg", "", "", "", 200)
	call("PUT", path, icon, ad, "https://untrusted.invalid", 403)
	call("PUT", path, strings.Repeat("x", store.MaxBrandIconBytes+1), ad, "", 413)
	call("PUT", path, `<svg><script>alert(1)</script></svg>`, ad, "", 400)
	call("PUT", path, "", ad, "", 400)
	custom := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect width="24" height="24" fill="#9a6b45"/></svg>`
	raw, _ := call("PUT", path, custom, ad, "", 200)
	var result store.SiteBranding
	if json.Unmarshal([]byte(raw), &result) != nil || !result.Customized {
		t.Fatal("missing upload result")
	}
	served, headers := call("GET", result.IconURL, "", "", "", 200)
	if !strings.Contains(served, "#9a6b45") || headers.Get("Content-Type") != "image/svg+xml" {
		t.Fatal("icon not served safely")
	}
	public, _ := call("GET", "/api/public/site", "", "", "", 200)
	var site struct {
		Branding store.SiteBranding `json:"branding"`
	}
	if json.Unmarshal([]byte(public), &site) != nil || site.Branding != result || strings.Contains(public, "icon_data") {
		t.Fatal("public branding metadata incorrect")
	}
	call("DELETE", path, "", ad, "", 200)
	icon, _ = call("GET", "/api/public/brand-icon", "", "", "", 200)
	if icon != string(store.DefaultBrandIcon) {
		t.Fatal("default icon not restored")
	}
}
