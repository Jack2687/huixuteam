package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"llm-refund/internal/httpapi"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"llm-refund/webui"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSiteSettingsHTTPBoundaryAndEmailFlow(t *testing.T) {
	s := testutil.Database(t)
	admin := testutil.Admin(t, s)
	member := testutil.Member(t, s, admin, "member@test.invalid", "member")
	operator := testutil.Member(t, s, admin, "ops@test.invalid", "operator")
	ctx := context.Background()
	ad, _ := s.NewSession(ctx, admin.ID)
	mem, _ := s.NewSession(ctx, member.ID)
	ops, _ := s.NewSession(ctx, operator.ID)
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	call := func(method, path, body, token string, want int) string {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: token})
		}
		res, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d wanted %d: %s", method, path, res.StatusCode, want, raw)
		}
		return string(raw)
	}
	for _, token := range []string{mem, ops} {
		call("GET", "/api/admin/site-settings", "", token, 403)
		call("PUT", "/api/admin/site-settings/features", `{}`, token, 403)
		call("PUT", "/api/admin/site-settings/security", `{}`, token, 403)
		call("PUT", "/api/admin/site-settings/mail", `{}`, token, 403)
		call("GET", "/api/admin/site-settings/mail/logs", "", token, 403)
		call("POST", "/api/admin/site-settings/mail/test", `{"email":"x@test.invalid"}`, token, 403)
		call("POST", "/api/admin/site-settings/mail/connection", `{}`, token, 403)
	}
	public := call("GET", "/api/public/site", "", "", 200)
	nav := call("GET", "/api/workspace/navigation", "", mem, 200)
	opsNav := call("GET", "/api/workspace/navigation", "", ops, 200)
	for _, raw := range []string{public, nav, opsNav} {
		if strings.Contains(raw, "admin") || strings.Contains(raw, "运营控制台") || strings.Contains(raw, "smtp") || strings.Contains(raw, "password_configured") {
			t.Fatal("non-admin projection exposed admin configuration")
		}
	}
	if !strings.Contains(call("GET", "/api/workspace/navigation", "", ad, 200), "运营控制台") {
		t.Fatal("admin menu missing")
	}
	for _, token := range []string{mem, ops} {
		if strings.Contains(call("GET", "/api/auth/me", "", token, 200), "admin.") {
			t.Fatal("non-admin user received admin permissions")
		}
	}
	for _, path := range []string{"/workspace/admin", "/workspace/admin/", "/workspace/admin/settings"} {
		for _, method := range []string{"GET", "HEAD"} {
			call(method, path, "", ad, 200)
			call(method, path, "", mem, 403)
			call(method, path, "", ops, 403)
			call(method, path, "", "", 401)
		}
	}
	cfg, e := s.SiteSettings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Features["developer"] = false
	cfg.Features["refunds"] = false
	cfg.Features["announcements"] = false
	raw, _ := json.Marshal(cfg.Features)
	call("PUT", "/api/admin/site-settings/features", string(raw), ad, 200)
	for _, path := range []string{"/api/keys", "/api/refunds", "/api/public/announcements", "/developers/reference", "/open/v1/requests", "/workspace/refunds"} {
		call("GET", path, "", mem, 404)
	}
	call("POST", "/api/refunds", `{}`, mem, 404)
	assets, e := fs.ReadDir(webui.Static, "static/assets")
	if e != nil {
		t.Fatal(e)
	}
	adminSiteFound, featureFound := false, false
	for _, asset := range assets {
		name := asset.Name()
		path := "/assets/" + name
		if strings.HasPrefix(name, "admin-") {
			call("GET", path, "", mem, 403)
			call("GET", path, "", ops, 403)
			call("GET", path, "", "", 401)
			call("GET", path, "", ad, 200)
		}
		if strings.HasPrefix(name, "admin-site-") {
			adminSiteFound = true
			call("GET", path, "", ops, 403)
			call("GET", path, "", ad, 200)
		}
		if strings.HasPrefix(name, "feature-refunds-") {
			featureFound = true
			call("GET", path, "", mem, 404)
		}
	}
	if !adminSiteFound || !featureFound {
		t.Fatal("build feature/admin modules before tests")
	}
	// Revoking an administrator role invalidates its existing session. A new
	// session must use the reduced role for navigation, pages, APIs and assets.
	delegated := testutil.Member(t, s, admin, "former-admin@test.invalid", "admin")
	oldSession, e := s.NewSession(ctx, delegated.ID)
	if e != nil {
		t.Fatal(e)
	}
	call("GET", "/api/admin/overview", "", oldSession, 200)
	if e = s.AdminUpdateUser(ctx, admin.ID, delegated.ID, store.UserInput{Name: delegated.Name, Role: "operator", ServiceFee: delegated.ServiceFee}); e != nil {
		t.Fatal(e)
	}
	call("GET", "/api/admin/overview", "", oldSession, 401)
	newSession, e := s.NewSession(ctx, delegated.ID)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(call("GET", "/api/workspace/navigation", "", newSession, 200), "运营控制台") {
		t.Fatal("demoted user retained console navigation")
	}
	call("GET", "/workspace/admin", "", newSession, 403)
	call("GET", "/api/admin/overview", "", newSession, 403)
	for _, asset := range assets {
		if strings.HasPrefix(asset.Name(), "admin-") {
			call("GET", "/assets/"+asset.Name(), "", newSession, 403)
		}
	}
	cfg.Features["refunds"] = true
	raw, _ = json.Marshal(cfg.Features)
	call("PUT", "/api/admin/site-settings/features", string(raw), ad, 200)
	call("GET", "/api/refunds", "", mem, 200)
	// Public auth endpoints must enforce settings without trusting the UI.
	call("POST", "/api/auth/register", `{"email":"blocked@test.invalid","name":"User","password":"LongTestPassword123"}`, "", 403)
	call("PUT", "/api/admin/site-settings/security", `{"registration_enabled":true,"email_verification_enabled":true,"email_domains":[]}`, ad, 400)
	sink := testutil.SMTP(t)
	mail := store.MailInput{SiteMail: store.SiteMail{Host: sink.Host, Port: sink.Port, FromEmail: "mail@test.invalid", FromName: "QA", Encryption: "none"}}
	raw, _ = json.Marshal(mail)
	call("PUT", "/api/admin/site-settings/mail", string(raw), ad, 200)
	call("POST", "/api/admin/site-settings/mail/connection", `{}`, ad, 200)
	call("PUT", "/api/admin/site-settings/security", `{"registration_enabled":true,"email_verification_enabled":true,"email_domains":["test.invalid"]}`, ad, 200)
	call("POST", "/api/auth/email-code", `{"email":"outside@invalid.example","purpose":"register"}`, "", 400)
	call("POST", "/api/auth/email-code", `{"email":"fresh@test.invalid","purpose":"register"}`, "", 202)
	if worked, e := s.DeliverOneMail(ctx); e != nil || !worked {
		t.Fatal(worked, e)
	}
	code := sink.Code(t)
	raw, _ = json.Marshal(map[string]string{"email": "fresh@test.invalid", "name": "Fresh", "password": "LongTestPassword123", "code": code, "role": "admin"})
	call("POST", "/api/auth/register", string(raw), "", 400)
	raw, _ = json.Marshal(map[string]string{"email": "fresh@test.invalid", "name": "Fresh", "password": "LongTestPassword123", "code": code})
	call("POST", "/api/auth/register", string(raw), "", 201)
	resetReq := `{"email":"fresh@test.invalid","purpose":"reset"}`
	call("POST", "/api/auth/email-code", resetReq, "", 202)
	if worked, e := s.DeliverOneMail(ctx); e != nil || !worked {
		t.Fatal(worked, e)
	}
	reset := sink.Code(t)
	raw, _ = json.Marshal(map[string]string{"email": "fresh@test.invalid", "password": "ChangedPassword123", "code": reset})
	call("POST", "/api/auth/reset-password", string(raw), "", 200)
	call("POST", "/api/auth/login", `{"email":"fresh@test.invalid","password":"ChangedPassword123"}`, "", 200)
	logs := call("GET", "/api/admin/site-settings/mail/logs", "", ad, 200)
	if strings.Contains(logs, code) || strings.Contains(logs, reset) {
		t.Fatal("code leaked over HTTP")
	}
}
