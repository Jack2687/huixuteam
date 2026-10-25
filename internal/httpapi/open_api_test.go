package httpapi_test

import (
	"encoding/json"
	"io"
	"llm-refund/internal/httpapi"
	"llm-refund/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHuixuOpenAPICompleteLifecycleAndRetiredRoutes(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "api-client@test.invalid", "member")
	if err := s.Credit(t.Context(), u.Email, 10000, "fixture"); err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateKey(t.Context(), u.ID, "api fixture")
	if err != nil {
		t.Fatal(err)
	}
	other := testutil.Member(t, s, a, "api-other@test.invalid", "member")
	otherKey, err := s.CreateKey(t.Context(), other.ID, "other fixture")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	call := func(method, path, bearer, body string, want int) []byte {
		t.Helper()
		req, e := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "huixu-public-api-test")
		// Bearer calls may originate from a different host; cookies are not used.
		req.Header.Set("Origin", "https://client.example.test")
		resp, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s = %d wanted %d: %s", method, path, resp.StatusCode, want, raw)
		}
		return raw
	}
	base := "/open/v1/requests"
	call("GET", base, "", "", 401)
	for _, path := range []string{"/v1/refunds", "/v1/refunds/old-id", "/v1/refunds/old-id/cancel", "/open/v1/not-a-route"} {
		call("GET", path, key, "", 404)
	}
	body := `{"authorized":true,"accounts":[{"email":"fixture-refund@test.invalid","password":"private-fixture-password","totp_secret":"JBSWY3DPEHPK3PXP"}]}`
	var result struct {
		IDs      []string `json:"ids"`
		Replayed bool     `json:"replayed"`
	}
	if e := json.Unmarshal(call("POST", base, key, body, 202), &result); e != nil || len(result.IDs) != 1 {
		t.Fatal("submit failed", e)
	}
	id := result.IDs[0]
	if e := json.Unmarshal(call("POST", base, key, body, 202), &result); e != nil || !result.Replayed || result.IDs[0] != id {
		t.Fatal("idempotency changed", e)
	}
	call("GET", base+"/"+id, otherKey, "", 404)
	for _, path := range []string{base, base + "/" + id} {
		raw := call("GET", path, key, "", 200)
		if strings.Contains(string(raw), "private-fixture-password") || strings.Contains(string(raw), "JBSWY3DPEHPK3PXP") {
			t.Fatal("credential leaked")
		}
	}
	call("POST", base+"/"+id+"/withdraw", key, "", 200)
	call("POST", base+"/"+id+"/withdraw", key, "", 200)
	u, err = s.User(t.Context(), u.ID)
	if err != nil || u.Frozen != 0 || u.Balance != 10000 {
		t.Fatal("withdrawal balance incorrect", u, err)
	}
}
