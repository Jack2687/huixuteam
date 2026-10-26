package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"llm-refund/internal/httpapi"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestPageAndAPISharePersistentQueueWithoutAvailableMother(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "queue@test.invalid", "member")
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Credit(ctx, u.Email, 9000, "fixture"))
	cookie, err := s.NewSession(ctx, u.ID)
	must(err)
	key, err := s.CreateKey(ctx, u.ID, "queue fixture")
	must(err)
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	// Both entry points arrive together before any mother is configured.
	var wg sync.WaitGroup
	for i, path := range []string{"/api/refunds", "/open/v1/requests"} {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			body := fmt.Sprintf(`{"authorized":true,"accounts":[{"email":"queue%d@test.invalid","password":"FixturePassword123","totp_secret":"JBSWY3DPEHPK3PXP"}]}`, i)
			req, err := http.NewRequest("POST", server.URL+path, strings.NewReader(body))
			if err != nil {
				t.Error(err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", fmt.Sprintf("queue-http-%d", i))
			if i == 0 {
				req.AddCookie(&http.Cookie{Name: "refund_session", Value: cookie})
			} else {
				req.Header.Set("Authorization", "Bearer "+key)
			}
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			var data struct {
				Status string   `json:"status"`
				IDs    []string `json:"ids"`
			}
			if err = json.NewDecoder(resp.Body).Decode(&data); err != nil {
				t.Error(err)
				return
			}
			if resp.StatusCode != 202 || data.Status != "queued" || len(data.IDs) != 1 {
				t.Errorf("%s not queued: %d %+v", path, resp.StatusCode, data)
			}
		}(i, path)
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	items, total, err := s.ListRefunds(ctx, u.ID, "", "queued", 1, 20)
	must(err)
	if total != 2 || len(items) != 2 {
		t.Fatal("submission lost", total)
	}
	for _, path := range []string{"/api/refunds/", "/open/v1/requests/"} {
		req, err := http.NewRequest("GET", server.URL+path+items[0].ID, nil)
		must(err)
		if strings.HasPrefix(path, "/api/") {
			req.AddCookie(&http.Cookie{Name: "refund_session", Value: cookie})
		} else {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := server.Client().Do(req)
		must(err)
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		must(err)
		if resp.StatusCode != 200 || !strings.Contains(string(raw), "申请正在排队") || !strings.Contains(string(raw), "开始处理前可撤回") {
			t.Fatal("missing customer queue explanation", string(raw))
		}
		for _, secret := range []string{"mother", "proxy", "team_id", "export_and_deactivate", "母号", "团队", "代理"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("queue explanation leaked internal resource", secret)
			}
		}
	}
	owner, err := s.User(ctx, u.ID)
	must(err)
	if owner.Balance != 9000 || owner.Frozen != 6000 {
		t.Fatal("queue should freeze without settling", owner)
	}
	job, err := s.ClaimRefund(ctx)
	must(err)
	if job != nil {
		t.Fatal("claimed without a mother")
	}
	p, err := s.SaveRefundProxy(ctx, a.ID, store.RefundProxy{Name: "fixture", URL: "http://proxy.invalid:8080", Enabled: true})
	must(err)
	_, err = s.SaveRefundMother(ctx, a.ID, store.RefundMother{Name: "fixture", Email: "mother@test.invalid", TeamID: "fixture-team", ProxyID: p, SeatType: "default", AccessToken: "fixture-private-at", Enabled: true})
	must(err)
	must(s.SaveRefundSettings(ctx, a.ID, store.RefundSettings{ChildProxyID: p, Enabled: true, MaxParallel: 2, LoginAttempts: 1, RetrySeconds: 5}))
	var first string
	must(s.Pool.QueryRow(ctx, "SELECT id FROM refunds WHERE user_id=$1 ORDER BY created_at,id LIMIT 1", u.ID).Scan(&first))
	job, err = s.ClaimRefund(ctx)
	must(err)
	if job == nil || job.ID != first {
		t.Fatal("queue did not resume in acceptance order")
	}
	next, err := s.ClaimRefund(ctx)
	must(err)
	if next != nil {
		t.Fatal("same mother ran a second request concurrently")
	}
	for _, method := range []string{"inviting", "invited", "login", "merge_v2_ready", "merge_v2_submitting", "merge_v2_accepted", "removing", "completed"} {
		must(s.RefundCheckpoint(ctx, job, store.Checkpoint{Method: method, TeamID: job.Mother.TeamID, UserID: "fixture-child"}))
	}
	next, err = s.ClaimRefund(ctx)
	must(err)
	if next == nil || next.ID == job.ID || next.Mother.ID != job.Mother.ID {
		t.Fatal("released mother did not take next queued request")
	}
}
