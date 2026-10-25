package store_test

import (
	"context"
	"encoding/json"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"strings"
	"testing"
	"time"
)

func TestResourceCheckSnapshotAndConfigurationInvalidation(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	u := testutil.Member(t, s, a, "probe@test.invalid", "member")
	pid, err := s.SaveRefundProxy(ctx, a.ID, store.RefundProxy{Name: "probe", URL: "host.invalid:8080:user:private-proxy", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := motherSessionFixture(t)
	mid, err := s.SaveRefundMother(ctx, a.ID, store.RefundMother{Name: "probe", Session: raw, ProxyID: pid, SeatType: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareProxyProbe(ctx, u.ID, store.RefundProxy{ID: pid}); err == nil {
		t.Fatal("member read secret")
	}
	url, err := s.PrepareProxyProbe(ctx, a.ID, store.RefundProxy{ID: pid})
	if err != nil || url != "http://user:private-proxy@host.invalid:8080" {
		t.Fatal("saved proxy resolution failed", err)
	}
	url, err = s.PrepareProxyProbe(ctx, a.ID, store.RefundProxy{URL: "127.0.0.1:8080:fixture:password"})
	if err != nil || url != "http://fixture:password@127.0.0.1:8080" {
		t.Fatal("unsaved shorthand failed", err)
	}
	started := time.Now().UTC()
	result := store.ResourceCheck{Valid: true, HTTPStatus: 200, Code: "ok", Message: "检查通过", CheckedAt: started, Seats: &store.SeatCapacity{Standard: &store.SeatCount{Total: 8, Remaining: 3, Held: 2, Used: 5}}}
	for _, v := range []struct{ kind, id string }{{"proxy", pid}, {"mother", mid}} {
		if err = s.SaveResourceCheck(ctx, a.ID, v.kind, v.id, started, result); err != nil {
			t.Fatal(err)
		}
	}
	ms, err := s.RefundMothers(ctx)
	if err != nil || ms[0].Check == nil || ms[0].Check.Seats.Standard.Total != 8 {
		t.Fatal("mother check not persisted", err)
	}
	ps, err := s.RefundProxies(ctx)
	if err != nil || ps[0].Check == nil {
		t.Fatal("proxy check not persisted", err)
	}
	encoded, _ := json.Marshal([]any{ms, ps})
	if strings.Contains(string(encoded), "private-proxy") {
		t.Fatal("snapshot leaked credentials")
	}
	// An edited proxy invalidates both its own check and dependent mother checks.
	_, err = s.SaveRefundProxy(ctx, a.ID, store.RefundProxy{ID: pid, Name: "updated", URL: "host.invalid:8081:user:changed", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ kind, id string }{{"proxy", pid}, {"mother", mid}} {
		if err = s.SaveResourceCheck(ctx, a.ID, v.kind, v.id, started, result); err != nil {
			t.Fatal(err)
		}
	}
	ms, _ = s.RefundMothers(ctx)
	ps, _ = s.RefundProxies(ctx)
	if ms[0].Check != nil || ps[0].Check != nil {
		t.Fatal("stale check survived changed proxy")
	}
	started = time.Now().UTC()
	result.CheckedAt = started
	if err = s.SaveResourceCheck(ctx, a.ID, "mother", mid, started, result); err != nil {
		t.Fatal(err)
	}
	m := ms[0]
	m.Name = "changed"
	if _, err = s.SaveRefundMother(ctx, a.ID, m); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveResourceCheck(ctx, a.ID, "mother", mid, started, result); err != nil {
		t.Fatal(err)
	}
	ms, _ = s.RefundMothers(ctx)
	if ms[0].Check != nil {
		t.Fatal("old check restored after mother edit")
	}
}
