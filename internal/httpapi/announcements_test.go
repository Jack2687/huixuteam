package httpapi_test

import (
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

func TestAnnouncementsPublicationPermissionsAndAudit(t *testing.T) {
	s := testutil.Database(t)
	admin := testutil.Admin(t, s)
	member := testutil.Member(t, s, admin, "bulletin@test.invalid", "member")
	operator := testutil.Member(t, s, admin, "bulletin-ops@test.invalid", "operator")
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	session := func(u store.User) string {
		v, e := s.NewSession(t.Context(), u.ID)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	ad, mem, ops := session(admin), session(member), session(operator)
	call := func(method, path, token, body string, want int) []byte {
		t.Helper()
		req, e := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: token})
		}
		resp, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: status %d wanted %d: %s", method, path, resp.StatusCode, want, raw)
		}
		return raw
	}
	draft := `{"title":"待发布内容","content":"私人草稿\n<script>unsafe()</script>","category":"notice","published":false,"pinned":false}`
	call("GET", "/api/announcements", "", "", 401)
	call("GET", "/api/public/announcements", "", "", 200)
	call("GET", "/api/admin/announcements", "", "", 401)
	call("POST", "/api/admin/announcements", "", draft, 401)
	for _, cookie := range []string{mem, ops} {
		call("GET", "/api/admin/announcements", cookie, "", 403)
		call("POST", "/api/admin/announcements", cookie, draft, 403)
	}
	raw := call("POST", "/api/admin/announcements", ad, draft, 200)
	var id struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal(raw, &id); e != nil || id.ID == "" {
		t.Fatal("no announcement ID", e)
	}
	for _, cookie := range []string{ad, mem, ops} {
		raw = call("GET", "/api/announcements?include_drafts=true", cookie, "", 200)
		if strings.Contains(string(raw), "待发布内容") {
			t.Fatal("draft leaked to reader feed")
		}
	}
	raw = call("GET", "/api/public/announcements?include_drafts=true&published=false", "", "", 200)
	if strings.Contains(string(raw), "待发布内容") || strings.Contains(string(raw), "私人草稿") {
		t.Fatal("anonymous feed exposed draft")
	}
	for _, cookie := range []string{mem, ops} {
		call("PUT", "/api/admin/announcements/"+id.ID, cookie, draft, 403)
		call("DELETE", "/api/admin/announcements/"+id.ID, cookie, "", 403)
	}
	call("PUT", "/api/admin/announcements/"+id.ID, ad, `{"title":"","content":"x","category":"notice"}`, 400)
	call("PUT", "/api/admin/announcements/"+id.ID, ad, `{"title":"x","content":"x","category":"invalid"}`, 400)
	published := strings.ReplaceAll(strings.ReplaceAll(draft, `"published":false`, `"published":true`), `"pinned":false`, `"pinned":true`)
	call("PUT", "/api/admin/announcements/"+id.ID, ad, published, 200)
	call("POST", "/api/admin/announcements", ad, `{"title":"另一条公告","content":"正文","category":"update","published":true,"pinned":false}`, 200)
	var feed struct {
		Items []store.Announcement `json:"items"`
		Total int                  `json:"total"`
	}
	if e := json.Unmarshal(call("GET", "/api/announcements?size=1", mem, "", 200), &feed); e != nil {
		t.Fatal(e)
	}
	if feed.Total != 2 || len(feed.Items) != 1 || feed.Items[0].ID != id.ID || feed.Items[0].PublishedAt == nil {
		t.Fatalf("publication/pin/pagination mismatch: %+v", feed)
	}
	if err := json.Unmarshal(call("GET", "/api/public/announcements?size=1", "", "", 200), &feed); err != nil || feed.Total != 2 || len(feed.Items) != 1 || feed.Items[0].ID != id.ID {
		t.Fatal("anonymous publication ordering mismatch", err)
	}
	call("PUT", "/api/admin/announcements/"+id.ID, ad, draft, 200)
	raw = call("GET", "/api/announcements", mem, "", 200)
	if strings.Contains(string(raw), id.ID) {
		t.Fatal("withdrawn item still visible")
	}
	if strings.Contains(string(call("GET", "/api/public/announcements", "", "", 200)), id.ID) {
		t.Fatal("withdrawn announcement exposed anonymously")
	}
	call("DELETE", "/api/admin/announcements/"+id.ID, ad, "", 200)
	call("DELETE", "/api/admin/announcements/"+id.ID, ad, "", 404)
	var count int
	if e := s.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_logs WHERE target_id=$1 AND action LIKE 'announcement.%'", id.ID).Scan(&count); e != nil || count != 4 {
		t.Fatal("audit missing", count, e)
	}
}
