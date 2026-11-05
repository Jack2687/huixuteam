package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"llm-refund/internal/httpapi"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCustomMenusRoleBoundariesAndRedirects(t *testing.T) {
	s := testutil.Database(t)
	ctx := context.Background()
	admin := testutil.Admin(t, s)
	member := testutil.Member(t, s, admin, "member-menu@test.invalid", "member")
	operator := testutil.Member(t, s, admin, "operator-menu@test.invalid", "operator")
	ad, _ := s.NewSession(ctx, admin.ID)
	mem, _ := s.NewSession(ctx, member.ID)
	ops, _ := s.NewSession(ctx, operator.ID)
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	call := func(method, path, token, body string, want int) (string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: token})
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d expected %d: %s", method, path, res.StatusCode, want, data)
		}
		return string(data), res.Header
	}
	items := []store.CustomMenu{
		{ID: "public-help", Label: "外部帮助", URL: "https://example.com/help?source=menu", Enabled: true, Visibility: "user", OpenInNewTab: true, IconSVG: `<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8"/></svg>`},
		{ID: "staff-secret", Label: "私人管理入口", URL: "https://private.example.com/admin", Enabled: true, Visibility: "admin", IconSVG: `<svg viewBox="0 0 24 24"><path d="M1 1L2 2"/></svg>`},
	}
	raw, _ := json.Marshal(items)
	for _, token := range []string{mem, ops} {
		call("PUT", "/api/admin/site-settings/custom-menus", token, string(raw), 403)
	}
	call("PUT", "/api/admin/site-settings/custom-menus", ad, string(raw), 200)
	for _, token := range []string{mem, ops} {
		body, _ := call("GET", "/api/workspace/navigation", token, "", 200)
		if strings.Contains(body, "staff-secret") || strings.Contains(body, "私人管理入口") || strings.Contains(body, "private.example.com") {
			t.Fatal("private menu leaked")
		}
		if !strings.Contains(body, `"key":"custom:public-help"`) || !strings.Contains(body, `"open_in_new_tab":true`) {
			t.Fatal("public navigation missing")
		}
		for _, path := range []string{"/api/workspace/custom-menus/staff-secret", "/api/workspace/custom-menus/staff-secret/icon", "/workspace/custom/staff-secret", "/workspace/custom/staff-secret/open"} {
			call("GET", path, token, "", 403)
		}
	}
	public, _ := call("GET", "/api/public/site", "", "", 200)
	if strings.Contains(public, "public-help") || strings.Contains(public, "staff-secret") {
		t.Fatal("custom menus exposed to anonymous visitors")
	}
	for _, path := range []string{"/api/workspace/custom-menus/public-help", "/api/workspace/custom-menus/public-help/icon", "/workspace/custom/public-help", "/workspace/custom/public-help/open"} {
		call("GET", path, "", "", 401)
	}
	for _, path := range []string{"/api/workspace/custom-menus/staff-secret", "/api/workspace/custom-menus/staff-secret/icon", "/workspace/custom/staff-secret"} {
		call("GET", path, ad, "", 200)
	}
	_, headers := call("GET", "/workspace/custom/public-help/open?url=https://attacker.invalid", mem, "", 302)
	if headers.Get("Location") != items[0].URL || headers.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(headers.Get("Cache-Control"), "no-store") {
		t.Fatal("unsafe redirect headers")
	}
	_, headers = call("GET", "/api/workspace/custom-menus/public-help/icon", mem, "", 200)
	if headers.Get("Content-Type") != "image/svg+xml" || !strings.Contains(headers.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("unsafe SVG response")
	}
	_, headers = call("GET", "/workspace/custom/public-help", mem, "", 200)
	if !strings.Contains(headers.Get("Content-Security-Policy"), "frame-src https: http:") {
		t.Fatal("embedding blocked by app CSP")
	}
	items[0].Enabled = false
	raw, _ = json.Marshal(items)
	call("PUT", "/api/admin/site-settings/custom-menus", ad, string(raw), 200)
	for _, path := range []string{"/api/workspace/custom-menus/public-help", "/api/workspace/custom-menus/public-help/icon", "/workspace/custom/public-help", "/workspace/custom/public-help/open"} {
		call("GET", path, mem, "", 404)
	}
	call("PUT", "/api/admin/site-settings/custom-menus", ad, `[]`, 200)
	call("GET", "/workspace/custom/staff-secret/open", ad, "", 404)
}
