package httpapi

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"llm-refund/webui"
)

func TestRiskTrustedSourceParsing(t *testing.T) {
	cases := []struct {
		peer, xff string
		trust     []string
		ip        string
		verified  bool
	}{
		{"172.18.0.1:42", "1.1.1.1", nil, "172.18.0.1", false},
		{"198.51.100.3:42", "1.1.1.1", []string{"172.18.0.0/16"}, "198.51.100.3", true},
		{"172.18.0.1:42", "1.1.1.1, 198.51.100.2", []string{"172.18.0.0/16"}, "198.51.100.2", true},
		{"172.18.0.1:42", "198.51.100.2, 10.0.0.2", []string{"172.18.0.0/16", "10.0.0.2"}, "198.51.100.2", true},
		{"172.18.0.1:42", "bad header", []string{"172.18.0.0/16"}, "172.18.0.1", false},
		{"[::1]:42", "203.0.113.7", []string{"::1"}, "203.0.113.7", true},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/api/auth/me", nil)
		r.RemoteAddr = c.peer
		r.Header.Set("X-Forwarded-For", c.xff)
		r.Header.Set("CF-Connecting-IP", "8.8.8.8")
		ip, verified := riskSource(r, c.trust)
		if ip != c.ip || verified != c.verified {
			t.Fatalf("source %+v got %s %v", c, ip, verified)
		}
	}
}

func TestRiskHTTPPermissionsControlsAndPrivacy(t *testing.T) {
	db := testutil.Database(t)
	a := testutil.Admin(t, db)
	u := testutil.Member(t, db, a, "risk@test.invalid", "member")
	o := testutil.Member(t, db, a, "operator@test.invalid", "operator")
	ctx := context.Background()
	h := New(db, false)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	admin, err := db.NewSession(ctx, a.ID)
	must(err)
	member, err := db.NewSession(ctx, u.ID)
	must(err)
	operator, err := db.NewSession(ctx, o.ID)
	must(err)
	key, err := db.CreateKey(ctx, u.ID, "Customer key")
	must(err)
	request := func(method, path, cookie, key string, body any, want int, origin string) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "refund_session", Value: cookie})
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	for _, path := range []string{"/api/admin/risk-settings", "/api/admin/risk-monitor", "/api/admin/risk-customers"} {
		request("GET", path, "", "", nil, 401, "")
		request("GET", path, member, "", nil, 403, "")
		request("GET", path, operator, "", nil, 403, "")
		request("GET", path, "", key, nil, 401, "")
		request("GET", path, admin, "", nil, 200, "")
	}
	cfg, err := db.RiskSettings(ctx)
	must(err)
	request("PUT", "/api/admin/risk-settings", admin, "", cfg, 403, "https://different.invalid")
	cfg.Rules["query"] = store.RiskRule{Mode: "enforce", Limit: 2, WindowSeconds: 60, CooldownSeconds: 60}
	request("PUT", "/api/admin/risk-settings", admin, "", cfg, 200, "")
	for i := 0; i < 2; i++ {
		request("GET", "/open/v1/requests", "", key, nil, 200, "")
	}
	// A second API key cannot bypass the shared customer query bucket.
	key2, err := db.CreateKey(ctx, u.ID, "Another key")
	must(err)
	request("GET", "/open/v1/requests", "", key2, nil, 429, "")
	h = New(db, false)
	request("GET", "/open/v1/requests", "", key, nil, 429, "")
	request("GET", "/api/auth/me", operator, "", nil, 200, "") // another customer unaffected
	m, err := db.RiskMonitor(ctx, store.RiskMonitorFilter{})
	must(err)
	if len(m.Restrictions) != 1 {
		t.Fatal("missing restriction", m.Restrictions)
	}
	request("POST", "/api/admin/risk-limits/"+m.Restrictions[0].ID+"/release", admin, "", nil, 200, "")
	request("GET", "/open/v1/requests", "", key, nil, 200, "")
	request("PUT", "/api/admin/risk-customers/"+u.ID, admin, "", store.RiskUserControl{Paused: true, Limits: map[string]int{"query": 10}}, 200, "")
	must(db.Credit(ctx, u.Email, 3000, "test"))
	body := map[string]any{"authorized": true, "accounts": []map[string]string{{"email": "child@test.invalid", "password": "private-canary-password", "totp_secret": "JBSWY3DPEHPK3PXP"}}}
	// API requires an idempotency key. Pause is enforced transactionally after validation.
	r := httptest.NewRequest("POST", "/open/v1/requests", nil)
	raw, _ := json.Marshal(body)
	r = httptest.NewRequest("POST", "/open/v1/requests", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Idempotency-Key", "risk-paused")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("paused submission", w.Code, w.Body.String())
	}
	request("GET", "/open/v1/requests", "", key, nil, 200, "")
	// Administrators can still load their identity and the recovery UI while limited.
	request("GET", "/api/dashboard", admin, "", nil, 200, "")
	request("GET", "/api/dashboard", admin, "", nil, 200, "")
	request("GET", "/api/dashboard", admin, "", nil, 429, "")
	request("GET", "/api/auth/me", admin, "", nil, 200, "")
	output := request("GET", "/api/admin/risk-monitor", admin, "", nil, 200, "").Body.String()
	for _, private := range []string{key, key2, "private-canary-password", "JBSWY3DPEHPK3PXP", "export_and_deactivate", "lease_token", "credentials", "Authorization"} {
		if strings.Contains(output, private) {
			t.Fatal("private data exposed", private)
		}
	}
	// Static privileged chunks require the same admin permission, including CSS.
	entries, err := fs.ReadDir(webui.Static, "static/assets")
	must(err)
	assets := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "admin-risk-") {
			assets++
			path := "/assets/" + entry.Name()
			request("GET", path, "", "", nil, 401, "")
			request("GET", path, member, "", nil, 403, "")
			request("GET", path, operator, "", nil, 403, "")
			request("GET", path, admin, "", nil, 200, "")
		}
	}
	if assets < 2 {
		t.Fatal("risk assets not built")
	}
	identity, err := db.RiskKeyIdentity(ctx, u.ID, key)
	must(err)
	request("POST", "/api/admin/risk-keys/"+identity.ID+"/revoke", admin, "", nil, 200, "")
	request("GET", "/open/v1/requests", "", key, nil, 401, "")
	request("GET", "/api/admin/risk-customers/not-found", admin, "", nil, 404, "")
}
